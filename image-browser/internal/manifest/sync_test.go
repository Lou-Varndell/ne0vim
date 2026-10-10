package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

// moveFile physically relocates src to dest, mirroring what SyncMoves'
// caller always does before calling SyncMoves (see its doc comment: it
// "keeps *manifest.json files consistent after a set of images is moved on
// disk"). Tests need the real move, not just the moves map entry, now that
// syncManifestFile also prunes any entry whose File no longer resolves to a
// file on disk — src gone from its old spot, dest present at its new one.
func moveFile(t *testing.T, src, dest string) {
	t.Helper()
	if err := os.Rename(src, dest); err != nil {
		t.Fatal(err)
	}
}

// readEntries reads path and decodes it via the package's own Decode,
// which flattens any of the three recognized manifest shapes to a plain
// []Entry — handy for tests that only care about which entries survived,
// not which shape they're stored in.
func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := Decode(raw)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return entries
}

func TestSyncMovesRemovesMovedEntryFromArrayManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	manifestPath := filepath.Join(dir, "image-manifest.json")
	writeFile(t, manifestPath, `[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed"},{"site":"s","source_url":"u2","file":"other.jpg","status":"processed"}]`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	// The matched entry is dropped from the source manifest entirely — a
	// manifest only describes images in its own directory tree, and the
	// image just moved out of it — rather than rewritten in place to point
	// at dest. other.jpg was never written to disk, so its entry is pruned
	// too.
	entries := readEntries(t, manifestPath)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want 0", entries)
	}

	destManifest := filepath.Join(filepath.Dir(dest), "image-manifest.json")
	destEntries := readEntries(t, destManifest)
	if len(destEntries) != 1 || destEntries[0].File != dest {
		t.Fatalf("destination manifest = %+v, want 1 entry with File %q", destEntries, dest)
	}
}

// TestSyncMovesMatchesAbsoluteFileWrittenThroughADifferentPathToTheSameDir
// guards against a real failure mode: a manifest written by a separate
// ingestion process can store an absolute File computed via a different
// route (e.g. through a symlink the writer resolved and this process
// didn't) than the one SyncMoves' caller used to build moves' keys, even
// though both name the same on-disk file. The match must still succeed
// because resolveMove keys off manifestDir + the entry's base filename, not
// File's string verbatim.
func TestSyncMovesMatchesAbsoluteFileWrittenThroughADifferentPathToTheSameDir(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "via-symlink")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	src := filepath.Join(real, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	// The manifest's File uses the symlinked route to the same directory,
	// not the real path SyncMoves' caller used to build the moves map.
	entryFile := filepath.Join(link, "photo.jpg")
	manifestPath := filepath.Join(real, "image-manifest.json")
	writeFile(t, manifestPath, `[{"site":"s","source_url":"u","file":"`+entryFile+`","status":"processed"}]`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	// The match must still succeed and remove the entry from the source
	// manifest, even though the match itself went through the symlinked
	// route.
	entries := readEntries(t, manifestPath)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want 0 (match should survive a differently-rooted absolute path and be removed)", entries)
	}

	destManifest := filepath.Join(filepath.Dir(dest), "image-manifest.json")
	destEntries := readEntries(t, destManifest)
	if len(destEntries) != 1 || destEntries[0].File != dest {
		t.Fatalf("destination manifest = %+v, want 1 entry with File %q", destEntries, dest)
	}
}

// TestSyncMovesUpdatesEveryInvolvedSourceDirectoryIndependently moves files
// out of four different source directories in one call — an array-shape
// manifest, an object-shape manifest, a manifest whose only entry
// references a file that doesn't exist (and so gets pruned regardless of
// the move), and a directory with no manifest at all — and checks every
// directory that has a manifest got it updated, independently of what the
// others look like.
func TestSyncMovesUpdatesEveryInvolvedSourceDirectoryIndependently(t *testing.T) {
	arrayDir := t.TempDir()
	objectDir := t.TempDir()
	unrelatedDir := t.TempDir()
	noManifestDir := t.TempDir()
	destDir := t.TempDir()

	arraySrc := filepath.Join(arrayDir, "a.jpg")
	objectSrc := filepath.Join(objectDir, "b.jpg")
	unrelatedSrc := filepath.Join(unrelatedDir, "c.jpg")
	noManifestSrc := filepath.Join(noManifestDir, "d.jpg")
	for _, p := range []string{arraySrc, objectSrc, unrelatedSrc, noManifestSrc} {
		writeFile(t, p, "img")
	}

	arrayDest := filepath.Join(destDir, "a.jpg")
	objectDest := filepath.Join(destDir, "b.jpg")
	unrelatedDest := filepath.Join(destDir, "c.jpg")
	noManifestDest := filepath.Join(destDir, "d.jpg")

	arrayManifest := filepath.Join(arrayDir, "image-manifest.json")
	writeFile(t, arrayManifest, `[{"site":"s","source_url":"u","file":"a.jpg","status":"processed"}]`)

	objectManifest := filepath.Join(objectDir, "manifest.json")
	writeFile(t, objectManifest, `{"site":"s","source_url":"u","file":"b.jpg","status":"kept"}`)

	unrelatedManifest := filepath.Join(unrelatedDir, "manifest.json")
	writeFile(t, unrelatedManifest, `{"site":"s","source_url":"u","file":"not-this-one.jpg","status":"kept"}`)

	moves := map[string]string{
		arraySrc:      arrayDest,
		objectSrc:     objectDest,
		unrelatedSrc:  unrelatedDest,
		noManifestSrc: noManifestDest,
	}
	for src, dest := range moves {
		moveFile(t, src, dest)
	}
	if err := SyncMoves(moves); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	// Both the array and object manifests had their only entry match the
	// move, so each is dropped from the source manifest entirely rather
	// than rewritten in place — the destination checks below confirm it
	// landed correctly instead.
	arrayEntries := readEntries(t, arrayManifest)
	if len(arrayEntries) != 0 {
		t.Fatalf("array manifest = %+v, want 0 (moved entry should be removed)", arrayEntries)
	}

	objectEntries := readEntries(t, objectManifest)
	if len(objectEntries) != 0 {
		t.Fatalf("object manifest = %+v, want 0 (moved entry should be removed)", objectEntries)
	}

	unrelatedEntries := readEntries(t, unrelatedManifest)
	if len(unrelatedEntries) != 0 {
		t.Fatalf("entry referencing a nonexistent file should have been pruned, got %+v", unrelatedEntries)
	}

	destManifest := filepath.Join(destDir, "image-manifest.json")
	destEntries := readEntries(t, destManifest)
	// Only arraySrc and objectSrc had a source manifest entry to carry
	// over; unrelatedSrc and noManifestSrc did not, so they are not
	// mirrored into the destination manifest.
	if len(destEntries) != 2 {
		t.Fatalf("len(destEntries) = %d, want 2: %+v", len(destEntries), destEntries)
	}
}

func TestSyncMovesRemovesMovedEntryFromObjectManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	manifestPath := filepath.Join(dir, "manifest.json")
	writeFile(t, manifestPath, `{"site":"s","source_url":"u","file":"photo.jpg","status":"kept"}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	// The manifest's only entry matched the move, so it's dropped (not
	// rewritten in place); a bare single-object manifest with nothing left
	// in it is written back as an empty array (see encodeManifestFile).
	entries := readEntries(t, manifestPath)
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want 0 (the moved entry should be removed, not rewritten in place)", entries)
	}
}

// TestSyncMovesRemovesMovedEntryFromEntriesWrapperManifest guards against
// the real failure this shape caused: site-scraper always writes
// {"entries": [...]}, which previously fell through to the single-object
// unmarshal attempt and silently matched nothing (every field zero-valued,
// including File), so SyncMoves reported success while leaving the
// manifest completely untouched.
func TestSyncMovesRemovesMovedEntryFromEntriesWrapperManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	manifestPath := filepath.Join(dir, "manifest.json")
	writeFile(t, manifestPath, `{"entries":[{"site":"s","source_url":"u","file":"photo.jpg","status":"kept"},{"site":"s","source_url":"u2","file":"other.jpg","status":"kept"}]}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var w Wrapper
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("manifest is no longer a valid entries wrapper: %v", err)
	}
	// The matched entry is dropped (not rewritten in place) and other.jpg
	// was never written to disk, so its entry is pruned too.
	if len(w.Entries) != 0 {
		t.Fatalf("len(entries) = %d, want 0: %+v", len(w.Entries), w.Entries)
	}
}

func TestSyncMovesLeavesNonMatchingManifestUntouchedWhenFileStillExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	// unrelated.jpg stays on disk the whole time: this entry neither
	// matches the move nor references a missing file, so the manifest
	// must come through byte-for-byte unchanged.
	writeFile(t, filepath.Join(dir, "unrelated.jpg"), "img2")

	manifestPath := filepath.Join(dir, "manifest.json")
	original := `{"site":"s","source_url":"u","file":"unrelated.jpg","status":"kept"}`
	writeFile(t, manifestPath, original)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != original {
		t.Fatalf("manifest was rewritten despite no matching entry and an existing file: got %q, want %q", raw, original)
	}
}

// TestSyncMovesPrunesNonMatchingEntryWhoseFileNoLongerExists covers the new
// half of the behavior TestSyncMovesLeavesNonMatchingManifestUntouchedWhenFileStillExists
// guards the old half of: an entry that doesn't match the move is still
// dropped if the file it names is simply gone, since every command that
// touches a manifest file removes entries for files that no longer exist.
func TestSyncMovesPrunesNonMatchingEntryWhoseFileNoLongerExists(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	dest := filepath.Join(t.TempDir(), "photo.jpg")
	writeFile(t, src, "img")
	moveFile(t, src, dest)

	manifestPath := filepath.Join(dir, "manifest.json")
	writeFile(t, manifestPath, `{"site":"s","source_url":"u","file":"gone.jpg","status":"kept"}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("manifest is no longer valid JSON: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entry referencing a nonexistent file should have been pruned, got %+v", entries)
	}
}

func TestSyncMovesSkipsDirectoriesWithNoManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(destDir, "*manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no destination manifest when the source has none, found %v", matches)
	}
}

func TestSyncMovesCreatesDestinationManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed","width":10,"height":20}]`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	destManifest := filepath.Join(destDir, "image-manifest.json")
	var entries []Entry
	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatalf("destination manifest was not created: %v", err)
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("destination manifest is not a valid entry array: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	got := entries[0]
	if got.File != dest || got.Site != "s" || got.Width != 10 || got.Height != 20 {
		t.Fatalf("destination entry = %+v, want File=%q carrying over source metadata", got, dest)
	}
}

// TestSyncMovesCreatesDestinationManifestWithAbsoluteFileFromAbsoluteSource
// covers the other input convention: site-scraper's manifest.json always
// writes File as an absolute path (never a bare filename). The destination
// mirror should point File at the image's new absolute location here too —
// bare in or absolute in, the destination's copy is always absolute.
func TestSyncMovesCreatesDestinationManifestWithAbsoluteFileFromAbsoluteSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "manifest.json"),
		`{"entries":[{"site":"s","source_url":"u","file":"`+src+`","status":"kept","width":10,"height":20}]}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	destManifest := filepath.Join(destDir, "image-manifest.json")
	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatalf("destination manifest was not created: %v", err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("destination manifest is not a valid entry array: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	if entries[0].File != dest {
		t.Fatalf("destination entry File = %q, want absolute path %q", entries[0].File, dest)
	}
}

// TestSyncMovesReplacesExistingDestinationEntryAcrossFileConventions guards
// against a real failure: a destination manifest written for a file under
// one File convention (bare filename) must still be recognized and
// replaced, not duplicated, by a later sync for the same file that writes
// File under the other convention (absolute path) — e.g. a site-scraper
// entry re-synced after mirrorToDestinations previously ran with the old
// bare-filename-only behavior.
func TestSyncMovesReplacesExistingDestinationEntryAcrossFileConventions(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "manifest.json"),
		`{"entries":[{"site":"new","source_url":"u","file":"`+src+`","status":"kept"}]}`)

	destManifest := filepath.Join(destDir, "image-manifest.json")
	writeFile(t, destManifest, `[{"site":"old","source_url":"u-old","file":"photo.jpg","status":"kept"}]`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1 (replaced, not duplicated): %+v", len(entries), entries)
	}
	if entries[0].Site != "new" || entries[0].File != dest {
		t.Fatalf("entries[0] = %+v, want the new absolute entry to have replaced the old bare one", entries[0])
	}
}

func TestSyncMovesMergesIntoExistingDestinationArrayManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed"}]`)

	destManifest := filepath.Join(destDir, "image-manifest.json")
	writeFile(t, destManifest, `[{"site":"existing","source_url":"u3","file":"already-here.jpg","status":"processed"}]`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	var entries []Entry
	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("destination manifest is not a valid entry array: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2 (existing + merged)", len(entries))
	}

	byFile := make(map[string]Entry)
	for _, e := range entries {
		byFile[e.File] = e
	}
	if _, ok := byFile["already-here.jpg"]; !ok {
		t.Fatal("existing entry already-here.jpg was dropped")
	}
	if _, ok := byFile[dest]; !ok {
		t.Fatalf("moved entry was not merged in with absolute File %q: %+v", dest, entries)
	}
}

func TestSyncMovesConvertsDestinationObjectManifestToArrayWhenMerging(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed"}]`)

	destManifest := filepath.Join(destDir, "manifest.json")
	writeFile(t, destManifest, `{"site":"existing","source_url":"u3","file":"already-here.jpg","status":"kept"}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatal(err)
	}
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("expected manifest to become an array after merging a second entry: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(entries))
	}
}

func TestSyncMovesMergesIntoExistingDestinationEntriesWrapperManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed"}]`)

	destManifest := filepath.Join(destDir, "manifest.json")
	writeFile(t, destManifest, `{"entries":[{"site":"existing","source_url":"u3","file":"already-here.jpg","status":"kept"}]}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatal(err)
	}
	var w Wrapper
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("expected manifest to stay an entries wrapper after merging: %v", err)
	}
	if len(w.Entries) != 2 {
		t.Fatalf("len(entries) = %d, want 2", len(w.Entries))
	}

	byFile := make(map[string]Entry)
	for _, e := range w.Entries {
		byFile[e.File] = e
	}
	if _, ok := byFile["already-here.jpg"]; !ok {
		t.Fatal("existing entry already-here.jpg was dropped")
	}
	if _, ok := byFile[dest]; !ok {
		t.Fatalf("moved entry was not merged in with absolute File %q: %+v", dest, w.Entries)
	}
}

func TestSyncMovesReplacesMatchingDestinationEntryKeepingObjectShape(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"new","source_url":"u","file":"photo.jpg","status":"processed"}]`)

	destManifest := filepath.Join(destDir, "manifest.json")
	writeFile(t, destManifest, `{"site":"old","source_url":"u-old","file":"photo.jpg","status":"kept"}`)

	if err := SyncMoves(map[string]string{src: dest}); err != nil {
		t.Fatalf("SyncMoves returned error: %v", err)
	}

	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatal(err)
	}
	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("expected manifest to stay a single object when the merge replaces the only entry: %v", err)
	}
	if entry.Site != "new" {
		t.Fatalf("entry.Site = %q, want %q (replaced, not appended)", entry.Site, "new")
	}
}

func TestSyncMovesReportsAmbiguousDestinationManifest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.jpg")
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "photo.jpg")
	writeFile(t, src, "img")

	writeFile(t, filepath.Join(dir, "image-manifest.json"),
		`[{"site":"s","source_url":"u","file":"photo.jpg","status":"processed"}]`)

	writeFile(t, filepath.Join(destDir, "image-manifest.json"), `[]`)
	writeFile(t, filepath.Join(destDir, "manifest.json"), `{}`)

	if err := SyncMoves(map[string]string{src: dest}); err == nil {
		t.Fatal("SyncMoves() = nil, want an error for an ambiguous destination manifest")
	}
}
