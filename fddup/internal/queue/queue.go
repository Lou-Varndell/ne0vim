// Package queue is the persistence layer for fddup's scan-then-delete model.
//
// A scan never modifies files. It writes pending deletions into a SQLite
// database. A subsequent `delete` invocation reads from that queue, attempts
// each removal, and writes the per-file outcome back. This separation makes
// scan idempotent and delete resumable: a Ctrl-C or crash mid-delete leaves
// the queue in a state the next call can pick up from.
//
// Statuses:
//
//	pending  — queued by scan, not yet processed
//	deleted  — os.Remove succeeded
//	missing  — file already gone at delete time (from another process)
//	failed   — os.Remove returned an error (permission, etc.)
package queue

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, no CGo required.
)

// DB wraps *sql.DB so callers can only invoke published queue operations.
// This narrows the API surface and prevents accidental Exec(...) at call sites.
type DB struct {
	sql *sql.DB
}

// Open creates the database file (and parent directory) if missing, applies
// the schema, and returns a ready-to-use queue.
//
// SQLite pragmas:
//
//	journal_mode=WAL    — concurrent readers don't block each other
//	busy_timeout=5000   — 5s before SQLITE_BUSY when contended
//
// MaxOpenConns(1) serializes writes; with the modernc driver, multiple
// writers can occasionally hit "database is locked" even with WAL enabled.
// fddup's hot path is read-heavy in scan, so a single writer is not a bottleneck.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	schema := `
		CREATE TABLE IF NOT EXISTS scans (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			completed_at DATETIME,
			roots        TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS duplicates (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_id      INTEGER NOT NULL REFERENCES scans(id),
			hash         TEXT NOT NULL,
			size         INTEGER NOT NULL,
			path         TEXT NOT NULL,
			keeper       TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'pending',
			error        TEXT,
			processed_at DATETIME,
			UNIQUE(path)
		);
		CREATE INDEX IF NOT EXISTS idx_dup_status ON duplicates(status);
		CREATE INDEX IF NOT EXISTS idx_dup_hash   ON duplicates(hash);
		CREATE INDEX IF NOT EXISTS idx_dup_scan   ON duplicates(scan_id);

		CREATE TABLE IF NOT EXISTS hash_cache (
			path       TEXT    NOT NULL PRIMARY KEY,
			size       INTEGER NOT NULL,
			mtime_ns   INTEGER NOT NULL,
			head_hash  TEXT,
			full_hash  TEXT
		);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	// The queue drives unattended file deletion, so restrict it to the
	// owner — a world-writable DB would let any local user queue arbitrary
	// paths for the next `delete` run.
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, fmt.Errorf("set db permissions: %w", err)
	}
	return &DB{sql: db}, nil
}

// Close releases the underlying database handle.
func (q *DB) Close() error { return q.sql.Close() }

// StartScan opens a new scan row and returns its id. Each duplicate queued
// during this run references this id so multiple scans can coexist in one DB.
func (q *DB) StartScan(roots string) (int64, error) {
	res, err := q.sql.Exec(`INSERT INTO scans (roots) VALUES (?)`, roots)
	if err != nil {
		return 0, fmt.Errorf("start scan: %w", err)
	}
	return res.LastInsertId()
}

// CompleteScan stamps the scan as finished. Useful for reporting "what
// was the last completed scan?" if that ever becomes a feature.
func (q *DB) CompleteScan(scanID int64) error {
	_, err := q.sql.Exec(`UPDATE scans SET completed_at = CURRENT_TIMESTAMP WHERE id = ?`, scanID)
	if err != nil {
		return fmt.Errorf("complete scan: %w", err)
	}
	return nil
}

// QueueDuplicates inserts a batch of pending deletions in one transaction.
// ON CONFLICT DO NOTHING means re-scanning the same tree won't double-queue
// a path that's already pending from an earlier scan.
func (q *DB) QueueDuplicates(scanID int64, hash string, size int64, keeper string, dupes []string) error {
	tx, err := q.sql.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO duplicates (scan_id, hash, size, path, keeper)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO NOTHING
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, p := range dupes {
		if _, err := stmt.Exec(scanID, hash, size, p, keeper); err != nil {
			tx.Rollback()
			return fmt.Errorf("queue %s: %w", p, err)
		}
	}
	return tx.Commit()
}

// Pending is one row of the deletion queue.
type Pending struct {
	ID     int64
	Hash   string
	Size   int64
	Path   string
	Keeper string
}

// ListPending returns all rows still marked pending, oldest first. No
// pagination — millions of rows is fine and the caller usually wants the
// whole list to display or process.
func (q *DB) ListPending() ([]Pending, error) {
	rows, err := q.sql.Query(`
		SELECT id, hash, size, path, keeper
		FROM duplicates
		WHERE status = 'pending'
		ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("list pending: %w", err)
	}
	defer rows.Close()

	var out []Pending
	for rows.Next() {
		var p Pending
		if err := rows.Scan(&p.ID, &p.Hash, &p.Size, &p.Path, &p.Keeper); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Possible values for MarkProcessed's status argument.
const (
	StatusDeleted = "deleted"
	StatusMissing = "missing"
	StatusFailed  = "failed"
)

// MarkProcessed records the per-row outcome of a delete attempt. errMsg
// may be empty (it's stored as NULL when blank).
func (q *DB) MarkProcessed(id int64, status, errMsg string) error {
	_, err := q.sql.Exec(`
		UPDATE duplicates
		SET status = ?, error = NULLIF(?, ''), processed_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("mark processed: %w", err)
	}
	return nil
}

// Stats returns counts grouped by status, plus the total bytes of pending
// rows specifically (since that's the headline number fddup status shows).
func (q *DB) Stats() (counts map[string]int64, pendingBytes int64, err error) {
	rows, err := q.sql.Query(`
		SELECT status, COUNT(*), COALESCE(SUM(size), 0)
		FROM duplicates
		GROUP BY status
	`)
	if err != nil {
		return nil, 0, fmt.Errorf("query stats: %w", err)
	}
	defer rows.Close()

	counts = make(map[string]int64)
	for rows.Next() {
		var status string
		var count, bytes int64
		if err := rows.Scan(&status, &count, &bytes); err != nil {
			return nil, 0, fmt.Errorf("scan stats: %w", err)
		}
		counts[status] = count
		if status == "pending" {
			pendingBytes = bytes
		}
	}
	return counts, pendingBytes, rows.Err()
}

// Clear removes every row from the duplicates table. Used by `fddup reset`.
// Files on disk are unaffected.
func (q *DB) Clear() error {
	_, err := q.sql.Exec(`DELETE FROM duplicates`)
	return err
}

// CachedHashes holds previously computed hashes for a file. Either field may
// be zero if that stage hasn't been cached yet.
type CachedHashes struct {
	HeadHash [32]byte
	FullHash [32]byte
	HasHead  bool
	HasFull  bool
}

// GetCachedHash returns cached hashes for path if size and mtime_ns still
// match. Returns nil (no error) on a miss — caller must compute and store.
func (q *DB) GetCachedHash(path string, size, mtimeNs int64) (*CachedHashes, error) {
	var dbSize, dbMtime int64
	var headHex, fullHex sql.NullString

	err := q.sql.QueryRow(`
		SELECT size, mtime_ns, head_hash, full_hash
		FROM hash_cache WHERE path = ?
	`, path).Scan(&dbSize, &dbMtime, &headHex, &fullHex)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cache lookup %s: %w", path, err)
	}
	if dbSize != size || dbMtime != mtimeNs {
		return nil, nil // stale — file changed
	}

	c := &CachedHashes{}
	if headHex.Valid {
		b, err := hex.DecodeString(headHex.String)
		if err == nil && len(b) == 32 {
			copy(c.HeadHash[:], b)
			c.HasHead = true
		}
	}
	if fullHex.Valid {
		b, err := hex.DecodeString(fullHex.String)
		if err == nil && len(b) == 32 {
			copy(c.FullHash[:], b)
			c.HasFull = true
		}
	}
	return c, nil
}

// SetCachedHash upserts the hash cache for path. Pass a nil pointer for any
// hash that should not be updated yet (e.g. only head is known so far).
func (q *DB) SetCachedHash(path string, size, mtimeNs int64, headHash, fullHash *[32]byte) error {
	var headHex, fullHex sql.NullString
	if headHash != nil {
		headHex = sql.NullString{String: hex.EncodeToString(headHash[:]), Valid: true}
	}
	if fullHash != nil {
		fullHex = sql.NullString{String: hex.EncodeToString(fullHash[:]), Valid: true}
	}

	_, err := q.sql.Exec(`
		INSERT INTO hash_cache (path, size, mtime_ns, head_hash, full_hash)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			size      = excluded.size,
			mtime_ns  = excluded.mtime_ns,
			head_hash = CASE WHEN excluded.head_hash IS NOT NULL THEN excluded.head_hash ELSE hash_cache.head_hash END,
			full_hash = CASE WHEN excluded.full_hash IS NOT NULL THEN excluded.full_hash ELSE hash_cache.full_hash END
	`, path, size, mtimeNs, headHex, fullHex)
	if err != nil {
		return fmt.Errorf("cache store %s: %w", path, err)
	}
	return nil
}

// PruneCache removes hash_cache entries whose paths no longer exist on disk.
// Returns the number of rows removed.
func (q *DB) PruneCache() (int64, error) {
	rows, err := q.sql.Query(`SELECT path FROM hash_cache`)
	if err != nil {
		return 0, fmt.Errorf("prune query: %w", err)
	}
	defer rows.Close()

	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return 0, fmt.Errorf("prune scan: %w", err)
		}
		if _, statErr := os.Lstat(p); os.IsNotExist(statErr) {
			stale = append(stale, p)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(stale) == 0 {
		return 0, nil
	}

	tx, err := q.sql.Begin()
	if err != nil {
		return 0, fmt.Errorf("prune tx: %w", err)
	}
	stmt, err := tx.Prepare(`DELETE FROM hash_cache WHERE path = ?`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("prune prepare: %w", err)
	}
	defer stmt.Close()

	for _, p := range stale {
		if _, err := stmt.Exec(p); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("prune delete %s: %w", p, err)
		}
	}
	return int64(len(stale)), tx.Commit()
}
