// Package store provides persistence for the media library.
package store

import (
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
	BLAKE3     string
	CreatedAt  sql.NullTime
	ModifiedAt sql.NullTime
	ScannedAt  sql.NullTime
}

// AddFile inserts a new physical file into the library.
func (s *Store) AddFile(f File) (int64, error) {
	result, err := s.db.Exec(`
		INSERT INTO files (
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

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: get file id: %w", err)
	}

	return id, nil
}
