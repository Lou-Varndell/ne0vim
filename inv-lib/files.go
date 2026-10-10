package invlib

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateFile inserts f and sets f.ID to the new row's id. f.ScannedAt is
// defaulted by SQLite to the current timestamp when left zero.
func (db *DB) CreateFile(ctx context.Context, f *File) error {
	res, err := db.ExecContext(ctx, `
		INSERT INTO files (
			path, filename, extension, mime_type, filesize, hash, origin_id,
			created_at, modified_at, last_verified_at, missing_since, status, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Path, f.Filename, f.Extension, f.MimeType, f.Filesize, f.Hash, f.OriginID,
		f.CreatedAt, f.ModifiedAt, f.LastVerifiedAt, f.MissingSince, f.Status, f.Error,
	)
	if err != nil {
		return fmt.Errorf("invlib: create file %s: %w", f.Path, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("invlib: create file %s: %w", f.Path, err)
	}
	f.ID = id
	return db.GetFileScannedAt(ctx, f)
}

// GetFileScannedAt refills f.ScannedAt from the database. It exists because
// scanned_at is set by a column default that CreateFile's caller cannot
// supply up front.
func (db *DB) GetFileScannedAt(ctx context.Context, f *File) error {
	return db.QueryRowContext(ctx, `SELECT scanned_at FROM files WHERE id = ?`, f.ID).Scan(&f.ScannedAt)
}

const fileColumns = `
	id, path, filename, extension, mime_type, filesize, hash, origin_id,
	created_at, modified_at, scanned_at, last_verified_at, missing_since, status, error`

// GetFile returns the file with the given id, or sql.ErrNoRows if none exists.
func (db *DB) GetFile(ctx context.Context, id int64) (*File, error) {
	row := db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files WHERE id = ?`, id)
	return scanFile(row)
}

// GetFileByPath returns the file at the given path, or sql.ErrNoRows if none exists.
func (db *DB) GetFileByPath(ctx context.Context, path string) (*File, error) {
	row := db.QueryRowContext(ctx, `SELECT `+fileColumns+` FROM files WHERE path = ?`, path)
	return scanFile(row)
}

// ListFilesByHash returns every file sharing the given content hash. hash is
// not a unique column, so this is how callers find duplicate content.
func (db *DB) ListFilesByHash(ctx context.Context, hash string) ([]*File, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+fileColumns+` FROM files WHERE hash = ? ORDER BY id`, hash)
	if err != nil {
		return nil, fmt.Errorf("invlib: list files by hash: %w", err)
	}
	defer rows.Close()
	return collectFiles(rows)
}

// ListFiles returns every file, ordered by id.
func (db *DB) ListFiles(ctx context.Context) ([]*File, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+fileColumns+` FROM files ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("invlib: list files: %w", err)
	}
	defer rows.Close()
	return collectFiles(rows)
}

// UpdateFile updates every column of the file matching f.ID except scanned_at.
func (db *DB) UpdateFile(ctx context.Context, f *File) error {
	_, err := db.ExecContext(ctx, `
		UPDATE files SET
			path = ?, filename = ?, extension = ?, mime_type = ?, filesize = ?,
			hash = ?, origin_id = ?, created_at = ?, modified_at = ?,
			last_verified_at = ?, missing_since = ?, status = ?, error = ?
		WHERE id = ?`,
		f.Path, f.Filename, f.Extension, f.MimeType, f.Filesize, f.Hash, f.OriginID,
		f.CreatedAt, f.ModifiedAt, f.LastVerifiedAt, f.MissingSince, f.Status, f.Error, f.ID,
	)
	if err != nil {
		return fmt.Errorf("invlib: update file %d: %w", f.ID, err)
	}
	return nil
}

// EnsureFile inserts f if no file exists at f.Path yet, or leaves the
// existing row untouched and sets f.ID to its id otherwise. It reports
// whether a new row was inserted. Callers that need the existing row's
// other columns on a cache hit should follow up with GetFile.
func (db *DB) EnsureFile(ctx context.Context, f *File) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO files (
			path, filename, extension, mime_type, filesize, hash, origin_id,
			created_at, modified_at, last_verified_at, missing_since, status, error
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (path) DO NOTHING`,
		f.Path, f.Filename, f.Extension, f.MimeType, f.Filesize, f.Hash, f.OriginID,
		f.CreatedAt, f.ModifiedAt, f.LastVerifiedAt, f.MissingSince, f.Status, f.Error,
	)
	if err != nil {
		return false, fmt.Errorf("invlib: ensure file %s: %w", f.Path, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("invlib: ensure file %s: %w", f.Path, err)
	}
	if affected > 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return false, fmt.Errorf("invlib: ensure file %s: %w", f.Path, err)
		}
		f.ID = id
		if err := db.GetFileScannedAt(ctx, f); err != nil {
			return false, fmt.Errorf("invlib: ensure file %s: %w", f.Path, err)
		}
		return true, nil
	}

	existing, err := db.GetFileByPath(ctx, f.Path)
	if err != nil {
		return false, fmt.Errorf("invlib: ensure file %s: %w", f.Path, err)
	}
	f.ID = existing.ID
	return false, nil
}

// UpdateFilePath updates only path and filename for the file matching id,
// leaving every other column untouched. Used when a file moves on disk.
func (db *DB) UpdateFilePath(ctx context.Context, id int64, path, filename string) error {
	_, err := db.ExecContext(ctx, `UPDATE files SET path = ?, filename = ? WHERE id = ?`, path, filename, id)
	if err != nil {
		return fmt.Errorf("invlib: update file path %d: %w", id, err)
	}
	return nil
}

// UpdateFileStatus updates only status and error for the file matching id,
// leaving every other column untouched. A nil status or errMsg stores SQL
// NULL. Used to record a local processing outcome, e.g. an image decode.
func (db *DB) UpdateFileStatus(ctx context.Context, id int64, status, errMsg *string) error {
	_, err := db.ExecContext(ctx, `UPDATE files SET status = ?, error = ? WHERE id = ?`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("invlib: update file status %d: %w", id, err)
	}
	return nil
}

// UpdateFileOrigin updates only origin_id for the file matching id, leaving
// every other column untouched. A nil originID clears the association.
func (db *DB) UpdateFileOrigin(ctx context.Context, id int64, originID *int64) error {
	_, err := db.ExecContext(ctx, `UPDATE files SET origin_id = ? WHERE id = ?`, originID, id)
	if err != nil {
		return fmt.Errorf("invlib: update file origin %d: %w", id, err)
	}
	return nil
}

// CreateFilesTx inserts files in one transaction, skipping (not erroring
// on) any whose path already exists, and rolling back entirely if any
// other error occurs. Each element's ID is set to its row's id — existing
// or newly inserted — on success. Unlike CreateFile, it does not backfill
// ScannedAt for newly inserted rows; callers needing it should follow up
// with GetFile.
func (db *DB) CreateFilesTx(ctx context.Context, files []*File) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("invlib: create files: begin: %w", err)
	}
	defer tx.Rollback()

	for _, f := range files {
		res, err := tx.ExecContext(ctx, `
			INSERT INTO files (
				path, filename, extension, mime_type, filesize, hash, origin_id,
				created_at, modified_at, last_verified_at, missing_since, status, error
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (path) DO NOTHING`,
			f.Path, f.Filename, f.Extension, f.MimeType, f.Filesize, f.Hash, f.OriginID,
			f.CreatedAt, f.ModifiedAt, f.LastVerifiedAt, f.MissingSince, f.Status, f.Error,
		)
		if err != nil {
			return fmt.Errorf("invlib: create files: insert %s: %w", f.Path, err)
		}

		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("invlib: create files: insert %s: %w", f.Path, err)
		}
		if affected > 0 {
			id, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("invlib: create files: insert %s: %w", f.Path, err)
			}
			f.ID = id
			continue
		}

		row := tx.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, f.Path)
		if err := row.Scan(&f.ID); err != nil {
			return fmt.Errorf("invlib: create files: lookup existing %s: %w", f.Path, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("invlib: create files: commit: %w", err)
	}
	return nil
}

// TouchScanned sets scanned_at to now for the given file id, the way a
// filesystem sync would after re-observing a file.
func (db *DB) TouchScanned(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `UPDATE files SET scanned_at = ? WHERE id = ?`, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("invlib: touch scanned %d: %w", id, err)
	}
	return nil
}

// DeleteFile deletes the file with the given id. images/videos/audio/
// media_probe/file_tags rows for it are removed by ON DELETE CASCADE.
func (db *DB) DeleteFile(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("invlib: delete file %d: %w", id, err)
	}
	return nil
}

func scanFile(row rowScanner) (*File, error) {
	var f File
	err := row.Scan(
		&f.ID, &f.Path, &f.Filename, &f.Extension, &f.MimeType, &f.Filesize, &f.Hash, &f.OriginID,
		&f.CreatedAt, &f.ModifiedAt, &f.ScannedAt, &f.LastVerifiedAt, &f.MissingSince, &f.Status, &f.Error,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: scan file: %w", err)
	}
	return &f, nil
}

func collectFiles(rows *sql.Rows) ([]*File, error) {
	var out []*File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
