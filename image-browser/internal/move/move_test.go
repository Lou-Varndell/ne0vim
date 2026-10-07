package move

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"image-browser/internal/db"
	"image-browser/internal/store"
)

// newTestStore opens a fresh, isolated inventory database for the
// duration of t and points INVENTORY_MANAGER_DB at it so Run (which opens
// the database itself via db.DefaultPath) uses the same one.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	t.Setenv("INVENTORY_MANAGER_DB", path)
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return store.New(database)
}

func writeImage(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordSpec is one database file+image row to seed for a test.
type recordSpec struct {
	path         string
	writeFile    bool
	content      string
	site         string
	sourceURL    string
	original     string
	status       string
	discoveredAt time.Time
	width        int
	height       int
}

func seedRecord(t *testing.T, inv *store.Store, spec recordSpec) {
	t.Helper()
	ctx := context.Background()

	if spec.writeFile {
		writeImage(t, spec.path, spec.content)
	}

	id, _, err := inv.AddFile(ctx, store.File{
		Path:         spec.path,
		Filename:     filepath.Base(spec.path),
		Extension:    filepath.Ext(spec.path),
		Filesize:     int64(len(spec.content)),
		Site:         sql.NullString{String: spec.site, Valid: spec.site != ""},
		SourceURL:    sql.NullString{String: spec.sourceURL, Valid: spec.sourceURL != ""},
		OriginalFile: sql.NullString{String: spec.original, Valid: spec.original != ""},
		DiscoveredAt: sql.NullTime{Time: spec.discoveredAt, Valid: !spec.discoveredAt.IsZero()},
		Status:       sql.NullString{String: spec.status, Valid: spec.status != ""},
	})
	if err != nil {
		t.Fatal(err)
	}

	if spec.width > 0 || spec.height > 0 {
		if _, err := inv.AddImage(ctx, store.Image{FileID: id, Width: spec.width, Height: spec.height}); err != nil {
			t.Fatal(err)
		}
	}
}

func allRecords(t *testing.T, inv *store.Store) []store.FileRecord {
	t.Helper()
	all, err := inv.AllFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return all
}

func recordByPath(t *testing.T, inv *store.Store, path string) (store.FileRecord, bool) {
	t.Helper()
	for _, r := range allRecords(t, inv) {
		if r.Path == path {
			return r, true
		}
	}
	return store.FileRecord{}, false
}

func TestCriteriaMatchesSiteCaseInsensitiveContains(t *testing.T) {
	c := criteria{site: "BlueBird"}
	r := store.FileRecord{Site: "https://www.wildlife.com/galleries/bluebird-23016175/"}
	if !c.matches(r) {
		t.Fatal("expected site filter to match case-insensitively")
	}
	if c.matches(store.FileRecord{Site: "https://www.wildlife.com/galleries/owl/"}) {
		t.Fatal("expected non-matching site to be rejected")
	}
}

func TestCriteriaMatchesCombinesWithAND(t *testing.T) {
	c := criteria{site: "bluebird", hasWidth: true, width: 1024}
	match := store.FileRecord{Site: "bluebird-gallery", Width: 1024}
	noMatch := store.FileRecord{Site: "bluebird-gallery", Width: 768}
	if !c.matches(match) {
		t.Fatal("expected record matching both criteria to match")
	}
	if c.matches(noMatch) {
		t.Fatal("expected record matching only one criterion to be rejected")
	}
}

func TestCriteriaMatchesDiscoveredAtComparesDateOnly(t *testing.T) {
	c := criteria{discoveredAt: "2026-10-02"}
	r := store.FileRecord{DiscoveredAt: time.Date(2026, 10, 2, 23, 59, 0, 0, time.UTC)}
	if !c.matches(r) {
		t.Fatal("expected same-day record to match regardless of time of day")
	}
	other := store.FileRecord{DiscoveredAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	if c.matches(other) {
		t.Fatal("expected different-day record to be rejected")
	}
}

func TestCriteriaMatchesHashAndStatusExact(t *testing.T) {
	c := criteria{hash: "ABCD", status: "Kept"}
	if !c.matches(store.FileRecord{Hash: "abcd", Status: "kept"}) {
		t.Fatal("expected case-insensitive exact match on hash and status")
	}
	if c.matches(store.FileRecord{Hash: "abcde", Status: "kept"}) {
		t.Fatal("expected hash to require exact match, not substring")
	}
}

func TestDeriveDestNameJoinsActiveCriteria(t *testing.T) {
	got := deriveDestName(criteria{site: "bluebird", hasWidth: true, width: 1024})
	if want := "bluebird-1024"; got != want {
		t.Fatalf("deriveDestName() = %q, want %q", got, want)
	}
}

func TestSanitizeDirNameStripsPathSeparators(t *testing.T) {
	got := sanitizeDirName("https://www.wildlife.com/galleries/bluebird/")
	if filepath.Base(got) != got {
		t.Fatalf("sanitizeDirName() = %q, contains path separators", got)
	}
}

func TestUnderRoot(t *testing.T) {
	root := "/images/gallery"
	cases := []struct {
		path string
		want bool
	}{
		{"/images/gallery", true},
		{"/images/gallery/photo.jpg", true},
		{"/images/gallery/sub/photo.jpg", true},
		{"/images/galleryx/photo.jpg", false},
		{"/images/other/photo.jpg", false},
	}
	for _, c := range cases {
		if got := underRoot(c.path, root); got != c.want {
			t.Errorf("underRoot(%q, %q) = %v, want %v", c.path, root, got, c.want)
		}
	}
}

// setupRecords seeds a fresh database with records analogous to two
// manifest.json files' worth of entries, under distinct subdirectories of
// root, so tests can exercise move's filtering and relocation.
// gallery-b's missing.jpg record deliberately has no file written for it,
// to exercise the missing-record pruning path.
func setupRecords(t *testing.T) (root string, inv *store.Store) {
	t.Helper()
	inv = newTestStore(t)
	root = t.TempDir()

	galleryA := filepath.Join(root, "gallery-a")
	galleryB := filepath.Join(root, "gallery-b")

	seedRecord(t, inv, recordSpec{
		path: filepath.Join(galleryA, "a.jpg"), writeFile: true, content: "image-a",
		site: "https://www.wildlife.com/galleries/bluebird-1/", sourceURL: "https://cdn/a.jpg",
		status: "processed", discoveredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
		width: 1024, height: 768,
	})
	seedRecord(t, inv, recordSpec{
		path: filepath.Join(galleryA, "b.jpg"), writeFile: true, content: "image-b",
		site: "https://www.wildlife.com/galleries/bluebird-2/", sourceURL: "https://cdn/b.jpg",
		status: "kept", discoveredAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC),
		width: 500, height: 500,
	})
	seedRecord(t, inv, recordSpec{
		path: filepath.Join(galleryB, "c.jpg"), writeFile: true, content: "image-c",
		site: "https://www.wildlife.com/galleries/owl-1/", sourceURL: "https://cdn/c.jpg",
		status: "processed", discoveredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC),
		width: 1024, height: 768,
	})
	seedRecord(t, inv, recordSpec{
		path: filepath.Join(galleryB, "missing.jpg"), writeFile: false,
		site: "https://www.wildlife.com/galleries/owl-2/", sourceURL: "https://cdn/missing.jpg",
		status: "processed",
	})

	return root, inv
}

func TestRunMovesMatchesAcrossMultipleRecords(t *testing.T) {
	root, inv := setupRecords(t)
	dest := filepath.Join(t.TempDir(), "bluebird-out")

	if err := Run([]string{"bluebird", "-root", root, "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "a.jpg")); err != nil {
		t.Fatalf("expected a.jpg moved to dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "b.jpg")); err != nil {
		t.Fatalf("expected b.jpg moved to dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gallery-a", "a.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected source a.jpg removed, got err=%v", err)
	}

	aRecord, ok := recordByPath(t, inv, filepath.Join(dest, "a.jpg"))
	if !ok {
		t.Fatal("expected a database record at the new a.jpg path")
	}
	if aRecord.Site == "" {
		t.Fatalf("expected provenance preserved across the move, got %+v", aRecord)
	}

	// The owl gallery's c.jpg record must not have moved since nothing in
	// it matched the bluebird filter; its missing.jpg record is pruned
	// regardless, since Run always prunes records whose file no longer
	// exists before matching.
	if _, ok := recordByPath(t, inv, filepath.Join(root, "gallery-b", "missing.jpg")); ok {
		t.Fatal("expected the missing-file record to be pruned")
	}
	if _, ok := recordByPath(t, inv, filepath.Join(root, "gallery-b", "c.jpg")); !ok {
		t.Fatal("expected owl's c.jpg record to remain untouched at its original path")
	}
}

func TestRunPrunesMissingFileEntryWithoutAbortingOtherMatches(t *testing.T) {
	root, inv := setupRecords(t)
	dest := filepath.Join(t.TempDir(), "owl-out")

	if err := Run([]string{"-root", root, "-site", "owl", "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dest, "c.jpg")); statErr != nil {
		t.Fatalf("expected owl-1's c.jpg to still have moved despite owl-2's missing file: %v", statErr)
	}

	if _, ok := recordByPath(t, inv, filepath.Join(root, "gallery-b", "missing.jpg")); ok {
		t.Fatal("expected owl-2's record for a nonexistent file to have been pruned, not moved")
	}
}

func TestRunStatusFilterExactMatch(t *testing.T) {
	root, _ := setupRecords(t)
	dest := filepath.Join(t.TempDir(), "kept-out")

	if err := Run([]string{"-root", root, "-status", "kept", "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "b.jpg")); err != nil {
		t.Fatalf("expected kept record's file moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected processed record's file NOT moved, got err=%v", err)
	}
}

func TestRunDuplicateContentIsLeftInPlace(t *testing.T) {
	root, _ := setupRecords(t)
	dest := filepath.Join(t.TempDir(), "dup-out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed the destination with a file whose content matches a.jpg
	// exactly, under a different name, so ResolveDestination's
	// content-hash check reports it as a duplicate.
	writeImage(t, filepath.Join(dest, "already-there.jpg"), "image-a")

	if err := Run([]string{"-root", root, "-site", "bluebird-1", "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "gallery-a", "a.jpg")); err != nil {
		t.Fatalf("expected duplicate source file left in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected no new copy created for duplicate, got err=%v", err)
	}
}

func TestRunNoFilterGivenIsAnError(t *testing.T) {
	root, _ := setupRecords(t)
	if err := Run([]string{"-root", root}); err == nil {
		t.Fatal("expected an error when no filter is given")
	}
}

func TestRunPositionalValueConflictsWithSiteFlag(t *testing.T) {
	root, _ := setupRecords(t)
	if err := Run([]string{"bluebird", "-site", "owl", "-root", root}); err == nil {
		t.Fatal("expected an error when a positional value and -site are both given")
	}
}

func TestRunNoRecordsFoundUnderRoot(t *testing.T) {
	newTestStore(t)
	root := t.TempDir()
	if err := Run([]string{"bluebird", "-root", root}); err != nil {
		t.Fatalf("Run() error: %v, want nil (just nothing to do)", err)
	}
}

func TestRunDerivesDestFromMatchValueWhenDestNotGiven(t *testing.T) {
	root, _ := setupRecords(t)

	cwd := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	if err := Run([]string{"bluebird-1", "-root", root}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(cwd, "bluebird-1", "a.jpg")); err != nil {
		t.Fatalf("expected dest directory derived from match value: %v", err)
	}
}

// TestPerformMovesIsIdempotentOnRetry guards the mechanism previewMove's
// Move All button relies on: calling performMoves again on the exact same
// matched slice after a successful move must be a safe no-op (the
// already-moved record is now found sitting in destDir itself and reported
// as a duplicate of itself) rather than an error or a double-move. This is
// what lets a retry after a partial failure, or a destination change
// mid-session, both be implemented as "just call performMoves again".
func TestPerformMovesIsIdempotentOnRetry(t *testing.T) {
	root, inv := setupRecords(t)
	dest := filepath.Join(t.TempDir(), "bluebird-out")
	ctx := context.Background()

	matched, err := loadAndFilter(ctx, inv, root, criteria{site: "bluebird"})
	if err != nil {
		t.Fatal(err)
	}
	if len(matched) != 2 {
		t.Fatalf("loadAndFilter() matched %d, want 2", len(matched))
	}

	moved, duplicates, failureMsgs, movedPaths, errs := performMoves(ctx, inv, matched, dest)
	if len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("first performMoves(): errs=%v failures=%v, want none", errs, failureMsgs)
	}
	if moved != 2 || duplicates != 0 || len(movedPaths) != 2 {
		t.Fatalf("first performMoves() = moved=%d duplicates=%d movedPaths=%v, want moved=2 duplicates=0 len(movedPaths)=2", moved, duplicates, movedPaths)
	}

	// Re-running against the same (now-mutated) matched slice must not
	// error, re-move, or lose track of the files: everything should
	// resolve as a duplicate of itself and nothing new should move.
	moved, duplicates, failureMsgs, movedPaths, errs = performMoves(ctx, inv, matched, dest)
	if len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("retry performMoves(): errs=%v failures=%v, want none", errs, failureMsgs)
	}
	if moved != 0 || duplicates != 2 || len(movedPaths) != 0 {
		t.Fatalf("retry performMoves() = moved=%d duplicates=%d movedPaths=%v, want moved=0 duplicates=2 len(movedPaths)=0", moved, duplicates, movedPaths)
	}

	if _, err := os.Stat(filepath.Join(dest, "a.jpg")); err != nil {
		t.Fatalf("expected a.jpg to still be at dest after retry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "b.jpg")); err != nil {
		t.Fatalf("expected b.jpg to still be at dest after retry: %v", err)
	}

	// Regression guard: both moved records must have their database path
	// updated to dest, not left pointing at their original (now
	// nonexistent) location.
	if _, ok := recordByPath(t, inv, filepath.Join(root, "gallery-a", "a.jpg")); ok {
		t.Fatal("expected a.jpg's old path to no longer have a record")
	}
	if _, ok := recordByPath(t, inv, filepath.Join(root, "gallery-a", "b.jpg")); ok {
		t.Fatal("expected b.jpg's old path to no longer have a record")
	}
}

// TestPerformMovesRelocatesAlreadyMovedFilesWhenDestinationChanges guards
// the "Change Destination" scenario: redirecting a subsequent
// performMoves call to a new destination after an earlier call already
// moved files into the first destination must relocate those files again,
// since the records' Path now points at their current (first-destination)
// location, not their original one.
func TestPerformMovesRelocatesAlreadyMovedFilesWhenDestinationChanges(t *testing.T) {
	root, inv := setupRecords(t)
	firstDest := filepath.Join(t.TempDir(), "first")
	secondDest := filepath.Join(t.TempDir(), "second")
	ctx := context.Background()

	matched, err := loadAndFilter(ctx, inv, root, criteria{site: "bluebird"})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, failureMsgs, _, errs := performMoves(ctx, inv, matched, firstDest); len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("performMoves(firstDest): errs=%v failures=%v, want none", errs, failureMsgs)
	}

	moved, _, failureMsgs, _, errs := performMoves(ctx, inv, matched, secondDest)
	if len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("performMoves(secondDest): errs=%v failures=%v, want none", errs, failureMsgs)
	}
	if moved != 2 {
		t.Fatalf("performMoves(secondDest) moved = %d, want 2", moved)
	}

	if _, err := os.Stat(filepath.Join(secondDest, "a.jpg")); err != nil {
		t.Fatalf("expected a.jpg relocated to the new destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(firstDest, "a.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected a.jpg no longer at the first destination, got err=%v", err)
	}

	if _, ok := recordByPath(t, inv, filepath.Join(firstDest, "a.jpg")); ok {
		t.Fatal("expected the first-destination path to no longer have a record")
	}
	if _, ok := recordByPath(t, inv, filepath.Join(secondDest, "a.jpg")); !ok {
		t.Fatal("expected the second-destination path to have the record")
	}
}
