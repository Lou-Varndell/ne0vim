package ingest

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	invlib "inv-lib"

	"image-browser/internal/manifest"
)

func newTestStore(t *testing.T) *invlib.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	t.Setenv("INVENTORY_MANAGER_DB", path)
	database, err := invlib.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
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

// fileRow is a flattened view of a file's provenance/status/dimensions for
// test assertions, derived from invlib.FileRecord's nested, nullable
// fields.
type fileRow struct {
	Site, SourceURL, Hash, OriginalFile, Status string
	DiscoveredAt                                time.Time
	Width, Height                               int
}

func recordByPath(t *testing.T, inv *invlib.DB, path string) (fileRow, bool) {
	t.Helper()
	all, err := inv.ListFileRecords(context.Background(), invlib.FileQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.File.Path != path {
			continue
		}
		var row fileRow
		if r.File.Hash != nil {
			row.Hash = *r.File.Hash
		}
		if r.File.Status != nil {
			row.Status = *r.File.Status
		}
		if r.Origin != nil {
			if r.Origin.Site != nil {
				row.Site = *r.Origin.Site
			}
			if r.Origin.URL != nil {
				row.SourceURL = *r.Origin.URL
			}
			if r.Origin.OriginalFile != nil {
				row.OriginalFile = *r.Origin.OriginalFile
			}
			if r.Origin.DiscoveredAt != nil {
				row.DiscoveredAt = *r.Origin.DiscoveredAt
			}
		}
		if r.Image != nil {
			if r.Image.Width != nil {
				row.Width = *r.Image.Width
			}
			if r.Image.Height != nil {
				row.Height = *r.Image.Height
			}
		}
		return row, true
	}
	return fileRow{}, false
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
	if got.Hash != "abc123" || got.OriginalFile != "original.jpg" {
		t.Errorf("fields not imported: %+v", got)
	}
	// status is a `process` decode-outcome concern, not provenance — import
	// never touches it, regardless of what the manifest entry's own status
	// says.
	if got.Status != "" {
		t.Errorf("Status = %q, want empty (import must not set it)", got.Status)
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
	extension := ".jpg"
	hash := "scan-computed-hash"
	if err := inv.CreateFile(ctx, &invlib.File{
		Path:      imagePath,
		Filename:  "a.jpg",
		Extension: &extension,
		Filesize:  12345,
		Hash:      &hash,
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
	if got.Site != "https://example.com" {
		t.Fatalf("provenance not attached: %+v", got)
	}
	// The manifest entry's "kept" status is a scrape-outcome concept, not
	// the files.status process tracks — import must leave it alone.
	if got.Status != "" {
		t.Fatalf("Status = %q, want empty (import must not set it)", got.Status)
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
