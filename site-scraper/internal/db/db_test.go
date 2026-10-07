package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestOpenFreshDatabaseHasTargetSchema verifies a brand new database ends
// up with every table and files column the target schema defines.
func TestOpenFreshDatabaseHasTargetSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inv.db")

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	cols, err := columns(database, "files")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hash", "origin_id", "status", "error", "last_verified_at", "missing_since"} {
		if !cols[want] {
			t.Errorf("files table missing column %q", want)
		}
	}
	if cols["blake3"] {
		t.Error("files table still has blake3 column")
	}

	for _, table := range []string{"images", "videos", "audio", "media_probe", "origins", "tags", "file_tags"} {
		var name string
		row := database.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

// TestOpenMigratesExistingBlake3Schema verifies that an existing database
// created under the original schema (files.blake3, none of the new
// columns) is migrated in place without losing its data.
func TestOpenMigratesExistingBlake3Schema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inv.db")

	seed, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = seed.Exec(`
		CREATE TABLE files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			extension TEXT,
			mime_type TEXT,
			filesize INTEGER NOT NULL,
			blake3 TEXT,
			created_at DATETIME,
			modified_at DATETIME,
			scanned_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO files (path, filename, filesize, blake3) VALUES ('/a/b.jpg', 'b.jpg', 123, 'deadbeef');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	var hash string
	var filesize int64
	row := database.QueryRow(`SELECT hash, filesize FROM files WHERE path = ?`, "/a/b.jpg")
	if err := row.Scan(&hash, &filesize); err != nil {
		t.Fatalf("row surviving migration: %v", err)
	}
	if hash != "deadbeef" || filesize != 123 {
		t.Errorf("migrated row = (hash=%s, filesize=%d), want (deadbeef, 123)", hash, filesize)
	}

	var status string
	if err := database.QueryRow(`SELECT status FROM files WHERE path = ?`, "/a/b.jpg").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "processed" {
		t.Errorf("status = %q, want %q", status, "processed")
	}

	// Open again to confirm the migration (and index creation) is
	// idempotent against a database that's already current.
	if _, err := Open(path); err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
}
