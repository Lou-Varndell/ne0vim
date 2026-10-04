// Package store provides persistence for the media library.
package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Store provides access to the inv-db SQLite database.
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

// execer is satisfied by both *sql.DB and *sql.Tx, letting addFile run
// either as a standalone statement or inside a caller-managed transaction.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// AddFile inserts a new physical file into the library. If a file at the
// same path already exists, the insert is skipped and AddFile returns
// (0, nil).
func (s *Store) AddFile(ctx context.Context, f File) (int64, error) {
	return addFile(ctx, s.db, f)
}

func addFile(ctx context.Context, ex execer, f File) (int64, error) {
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
		return 0, fmt.Errorf("store: add file: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: check rows affected: %w", err)
	}
	if affected == 0 {
		// Path already existed; the insert was ignored.
		return 0, nil
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get file id: %w", err)
	}

	return id, nil
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
		if _, err := addFile(ctx, tx, f); err != nil {
			return fmt.Errorf("store: add file %q: %w", f.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit transaction: %w", err)
	}
	return nil
}
