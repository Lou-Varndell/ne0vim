package move

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"image-browser/internal/manifest"
)

func writeManifest(t *testing.T, path string, entries []manifest.Entry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
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

func readManifest(t *testing.T, path string) []manifest.Entry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := manifest.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCriteriaMatchesSiteCaseInsensitiveContains(t *testing.T) {
	c := criteria{site: "BlueBird"}
	e := manifest.Entry{Site: "https://www.wildlife.com/galleries/bluebird-23016175/"}
	if !c.matches(e) {
		t.Fatal("expected site filter to match case-insensitively")
	}
	if c.matches(manifest.Entry{Site: "https://www.wildlife.com/galleries/owl/"}) {
		t.Fatal("expected non-matching site to be rejected")
	}
}

func TestCriteriaMatchesCombinesWithAND(t *testing.T) {
	c := criteria{site: "bluebird", hasWidth: true, width: 1024}
	match := manifest.Entry{Site: "bluebird-gallery", Width: 1024}
	noMatch := manifest.Entry{Site: "bluebird-gallery", Width: 768}
	if !c.matches(match) {
		t.Fatal("expected entry matching both criteria to match")
	}
	if c.matches(noMatch) {
		t.Fatal("expected entry matching only one criterion to be rejected")
	}
}

func TestCriteriaMatchesDiscoveredAtComparesDateOnly(t *testing.T) {
	c := criteria{discoveredAt: "2026-10-02"}
	e := manifest.Entry{DiscoveredAt: time.Date(2026, 10, 2, 23, 59, 0, 0, time.UTC)}
	if !c.matches(e) {
		t.Fatal("expected same-day entry to match regardless of time of day")
	}
	other := manifest.Entry{DiscoveredAt: time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)}
	if c.matches(other) {
		t.Fatal("expected different-day entry to be rejected")
	}
}

func TestCriteriaMatchesHashAndStatusExact(t *testing.T) {
	c := criteria{hash: "ABCD", status: "Kept"}
	if !c.matches(manifest.Entry{Hash: "abcd", Status: "kept"}) {
		t.Fatal("expected case-insensitive exact match on hash and status")
	}
	if c.matches(manifest.Entry{Hash: "abcde", Status: "kept"}) {
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

func TestIsManifestFileName(t *testing.T) {
	for _, name := range []string{"manifest.json", "image-manifest.json", "site-manifest.json"} {
		if !isManifestFileName(name) {
			t.Fatalf("isManifestFileName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"photo.jpg", "manifest.json.bak", "notes.txt"} {
		if isManifestFileName(name) {
			t.Fatalf("isManifestFileName(%q) = true, want false", name)
		}
	}
}

func TestFindManifestFilesReturnsSingleFileRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	writeManifest(t, path, nil)

	got, err := findManifestFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != path {
		t.Fatalf("findManifestFiles() = %v, want [%s]", got, path)
	}
}

func TestFindManifestFilesRejectsNonManifestFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	writeImage(t, path, "not a manifest")

	if _, err := findManifestFiles(path); err == nil {
		t.Fatal("expected an error for a file that does not look like a manifest")
	}
}

func TestFindManifestFilesWalksDirectoryRecursively(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, filepath.Join(root, "manifest.json"), nil)
	writeManifest(t, filepath.Join(root, "a", "image-manifest.json"), nil)
	writeManifest(t, filepath.Join(root, "a", "b", "manifest.json"), nil)
	writeImage(t, filepath.Join(root, "a", "photo.jpg"), "x")

	got, err := findManifestFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("findManifestFiles() found %d files, want 3: %v", len(got), got)
	}
}

// setupManifests writes two *manifest.json files under distinct
// subdirectories of root, each with its own images, so tests can exercise
// move's recursive, multi-manifest search.
func setupManifests(t *testing.T) (root string) {
	t.Helper()
	root = t.TempDir()

	galleryA := filepath.Join(root, "gallery-a")
	galleryB := filepath.Join(root, "gallery-b")

	writeImage(t, filepath.Join(galleryA, "a.jpg"), "image-a")
	writeImage(t, filepath.Join(galleryA, "b.jpg"), "image-b")
	writeImage(t, filepath.Join(galleryB, "c.jpg"), "image-c")
	// galleryB's manifest also references missing.jpg, which is
	// deliberately never written, to exercise the missing-file-entry
	// pruning path.

	writeManifest(t, filepath.Join(galleryA, "manifest.json"), []manifest.Entry{
		{
			Site: "https://www.wildlife.com/galleries/bluebird-1/", SourceURL: "https://cdn/a.jpg",
			File: filepath.Join(galleryA, "a.jpg"), Status: "processed",
			DiscoveredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), Width: 1024, Height: 768,
		},
		{
			Site: "https://www.wildlife.com/galleries/bluebird-2/", SourceURL: "https://cdn/b.jpg",
			File: filepath.Join(galleryA, "b.jpg"), Status: "kept",
			DiscoveredAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), Width: 500, Height: 500,
		},
	})

	writeManifest(t, filepath.Join(galleryB, "image-manifest.json"), []manifest.Entry{
		{
			Site: "https://www.wildlife.com/galleries/owl-1/", SourceURL: "https://cdn/c.jpg",
			File: filepath.Join(galleryB, "c.jpg"), Status: "processed",
			DiscoveredAt: time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC), Width: 1024, Height: 768,
		},
		{
			Site: "https://www.wildlife.com/galleries/owl-2/", SourceURL: "https://cdn/missing.jpg",
			File: filepath.Join(galleryB, "missing.jpg"), Status: "processed",
		},
	})

	return root
}

func TestRunMovesMatchesAcrossMultipleManifestFiles(t *testing.T) {
	root := setupManifests(t)
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

	entries := readManifest(t, filepath.Join(root, "gallery-a", "manifest.json"))
	for _, e := range entries {
		if !filepath.IsAbs(e.File) {
			t.Fatalf("entry %s File not absolute: %q", e.SourceURL, e.File)
		}
		if e.File != filepath.Join(dest, filepath.Base(e.File)) {
			t.Fatalf("entry %s not rewritten to dest, got %q", e.SourceURL, e.File)
		}
	}

	// The owl gallery's c.jpg entry must not have moved since nothing in
	// it matched the bluebird filter; its missing.jpg entry is pruned
	// regardless, since loading a manifest always drops entries for files
	// that no longer exist.
	owlEntries := readManifest(t, filepath.Join(root, "gallery-b", "image-manifest.json"))
	if len(owlEntries) != 1 {
		t.Fatalf("expected gallery-b's missing-file entry to be pruned on load, got %d entries: %+v", len(owlEntries), owlEntries)
	}
	for _, e := range owlEntries {
		if e.SourceURL == "https://cdn/c.jpg" && e.File != filepath.Join(root, "gallery-b", "c.jpg") {
			t.Fatalf("owl entry should not have been moved, got %q", e.File)
		}
	}

	destManifest := filepath.Join(dest, "image-manifest.json")
	destEntries := readManifest(t, destManifest)
	if len(destEntries) != 2 {
		t.Fatalf("expected 2 entries mirrored into destination manifest, got %d", len(destEntries))
	}
}

func TestRunPrunesMissingFileEntryWithoutAbortingOtherMatches(t *testing.T) {
	root := setupManifests(t)
	dest := filepath.Join(t.TempDir(), "owl-out")

	if err := Run([]string{"-root", root, "-site", "owl", "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(dest, "c.jpg")); statErr != nil {
		t.Fatalf("expected owl-1's c.jpg to still have moved despite owl-2's missing file: %v", statErr)
	}

	entries := readManifest(t, filepath.Join(dest, "image-manifest.json"))
	for _, e := range entries {
		if e.SourceURL == "https://cdn/missing.jpg" {
			t.Fatalf("owl-2's entry for a nonexistent file should have been pruned, not moved: %+v", e)
		}
	}
}

func TestRunStatusFilterExactMatch(t *testing.T) {
	root := setupManifests(t)
	dest := filepath.Join(t.TempDir(), "kept-out")

	if err := Run([]string{"-root", root, "-status", "kept", "-dest", dest}); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dest, "b.jpg")); err != nil {
		t.Fatalf("expected kept entry's file moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected processed entry's file NOT moved, got err=%v", err)
	}
}

func TestRunDuplicateContentIsLeftInPlace(t *testing.T) {
	root := setupManifests(t)
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
	root := setupManifests(t)
	if err := Run([]string{"-root", root}); err == nil {
		t.Fatal("expected an error when no filter is given")
	}
}

func TestRunPositionalValueConflictsWithSiteFlag(t *testing.T) {
	root := setupManifests(t)
	if err := Run([]string{"bluebird", "-site", "owl", "-root", root}); err == nil {
		t.Fatal("expected an error when a positional value and -site are both given")
	}
}

func TestRunNoManifestFilesFoundUnderRoot(t *testing.T) {
	root := t.TempDir()
	if err := Run([]string{"bluebird", "-root", root}); err != nil {
		t.Fatalf("Run() error: %v, want nil (just nothing to do)", err)
	}
}

func TestRunDerivesDestFromMatchValueWhenDestNotGiven(t *testing.T) {
	root := setupManifests(t)

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
// toProcess after a successful move must be a safe no-op (the already-moved
// entry is now found sitting in destDir itself and reported as a duplicate
// of itself) rather than an error or a double-move. This is what lets a
// retry after a partial failure, or a destination change mid-session, both
// be implemented as "just call performMoves again".
func TestPerformMovesIsIdempotentOnRetry(t *testing.T) {
	root := setupManifests(t)
	dest := filepath.Join(t.TempDir(), "bluebird-out")

	manifestFiles, err := findManifestFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	toProcess, totalMatched, errs := loadAndFilter(manifestFiles, criteria{site: "bluebird"})
	if len(errs) != 0 || totalMatched != 2 {
		t.Fatalf("loadAndFilter() matched=%d errs=%v, want 2 matches and no errors", totalMatched, errs)
	}

	moved, duplicates, failureMsgs, movedPaths, errs := performMoves(toProcess, dest)
	if len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("first performMoves(): errs=%v failures=%v, want none", errs, failureMsgs)
	}
	if moved != 2 || duplicates != 0 || len(movedPaths) != 2 {
		t.Fatalf("first performMoves() = moved=%d duplicates=%d movedPaths=%v, want moved=2 duplicates=0 len(movedPaths)=2", moved, duplicates, movedPaths)
	}

	// Re-running against the same (now-mutated) toProcess must not error,
	// re-move, or lose track of the files: everything should resolve as a
	// duplicate of itself and nothing new should move.
	moved, duplicates, failureMsgs, movedPaths, errs = performMoves(toProcess, dest)
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

	// Regression guard: both moved entries must be gone from gallery-a's
	// own manifest on disk, not merely rewritten to point at dest — a
	// stale rewritten entry is exactly what caused a fresh `move`
	// invocation to find it, see its filter still match, and move the
	// (already-moved) file a second time.
	remaining := readManifest(t, filepath.Join(root, "gallery-a", "manifest.json"))
	if len(remaining) != 0 {
		t.Fatalf("gallery-a manifest = %+v, want 0 entries (moved entries should be removed, not rewritten in place)", remaining)
	}
}

// TestPerformMovesRelocatesAlreadyMovedFilesWhenDestinationChanges guards
// the "Change Destination" scenario: redirecting a subsequent
// performMoves call to a new destination after an earlier call already
// moved files into the first destination must relocate those files again,
// since the entries' File now points at their current (first-destination)
// location, not their original one.
func TestPerformMovesRelocatesAlreadyMovedFilesWhenDestinationChanges(t *testing.T) {
	root := setupManifests(t)
	firstDest := filepath.Join(t.TempDir(), "first")
	secondDest := filepath.Join(t.TempDir(), "second")

	manifestFiles, err := findManifestFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	toProcess, _, errs := loadAndFilter(manifestFiles, criteria{site: "bluebird"})
	if len(errs) != 0 {
		t.Fatalf("loadAndFilter() errs=%v, want none", errs)
	}

	if _, _, failureMsgs, _, errs := performMoves(toProcess, firstDest); len(errs) != 0 || len(failureMsgs) != 0 {
		t.Fatalf("performMoves(firstDest): errs=%v failures=%v, want none", errs, failureMsgs)
	}

	moved, _, failureMsgs, _, errs := performMoves(toProcess, secondDest)
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

	remaining := readManifest(t, filepath.Join(root, "gallery-a", "manifest.json"))
	if len(remaining) != 0 {
		t.Fatalf("gallery-a manifest = %+v, want 0 entries (moved entries should be removed, not rewritten in place)", remaining)
	}
}

// TestPreviewPathsSkipsEntriesWithNoFile guards against resolveSrc("",
// manifestDir) resolving to the manifest's own directory: without the
// skip, an entry with no File at all would hand the preview viewer a bare
// directory path it can neither thumbnail nor open.
func TestPreviewPathsSkipsEntriesWithNoFile(t *testing.T) {
	root := t.TempDir()
	gallery := filepath.Join(root, "gallery")
	writeImage(t, filepath.Join(gallery, "a.jpg"), "image-a")

	writeManifest(t, filepath.Join(gallery, "manifest.json"), []manifest.Entry{
		{Site: "bluebird", File: filepath.Join(gallery, "a.jpg"), Status: "processed"},
		{Site: "bluebird", File: "", Status: "invalid"},
	})

	manifestFiles, err := findManifestFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	toProcess, totalMatched, errs := loadAndFilter(manifestFiles, criteria{site: "bluebird"})
	if len(errs) != 0 || totalMatched != 2 {
		t.Fatalf("loadAndFilter() matched=%d errs=%v, want 2 matches and no errors", totalMatched, errs)
	}

	paths := previewPaths(toProcess)
	want := filepath.Join(gallery, "a.jpg")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("previewPaths() = %v, want [%q]", paths, want)
	}
}
