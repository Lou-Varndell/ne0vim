// Package db opens the SQLite inventory database and applies its schema.
package db

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// DefaultPath returns the shared inventory database path used by every
// image-browser (and site-scraper) command that doesn't need a directory-
// scoped inv.db of its own: ~/.config/inventory-manager/db/inv.db. The
// directory is created if it doesn't already exist.
//
// Tests that exercise a command end-to-end should not write into that real,
// shared file — set $INVENTORY_MANAGER_DB (e.g. to a path under t.TempDir())
// to override it instead; DefaultPath creates that path's directory the
// same way it does for the real default.
func DefaultPath() (string, error) {
	path := os.Getenv("INVENTORY_MANAGER_DB")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		path = filepath.Join(home, ".config", "inventory-manager", "db", "inv.db")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create db dir: %w", err)
	}

	return path, nil
}

// Open opens (creating if necessary) the SQLite database at path and
// applies the embedded schema.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}

	if err := migrateFilesColumns(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	return db, nil
}

// filesColumns lists every column schema.sql's CREATE TABLE IF NOT EXISTS
// for files declares beyond what the very first version of this database
// had. CREATE TABLE IF NOT EXISTS only applies the current column set to a
// brand-new database; a database created before one of these columns
// existed needs it added by hand, which migrateFilesColumns does.
var filesColumns = []struct {
	name, decl string
}{
	{"site", "TEXT"},
	{"source_url", "TEXT"},
	{"original_file", "TEXT"},
	{"discovered_at", "DATETIME"},
	{"status", "TEXT"},
	{"error", "TEXT"},
}

// migrateFilesColumns adds any column in filesColumns missing from an
// already-existing files table. It is idempotent and safe to run on every
// Open.
func migrateFilesColumns(db *sql.DB) error {
	existing := make(map[string]bool)

	rows, err := db.Query(`PRAGMA table_info(files)`)
	if err != nil {
		return fmt.Errorf("read files schema: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			return fmt.Errorf("scan files schema: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read files schema: %w", err)
	}

	for _, col := range filesColumns {
		if existing[col.name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf(`ALTER TABLE files ADD COLUMN %s %s`, col.name, col.decl)); err != nil {
			return fmt.Errorf("add column %s: %w", col.name, err)
		}
	}

	return nil
}
