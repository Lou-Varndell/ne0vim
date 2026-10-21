// Package store queries and updates the shared inventory database (see
// media-site/internal/db) on behalf of the catalog browser. It only ever
// touches the files and images tables, and only ever by absolute
// filesystem path — paths other tools (image-browser, site-scraper) have
// already indexed under the catalog's image directory show up here too,
// and files this package indexes are visible to them.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
)

// Store provides catalog access to the shared inventory database.
type Store struct {
	db *sql.DB
}

// New returns a Store backed by db.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// File is a physical file to record in the library, keyed by its absolute
// path.
type File struct {
	Path      string
	Filename  string
	Extension string
	MIMEType  string
	Filesize  int64
}

// Image is the image-specific extension of a File row. FileID must be the
// id of an existing files row.
type Image struct {
	FileID int64
	Width  int
	Height int
	Format string
}

// AddFile inserts a new physical file into the library, returning the
// row's id and whether it was newly inserted. If a file at the same path
// already exists — e.g. another tool indexed it first — no insert happens
// and AddFile returns that row's existing id with inserted=false, which is
// still valid for attaching an Image row to.
func (s *Store) AddFile(ctx context.Context, f File) (id int64, inserted bool, err error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO files (path, filename, extension, mime_type, filesize)
		VALUES (?, ?, ?, ?, ?)
	`, f.Path, f.Filename, f.Extension, f.MIMEType, f.Filesize)
	if err != nil {
		return 0, false, fmt.Errorf("store: add file: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("store: check rows affected: %w", err)
	}
	if affected == 0 {
		var existingID int64
		row := s.db.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, f.Path)
		if err := row.Scan(&existingID); err != nil {
			return 0, false, fmt.Errorf("store: look up existing file id: %w", err)
		}
		return existingID, false, nil
	}

	id, err = result.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("store: get file id: %w", err)
	}
	return id, true, nil
}

// AddImage inserts the image-specific extension row for an existing file,
// reporting whether it was newly inserted. If img.FileID already has an
// images row (e.g. a repeat scan), the insert is skipped rather than
// overwriting it.
func (s *Store) AddImage(ctx context.Context, img Image) (inserted bool, err error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO images (file_id, width, height, format)
		VALUES (?, ?, ?, ?)
	`, img.FileID, img.Width, img.Height, img.Format)
	if err != nil {
		return false, fmt.Errorf("store: add image: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: check rows affected: %w", err)
	}
	return affected > 0, nil
}

// PruneMissingUnderPrefix deletes every files row whose path starts with
// prefix but no longer exists on disk, reporting how many were removed.
// It is scoped to prefix (the catalog's image directory, with a trailing
// separator) rather than the whole shared table, so a media-site scan
// never deletes another tool's rows for paths outside its own tree.
func (s *Store) PruneMissingUnderPrefix(ctx context.Context, prefix string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path FROM files WHERE path LIKE ?`, prefix+"%")
	if err != nil {
		return 0, fmt.Errorf("store: list files for prune: %w", err)
	}

	type candidate struct {
		id   int64
		path string
	}
	var all []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.path); err != nil {
			rows.Close()
			return 0, fmt.Errorf("store: scan file for prune: %w", err)
		}
		all = append(all, c)
	}
	closeErr := rows.Err()
	rows.Close()
	if closeErr != nil {
		return 0, fmt.Errorf("store: list files for prune: %w", closeErr)
	}

	var removed int
	for _, c := range all {
		if _, err := os.Stat(c.path); errors.Is(err, os.ErrNotExist) {
			if _, err := s.db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, c.id); err != nil {
				return removed, fmt.Errorf("store: delete missing file %s: %w", c.path, err)
			}
			removed++
		}
	}
	return removed, nil
}

// ListSubdirectories returns the names of the immediate sub-directories
// under prefix (an absolute directory path ending in a separator) that
// contain at least one catalogued image anywhere beneath them, ordered by
// name.
func (s *Store) ListSubdirectories(ctx context.Context, prefix string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT substr(rel, 1, instr(rel, '/') - 1) AS name
		FROM (
			SELECT substr(f.path, ?) AS rel
			FROM files f
			JOIN images i ON i.file_id = f.id
			WHERE f.path LIKE ?
		)
		WHERE instr(rel, '/') > 0
		ORDER BY name
	`, len(prefix)+1, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("store: list subdirectories of %q: %w", prefix, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store: scan subdirectory: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ListImagePaths returns up to limit catalogued images directly in the
// directory at prefix (not in any sub-directory), ordered by path,
// starting after the first offset of them.
func (s *Store) ListImagePaths(ctx context.Context, prefix string, offset, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.path
		FROM files f
		JOIN images i ON i.file_id = f.id
		WHERE f.path LIKE ? AND substr(f.path, ?) NOT LIKE '%/%'
		ORDER BY f.path
		LIMIT ? OFFSET ?
	`, prefix+"%", len(prefix)+1, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list images in %q: %w", prefix, err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("store: scan image: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// DirectoryExists reports whether any catalogued image lives at or beneath
// prefix (an absolute directory path ending in a separator).
func (s *Store) DirectoryExists(ctx context.Context, prefix string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM files f JOIN images i ON i.file_id = f.id WHERE f.path LIKE ?
		)
	`, prefix+"%").Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: check directory %q: %w", prefix, err)
	}
	return exists, nil
}

// ImageExists reports whether path is a catalogued image.
func (s *Store) ImageExists(ctx context.Context, path string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM files f JOIN images i ON i.file_id = f.id WHERE f.path = ?
		)
	`, path).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("store: check image %q: %w", path, err)
	}
	return exists, nil
}
