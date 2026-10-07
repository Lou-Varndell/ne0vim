package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"image-browser/internal/db"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return New(database)
}

func TestUpsertFileInsertsNewRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertFile(ctx, File{
		Path:      "/images/a.jpg",
		Filename:  "a.jpg",
		Extension: ".jpg",
		Filesize:  10,
		Site:      sql.NullString{String: "example.com", Valid: true},
		Status:    sql.NullString{String: "processed", Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected a non-zero id")
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("AllFiles() returned %d rows, want 1", len(all))
	}
	if all[0].Site != "example.com" || all[0].Status != "processed" {
		t.Fatalf("row = %+v, want site/status set", all[0])
	}
}

func TestUpsertFileOnConflictOnlyUpdatesProvenanceColumns(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, _, err := s.AddFile(ctx, File{
		Path:      "/images/a.jpg",
		Filename:  "a.jpg",
		Extension: ".jpg",
		Filesize:  999,
		BLAKE3:    sql.NullString{String: "original-hash", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpsertFile(ctx, File{
		Path:      "/images/a.jpg",
		Filename:  "a.jpg",
		Extension: ".jpg",
		Filesize:  111, // different; must NOT overwrite the existing row's filesize
		BLAKE3:    sql.NullString{String: "import-hash", Valid: true},
		Site:      sql.NullString{String: "example.com", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("AllFiles() returned %d rows, want 1", len(all))
	}
	got := all[0]
	if got.Site != "example.com" {
		t.Fatalf("Site = %q, want example.com (provenance should be updated)", got.Site)
	}
	if got.Hash != "original-hash" {
		t.Fatalf("Hash = %q, want original-hash (scan-derived column should not be overwritten)", got.Hash)
	}
}

func TestUpdatePath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertFile(ctx, File{Path: "/images/a.jpg", Filename: "a.jpg", Extension: ".jpg", Filesize: 1})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.UpdatePath(ctx, id, "/images/dest/a.jpg", "a.jpg"); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Path != "/images/dest/a.jpg" {
		t.Fatalf("AllFiles() = %+v, want path updated", all)
	}
}

func TestSetFileStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertFile(ctx, File{Path: "/images/a.jpg", Filename: "a.jpg", Extension: ".jpg", Filesize: 1})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetFileStatus(ctx, id, "failed", "decode error"); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if all[0].Status != "failed" || all[0].Error != "decode error" {
		t.Fatalf("row = %+v, want status=failed error='decode error'", all[0])
	}
}

func TestGroupByResolutionFiltersByPathPrefixAndRequiresDimensions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	addImage := func(path string, width, height int) {
		id, _, err := s.AddFile(ctx, File{Path: path, Filename: filepath.Base(path), Extension: ".jpg", Filesize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if width > 0 {
			if _, err := s.AddImage(ctx, Image{FileID: id, Width: width, Height: height}); err != nil {
				t.Fatal(err)
			}
		}
	}

	addImage("/images/a/one.jpg", 100, 200)
	addImage("/images/b/two.jpg", 300, 400)
	addImage("/images/a/unprocessed.jpg", 0, 0) // no images row at all

	rows, err := s.GroupByResolution(ctx, "/images/a")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Path != "/images/a/one.jpg" || rows[0].Width != 100 || rows[0].Height != 200 {
		t.Fatalf("GroupByResolution(prefix) = %+v, want just one.jpg at 100x200", rows)
	}

	all, err := s.GroupByResolution(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("GroupByResolution(\"\") returned %d rows, want 2 (unprocessed file excluded)", len(all))
	}
}

func TestUnprocessedImages(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	processedID, _, err := s.AddFile(ctx, File{Path: "/images/done.jpg", Filename: "done.jpg", Extension: ".jpg", Filesize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddImage(ctx, Image{FileID: processedID, Width: 1, Height: 1}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.AddFile(ctx, File{Path: "/images/todo.jpg", Filename: "todo.jpg", Extension: ".jpg", Filesize: 1}); err != nil {
		t.Fatal(err)
	}

	got, err := s.UnprocessedImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != "/images/todo.jpg" {
		t.Fatalf("UnprocessedImages() = %+v, want just todo.jpg", got)
	}
}

func TestPruneMissingDeletesRowsForDeletedFiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.jpg")
	if err := os.WriteFile(existing, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.jpg")

	if _, _, err := s.AddFile(ctx, File{Path: existing, Filename: "existing.jpg", Extension: ".jpg", Filesize: 3}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddFile(ctx, File{Path: missing, Filename: "missing.jpg", Extension: ".jpg", Filesize: 0}); err != nil {
		t.Fatal(err)
	}

	removed, err := s.PruneMissing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("PruneMissing() removed = %d, want 1", removed)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Path != existing {
		t.Fatalf("AllFiles() = %+v, want only existing.jpg left", all)
	}
}

func TestAllFilesJoinsImageDimensions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _, err := s.AddFile(ctx, File{Path: "/images/a.jpg", Filename: "a.jpg", Extension: ".jpg", Filesize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddImage(ctx, Image{FileID: id, Width: 50, Height: 60}); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Width != 50 || all[0].Height != 60 {
		t.Fatalf("AllFiles() = %+v, want width=50 height=60", all)
	}
}

func TestUpsertFilePreservesDiscoveredAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	discovered := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := s.UpsertFile(ctx, File{
		Path:         "/images/a.jpg",
		Filename:     "a.jpg",
		Extension:    ".jpg",
		Filesize:     1,
		DiscoveredAt: sql.NullTime{Time: discovered, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !all[0].DiscoveredAt.Equal(discovered) {
		t.Fatalf("DiscoveredAt = %v, want %v", all[0].DiscoveredAt, discovered)
	}
}
