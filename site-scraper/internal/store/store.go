// Package store provides persistence for the media inventory database.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Store provides access to the inventory SQLite database.
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

// File represents a physical file in the media library.
type File struct {
	ID         int64
	Path       string
	Filename   string
	Extension  string
	MIMEType   string
	Filesize   int64
	BLAKE3     sql.NullString
	CreatedAt  sql.NullTime
	ModifiedAt sql.NullTime
	ScannedAt  sql.NullTime
}

// Image represents the image-specific extension of a File row. FileID must
// be the id of an existing files row.
type Image struct {
	FileID     int64
	Width      int
	Height     int
	Format     string
	ColorSpace string
}

// execer is satisfied by both *sql.DB and *sql.Tx, letting addFile and
// addImage run either as standalone statements or inside a caller-managed
// transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// AddFile inserts a new physical file into the library, returning the row's
// id and whether it was newly inserted. If a file at the same path already
// exists, no insert happens and AddFile returns that row's existing id with
// inserted=false — the id is still valid and usable, e.g. to attach an
// Image row to it.
func (s *Store) AddFile(ctx context.Context, f File) (id int64, inserted bool, err error) {
	return addFile(ctx, s.db, f)
}

func addFile(ctx context.Context, ex execer, f File) (int64, bool, error) {
	result, err := ex.ExecContext(ctx, `
		INSERT OR IGNORE INTO files (
			path,
			filename,
			extension,
			mime_type,
			filesize,
			blake3,
			created_at,
			modified_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		f.Path,
		f.Filename,
		f.Extension,
		f.MIMEType,
		f.Filesize,
		f.BLAKE3,
		f.CreatedAt,
		f.ModifiedAt,
	)
	if err != nil {
		return 0, false, fmt.Errorf("store: add file: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("store: check rows affected: %w", err)
	}
	if affected == 0 {
		// Path already existed; the insert was ignored, so look up the id
		// it was ignored in favor of.
		var id int64
		row := ex.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, f.Path)
		if err := row.Scan(&id); err != nil {
			return 0, false, fmt.Errorf("store: look up existing file id: %w", err)
		}
		return id, false, nil
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, false, fmt.Errorf("store: get file id: %w", err)
	}

	return id, true, nil
}

// AddImage inserts the image-specific extension row for an existing file,
// reporting whether it was newly inserted. If img.FileID already has an
// images row (e.g. a repeat scan), the insert is skipped rather than
// overwriting it, and AddImage returns inserted=false.
func (s *Store) AddImage(ctx context.Context, img Image) (inserted bool, err error) {
	return addImage(ctx, s.db, img)
}

func addImage(ctx context.Context, ex execer, img Image) (bool, error) {
	result, err := ex.ExecContext(ctx, `
		INSERT OR IGNORE INTO images (
			file_id,
			width,
			height,
			format,
			color_space
		)
		VALUES (?, ?, ?, ?, ?)
	`,
		img.FileID,
		img.Width,
		img.Height,
		img.Format,
		img.ColorSpace,
	)
	if err != nil {
		return false, fmt.Errorf("store: add image: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: check rows affected: %w", err)
	}
	return affected > 0, nil
}

// AddFiles inserts files in a single transaction, rolling back entirely if
// any insert fails. Files whose path already exists are skipped rather
// than treated as errors.
func (s *Store) AddFiles(ctx context.Context, files []File) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin transaction: %w", err)
	}
	defer tx.Rollback()

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, _, err := addFile(ctx, tx, f); err != nil {
			return fmt.Errorf("store: add file %q: %w", f.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}
