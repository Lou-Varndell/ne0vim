// Package inventory persists the image inventory — the set of scanned
// files, their content hashes, and basic metadata — in a local SQLite
// database. It is the first piece of what will grow into the application's
// broader inventory management and database features.
package inventory

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Record is one indexed image file.
type Record struct {
	Path      string
	Hash      string
	Size      int64
	Width     int
	Height    int
	ScannedAt time.Time
}

// Store persists the image inventory in a SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and ensures
// its schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open inventory database %q: %w", path, err)
	}

	// SQLite only supports one writer at a time; serializing connections
	// avoids SQLITE_BUSY errors from the concurrent indexing goroutines
	// Browser.indexAsync spawns.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to inventory database %q: %w", path, err)
	}

	const schema = `
CREATE TABLE IF NOT EXISTS images (
	path       TEXT PRIMARY KEY,
	hash       TEXT NOT NULL,
	size       INTEGER NOT NULL,
	width      INTEGER NOT NULL,
	height     INTEGER NOT NULL,
	scanned_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS images_hash_idx ON images(hash);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create inventory schema: %w", err)
	}

	return &Store{db: db}, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Upsert records (or updates) a single image's inventory entry, keyed by
// path.
func (s *Store) Upsert(ctx context.Context, r Record) error {
	const stmt = `
INSERT INTO images (path, hash, size, width, height, scanned_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	hash = excluded.hash,
	size = excluded.size,
	width = excluded.width,
	height = excluded.height,
	scanned_at = excluded.scanned_at;
`
	_, err := s.db.ExecContext(ctx, stmt, r.Path, r.Hash, r.Size, r.Width, r.Height, r.ScannedAt.Unix())
	return err
}

// Count returns the number of images currently recorded.
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM images`).Scan(&n)
	return n, err
}

// DuplicatesOf returns the paths that share hash with path, excluding path
// itself — the content-based duplicate detection the rest of the inventory
// workflow will build on.
func (s *Store) DuplicatesOf(ctx context.Context, path, hash string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM images WHERE hash = ? AND path != ?`, hash, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}
