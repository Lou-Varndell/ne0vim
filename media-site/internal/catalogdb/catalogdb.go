// Package catalogdb persists the image catalog's directory and file index
// in SQLite, so catalog page handlers can answer "what's in this directory"
// with a lookup instead of a filesystem walk on every request.
package catalogdb

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// DB wraps a SQLite connection holding the catalog index: one row per
// directory and one row per catalogued image, both keyed by their path
// relative to the configured image root ("" denotes the root itself).
type DB struct {
	conn *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS directories (
	path   TEXT PRIMARY KEY,
	name   TEXT NOT NULL,
	parent TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_directories_parent ON directories(parent);

CREATE TABLE IF NOT EXISTS images (
	path TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	dir  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_images_dir ON images(dir);
`

// Open creates (or reuses) the SQLite database file at path and ensures the
// catalog schema exists.
func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &DB{conn: conn}, nil
}

// Close closes the underlying database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}

// Reset deletes every row, leaving the schema in place. Call it before a
// fresh filesystem scan so stale entries from a previous run don't linger.
func (db *DB) Reset() error {
	if _, err := db.conn.Exec(`DELETE FROM directories`); err != nil {
		return fmt.Errorf("clear directories: %w", err)
	}
	if _, err := db.conn.Exec(`DELETE FROM images`); err != nil {
		return fmt.Errorf("clear images: %w", err)
	}
	return nil
}

// InsertDirectory records a directory at relPath (relative to the catalog
// root) with the given display name and parent directory path.
func (db *DB) InsertDirectory(relPath, name, parent string) error {
	_, err := db.conn.Exec(
		`INSERT INTO directories (path, name, parent) VALUES (?, ?, ?)`,
		relPath, name, parent,
	)
	return err
}

// InsertImage records an image at relPath (relative to the catalog root)
// with the given display name, living in directory dir.
func (db *DB) InsertImage(relPath, name, dir string) error {
	_, err := db.conn.Exec(
		`INSERT INTO images (path, name, dir) VALUES (?, ?, ?)`,
		relPath, name, dir,
	)
	return err
}

// Directory is one indexed sub-directory, relative to the catalog root.
type Directory struct {
	Name string
	Path string
}

// Image is one indexed image file, relative to the catalog root.
type Image struct {
	Name string
	Path string
}

// ListSubdirectories returns the immediate sub-directories of parent
// (relative to the catalog root, "" meaning the root itself), ordered by
// name.
func (db *DB) ListSubdirectories(parent string) ([]Directory, error) {
	rows, err := db.conn.Query(
		`SELECT path, name FROM directories WHERE parent = ? ORDER BY name`,
		parent,
	)
	if err != nil {
		return nil, fmt.Errorf("query subdirectories of %q: %w", parent, err)
	}
	defer rows.Close()

	var dirs []Directory
	for rows.Next() {
		var d Directory
		if err := rows.Scan(&d.Path, &d.Name); err != nil {
			return nil, fmt.Errorf("scan directory row: %w", err)
		}
		dirs = append(dirs, d)
	}
	return dirs, rows.Err()
}

// ListImages returns up to limit images catalogued directly in dir
// (relative to the catalog root), ordered by name, starting after the
// first offset of them. Pass limit one higher than the page size you
// actually want to render, so the caller can tell whether more images
// remain without a separate COUNT query.
func (db *DB) ListImages(dir string, offset, limit int) ([]Image, error) {
	rows, err := db.conn.Query(
		`SELECT path, name FROM images WHERE dir = ? ORDER BY name, path LIMIT ? OFFSET ?`,
		dir, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("query images in %q: %w", dir, err)
	}
	defer rows.Close()

	var images []Image
	for rows.Next() {
		var img Image
		if err := rows.Scan(&img.Path, &img.Name); err != nil {
			return nil, fmt.Errorf("scan image row: %w", err)
		}
		images = append(images, img)
	}
	return images, rows.Err()
}

// DirectoryExists reports whether relPath is a catalogued directory. The
// root itself ("") always exists.
func (db *DB) DirectoryExists(relPath string) (bool, error) {
	if relPath == "" {
		return true, nil
	}

	var exists bool
	err := db.conn.QueryRow(
		`SELECT EXISTS(SELECT 1 FROM directories WHERE path = ?)`,
		relPath,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check directory %q: %w", relPath, err)
	}
	return exists, nil
}

// GetImage looks up a catalogued image by its relative path. ok is false if
// no image is indexed at that path.
func (db *DB) GetImage(relPath string) (img Image, ok bool, err error) {
	row := db.conn.QueryRow(
		`SELECT path, name FROM images WHERE path = ?`,
		relPath,
	)
	if scanErr := row.Scan(&img.Path, &img.Name); scanErr != nil {
		if errors.Is(scanErr, sql.ErrNoRows) {
			return Image{}, false, nil
		}
		return Image{}, false, fmt.Errorf("get image %q: %w", relPath, scanErr)
	}
	return img, true, nil
}
