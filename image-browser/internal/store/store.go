// Package store provides persistence for the media inventory database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"
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

	// Provenance columns, populated by `import` from a scraper's
	// manifest.json (see internal/ingest) and by `process`'s status/error
	// tracking. A plain `scan` leaves all of these unset.
	Site         sql.NullString
	SourceURL    sql.NullString
	OriginalFile sql.NullString
	DiscoveredAt sql.NullTime
	Status       sql.NullString
	Error        sql.NullString
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
			modified_at,
			site,
			source_url,
			original_file,
			discovered_at,
			status,
			error
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		f.Path,
		f.Filename,
		f.Extension,
		f.MIMEType,
		f.Filesize,
		f.BLAKE3,
		f.CreatedAt,
		f.ModifiedAt,
		f.Site,
		f.SourceURL,
		f.OriginalFile,
		f.DiscoveredAt,
		f.Status,
		f.Error,
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

// UpsertFile inserts a new files row, or — if path already has one — updates
// just that row's provenance columns (site, source_url, original_file,
// discovered_at, status, error) from f. Scan-derived columns (filesize,
// blake3, modified_at, ...) on an existing row are left untouched, so
// re-running an import never clobbers data a more authoritative scan or
// process run already recorded. It returns the row's id either way.
func (s *Store) UpsertFile(ctx context.Context, f File) (int64, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (
			path, filename, extension, mime_type, filesize, blake3,
			created_at, modified_at, site, source_url, original_file,
			discovered_at, status, error
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			site          = excluded.site,
			source_url    = excluded.source_url,
			original_file = excluded.original_file,
			discovered_at = excluded.discovered_at,
			status        = excluded.status,
			error         = excluded.error
	`,
		f.Path, f.Filename, f.Extension, f.MIMEType, f.Filesize, f.BLAKE3,
		f.CreatedAt, f.ModifiedAt, f.Site, f.SourceURL, f.OriginalFile,
		f.DiscoveredAt, f.Status, f.Error,
	)
	if err != nil {
		return 0, fmt.Errorf("store: upsert file: %w", err)
	}

	var id int64
	row := s.db.QueryRowContext(ctx, `SELECT id FROM files WHERE path = ?`, f.Path)
	if err := row.Scan(&id); err != nil {
		return 0, fmt.Errorf("store: look up upserted file id: %w", err)
	}
	return id, nil
}

// UpdatePath updates the path and filename of an existing files row — the
// entire effect a move has on the database, since there is no longer a
// per-directory manifest to find, merge, or prune.
func (s *Store) UpdatePath(ctx context.Context, id int64, path, filename string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE files SET path = ?, filename = ? WHERE id = ?`, path, filename, id); err != nil {
		return fmt.Errorf("store: update path: %w", err)
	}
	return nil
}

// SetFileStatus records the outcome of a process run against an existing
// files row. An empty errMsg stores a SQL NULL rather than an empty string.
func (s *Store) SetFileStatus(ctx context.Context, id int64, status, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE files SET status = ?, error = ? WHERE id = ?`,
		sql.NullString{String: status, Valid: status != ""},
		sql.NullString{String: errMsg, Valid: errMsg != ""},
		id,
	)
	if err != nil {
		return fmt.Errorf("store: set file status: %w", err)
	}
	return nil
}

// FileRecord is one files row joined with its images row (if any), the
// shape move's field filters match against.
type FileRecord struct {
	ID           int64
	Path         string
	Filename     string
	Site         string
	SourceURL    string
	OriginalFile string
	Hash         string
	Status       string
	Error        string
	DiscoveredAt time.Time
	Width        int
	Height       int
}

// AllFiles returns every files row joined with its images row (if any), for
// a caller (move) that filters the result set itself rather than pushing a
// dynamic WHERE clause into SQL.
func (s *Store) AllFiles(ctx context.Context) ([]FileRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.path, f.filename, f.site, f.source_url, f.original_file,
		       f.blake3, f.status, f.error, f.discovered_at, i.width, i.height
		FROM files f
		LEFT JOIN images i ON i.file_id = f.id
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list files: %w", err)
	}
	defer rows.Close()

	var records []FileRecord
	for rows.Next() {
		var (
			r                                                   FileRecord
			site, sourceURL, originalFile, hash, status, errMsg sql.NullString
			discoveredAt                                        sql.NullTime
			width, height                                       sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.Path, &r.Filename, &site, &sourceURL, &originalFile,
			&hash, &status, &errMsg, &discoveredAt, &width, &height); err != nil {
			return nil, fmt.Errorf("store: scan file: %w", err)
		}
		r.Site = site.String
		r.SourceURL = sourceURL.String
		r.OriginalFile = originalFile.String
		r.Hash = hash.String
		r.Status = status.String
		r.Error = errMsg.String
		r.DiscoveredAt = discoveredAt.Time
		r.Width = int(width.Int64)
		r.Height = int(height.Int64)
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list files: %w", err)
	}
	return records, nil
}

// ResolutionRow is one processed image's width, height, and path, as
// returned by GroupByResolution for size to bucket in Go.
type ResolutionRow struct {
	Width  int
	Height int
	Path   string
}

// GroupByResolution returns every processed image's width, height, and
// path, optionally restricted to paths beginning with pathPrefix (an empty
// pathPrefix returns every processed image in the database).
func (s *Store) GroupByResolution(ctx context.Context, pathPrefix string) ([]ResolutionRow, error) {
	query := `
		SELECT i.width, i.height, f.path
		FROM files f
		JOIN images i ON i.file_id = f.id
		WHERE i.width IS NOT NULL AND i.height IS NOT NULL
	`
	args := make([]any, 0, 1)
	if pathPrefix != "" {
		query += ` AND f.path LIKE ?`
		args = append(args, pathPrefix+"%")
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: group by resolution: %w", err)
	}
	defer rows.Close()

	var result []ResolutionRow
	for rows.Next() {
		var r ResolutionRow
		if err := rows.Scan(&r.Width, &r.Height, &r.Path); err != nil {
			return nil, fmt.Errorf("store: scan resolution row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: group by resolution: %w", err)
	}
	return result, nil
}

// UnprocessedImages returns every files row with no corresponding images
// row yet — a file scan recorded but never successfully decoded dimensions
// for. The caller (process) is responsible for filtering out any row whose
// extension isn't a recognized image type before attempting to decode it.
func (s *Store) UnprocessedImages(ctx context.Context) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.path, f.filename, f.extension, f.mime_type, f.filesize,
		       f.blake3, f.created_at, f.modified_at, f.scanned_at
		FROM files f
		LEFT JOIN images i ON i.file_id = f.id
		WHERE i.file_id IS NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("store: list unprocessed files: %w", err)
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.Path, &f.Filename, &f.Extension, &f.MIMEType, &f.Filesize,
			&f.BLAKE3, &f.CreatedAt, &f.ModifiedAt, &f.ScannedAt); err != nil {
			return nil, fmt.Errorf("store: scan unprocessed file: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list unprocessed files: %w", err)
	}
	return files, nil
}

// PruneMissing deletes every files row whose path no longer exists on disk
// (images/videos rows cascade via their foreign key), reporting how many
// were removed. Both move and size call this at startup to clear out
// records left behind by a file that was deleted, renamed, or moved
// outside this program's own tooling.
func (s *Store) PruneMissing(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path FROM files`)
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
