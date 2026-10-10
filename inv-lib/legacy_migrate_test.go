package invlib

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestOpenRepairsLegacyColumns hand-creates a files/origins table shaped
// like a pre-inv-lib site-scraper/image-browser database (blake3 instead of
// hash, no origin_id/status/error, a flat site/source_url/original_file/
// discovered_at shape on files, no original_file/discovered_at on origins)
// and asserts that Open repairs it into the current schema.
func TestOpenRepairsLegacyColumns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE origins (
			id INTEGER PRIMARY KEY,
			type TEXT NOT NULL,
			site TEXT,
			url TEXT,
			identifier TEXT,
			metadata TEXT,
			UNIQUE (site, identifier)
		);
		CREATE TABLE files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			extension TEXT,
			mime_type TEXT,
			filesize INTEGER NOT NULL,
			blake3 TEXT,
			site TEXT,
			source_url TEXT,
			original_file TEXT,
			discovered_at DATETIME,
			created_at DATETIME,
			modified_at DATETIME,
			scanned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO files (path, filename, filesize, blake3, site)
		VALUES ('/photos/legacy.jpg', 'legacy.jpg', 42, 'cafebabe', 'example.com');
	`); err != nil {
		t.Fatalf("create legacy schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw db: %v", err)
	}

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	f, err := db.GetFileByPath(ctx, "/photos/legacy.jpg")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f.Hash == nil || *f.Hash != "cafebabe" {
		t.Fatalf("expected blake3 backfilled into hash, got %+v", f)
	}

	cols, err := tableColumns(ctx, db, "files")
	if err != nil {
		t.Fatalf("tableColumns(files): %v", err)
	}
	for _, legacy := range []string{"blake3", "site", "source_url", "original_file", "discovered_at"} {
		if cols[legacy] {
			t.Errorf("expected legacy column %q to be dropped from files", legacy)
		}
	}
	for _, want := range []string{"hash", "origin_id", "last_verified_at", "missing_since", "status", "error"} {
		if !cols[want] {
			t.Errorf("expected column %q to exist on files after migration", want)
		}
	}

	originCols, err := tableColumns(ctx, db, "origins")
	if err != nil {
		t.Fatalf("tableColumns(origins): %v", err)
	}
	for _, want := range []string{"original_file", "discovered_at"} {
		if !originCols[want] {
			t.Errorf("expected column %q to exist on origins after migration", want)
		}
	}

	// Re-opening the now-migrated database must still be a no-op.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	db2.Close()
}

// TestOpenAppliesIndexes guards against indexes.sql silently never being
// executed.
func TestOpenAppliesIndexes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	want := []string{
		"idx_files_hash",
		"idx_files_mime_type",
		"idx_files_origin_id",
		"idx_files_status",
		"idx_files_path_sortkey",
	}
	for _, name := range want {
		var got string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&got)
		if err != nil {
			t.Errorf("index %q not found: %v", name, err)
		}
	}
}
