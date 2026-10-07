package ingest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"image-browser/internal/db"
	"image-browser/internal/manifest"
	"image-browser/internal/store"
)

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

func writeManifest(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func recordByPath(t *testing.T, inv *store.Store, path string) (store.FileRecord, bool) {
	t.Helper()
	all, err := inv.AllFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.Path == path {
			return r, true
		}
	}
	return store.FileRecord{}, false
}

func TestRunImportsProcessedEntryWithProvenance(t *testing.T) {
	inv := newTestStore(t)
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(imagePath, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeManifest(t, filepath.Join(dir, "image-manifest.json"), `[{
		"site": "https://example.com/page",
		"source_url": "https://cdn.example.com/a.jpg",
		"file": "a.jpg",
		"hash": "abc123",
		"status": "processed",
		"original_file": "original.jpg",
		"discovered_at": "2026-08-28T12:34:56Z",
		"width": 100,
		"height": 200
	}]`)

	if err := Run([]string{"-root", dir}); err != nil {
		t.Fatal(err)
	}

	got, ok := recordByPath(t, inv, imagePath)
	if !ok {
		t.Fatal("expected a record for the imported entry")
	}
	if got.Site != "https://example.com/page" || got.SourceURL != "https://cdn.example.com/a.jpg" {
		t.Errorf("provenance not imported: %+v", got)
	}
	if got.Hash != "abc123" || got.OriginalFile != "original.jpg" || got.Status != "processed" {
		t.Errorf("fields not imported: %+v", got)
	}
	if !got.DiscoveredAt.Equal(time.Date(2026, 8, 28, 12, 34, 56, 0, time.UTC)) {
		t.Errorf("DiscoveredAt = %v, want 2026-08-28T12:34:56Z", got.DiscoveredAt)
	}
	if got.Width != 100 || got.Height != 200 {
		t.Errorf("dimensions not imported: %+v", got)
	}
}

func TestRunSkipsMissingEntryWithoutCreatingRecord(t *testing.T) {
	inv := newTestStore(t)
	dir := t.TempDir()

	writeManifest(t, filepath.Join(dir, "image-manifest.json"), `[{
		"site": "s", "source_url": "u", "file": "missing.jpg", "status": "processed"
	}]`)

	if err := Run([]string{"-root", dir}); err != nil {
		t.Fatal(err)
	}

	if _, ok := recordByPath(t, inv, filepath.Join(dir, "missing.jpg")); ok {
		t.Fatal("expected no record for a file that doesn't exist on disk")
	}
}

func TestRunSkipsInvalidEntryWithNoFile(t *testing.T) {
	newTestStore(t)
	dir := t.TempDir()

	writeManifest(t, filepath.Join(dir, "image-manifest.json"), `[{"site": "s", "source_url": "u", "file": ""}]`)

	if err := Run([]string{"-root", dir}); err != nil {
		t.Fatal(err)
	}
	// Nothing to assert beyond "did not crash" — an empty File has no path
	// to create a record for.
}

func TestRunSkipsOutOfTreeEntry(t *testing.T) {
	inv := newTestStore(t)
	dir := t.TempDir()
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "elsewhere.jpg")
	if err := os.WriteFile(outsidePath, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeManifest(t, filepath.Join(dir, "image-manifest.json"), `[{
		"site": "s", "source_url": "u", "file": "`+outsidePath+`", "status": "processed"
	}]`)

	if err := Run([]string{"-root", dir}); err != nil {
		t.Fatal(err)
	}

	if _, ok := recordByPath(t, inv, outsidePath); ok {
		t.Fatal("expected the out-of-tree entry to be skipped")
	}
}

func TestRunDoesNotClobberScanDerivedColumns(t *testing.T) {
	inv := newTestStore(t)
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "a.jpg")
	if err := os.WriteFile(imagePath, []byte("already-scanned-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate a prior `scan` run that already recorded this file with its
	// own filesize and hash.
	ctx := context.Background()
	if _, _, err := inv.AddFile(ctx, store.File{
		Path:      imagePath,
		Filename:  "a.jpg",
		Extension: ".jpg",
		Filesize:  12345,
		BLAKE3:    sql.NullString{String: "scan-computed-hash", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	writeManifest(t, filepath.Join(dir, "image-manifest.json"), `[{
		"site": "https://example.com", "source_url": "u", "file": "a.jpg", "status": "kept"
	}]`)

	if err := Run([]string{"-root", dir}); err != nil {
		t.Fatal(err)
	}

	got, ok := recordByPath(t, inv, imagePath)
	if !ok {
		t.Fatal("expected the record to still exist")
	}
	if got.Site != "https://example.com" || got.Status != "kept" {
		t.Fatalf("provenance not merged in: %+v", got)
	}
	if got.Hash != "scan-computed-hash" {
		t.Fatalf("Hash = %q, want scan's original hash preserved, not overwritten by import", got.Hash)
	}
}

func TestImportEntryClassifiesEachSkipReason(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.jpg")
	if err := os.WriteFile(existing, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	outsidePath := filepath.Join(outside, "elsewhere.jpg")
	if err := os.WriteFile(outsidePath, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv := newTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		entry manifest.Entry
		want  skipReason
	}{
		{"invalid", manifest.Entry{File: ""}, skipInvalid},
		{"outOfTree", manifest.Entry{File: outsidePath}, skipOutOfTree},
		{"missing", manifest.Entry{File: filepath.Join(dir, "missing.jpg")}, skipMissing},
		{"ok", manifest.Entry{File: existing}, skipNone},
	}
	for _, c := range cases {
		if got := importEntry(ctx, inv, c.entry, dir); got != c.want {
			t.Errorf("%s: importEntry() = %v, want %v", c.name, got, c.want)
		}
	}
}
