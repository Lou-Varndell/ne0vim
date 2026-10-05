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

	return db, nil
}
