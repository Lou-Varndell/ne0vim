package invlib

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

// OpenReadOnly connects to the SQLite database at path in query-only mode:
// SQLite itself rejects any statement that would write to the database
// file — INSERT, UPDATE, DELETE, PRAGMA writes, ATTACH, schema changes —
// regardless of what the caller's SQL text says, so a hostile or buggy
// query can't do more than read. No migration runs on a read-only
// connection, and none is attempted — the caller is expected to already
// know the database at path has been through a normal invlib.Open at some
// point. Open with the DSN built the same careful way (url.Values, not
// string concatenation) as Open itself, so a path containing '?'/'&'/'#'
// can't inject or alter DSN parameters.
//
// Intended for interfaces that accept arbitrary user-supplied SQL against
// the shared inventory database — see QueryFileIDs.
func OpenReadOnly(ctx context.Context, path string) (*DB, error) {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Set("_texttotime", "true")
	dsn := "file:" + path + "?" + q.Encode()

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("invlib: open read-only %s: %w", path, err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("invlib: ping read-only %s: %w", path, err)
	}

	return &DB{sqlDB}, nil
}

// QueryFileIDs runs rawSQL — expected to be a single SELECT statement
// supplied by a user, e.g. pasted into an "advanced search" box — and
// returns the distinct values of its "file_id" or "id" result column (
// matched case-insensitively against the column names rawSQL's SELECT
// actually returns), in first-seen order. Values are collapsed to their
// first occurrence so a query that joins and returns the same file more
// than once doesn't produce duplicate results downstream.
//
// QueryFileIDs itself does not enforce read-only-ness; pair it with a *DB
// obtained from OpenReadOnly so a write statement is rejected by SQLite
// rather than merely discouraged by convention here.
//
// An error is returned, with SQLite's own message intact, if rawSQL fails
// for any reason (syntax error, unknown table/column, a write rejected by
// a read-only connection, ...) or if its result has neither a "file_id"
// nor an "id" column — callers are expected to surface that text directly
// to whoever supplied rawSQL, since it's the only way they can tell what
// went wrong with a query inv-lib never otherwise interprets.
func (db *DB) QueryFileIDs(ctx context.Context, rawSQL string) ([]int64, error) {
	rows, err := db.QueryContext(ctx, rawSQL)
	if err != nil {
		return nil, fmt.Errorf("invlib: query file ids: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("invlib: query file ids: %w", err)
	}
	idCol := -1
	for i, c := range cols {
		if strings.EqualFold(c, "file_id") || strings.EqualFold(c, "id") {
			idCol = i
			break
		}
	}
	if idCol == -1 {
		return nil, fmt.Errorf("invlib: query file ids: result must include a file_id or id column, got %v", cols)
	}

	scanned := make([]any, len(cols))
	dest := make([]sql.NullInt64, len(cols))
	for i := range dest {
		scanned[i] = &dest[i]
	}

	seen := make(map[int64]bool)
	// ids starts non-nil (not var ids []int64) so a query that succeeds
	// with zero matching rows returns an empty-but-non-nil slice —
	// FileQuery.FileIDs treats that as "match nothing", exactly right for
	// a search that genuinely found nothing, as opposed to nil's "no ID
	// restriction at all" meaning.
	ids := []int64{}
	for rows.Next() {
		if err := rows.Scan(scanned...); err != nil {
			return nil, fmt.Errorf("invlib: query file ids: %w", err)
		}
		v := dest[idCol]
		if !v.Valid {
			continue
		}
		if !seen[v.Int64] {
			seen[v.Int64] = true
			ids = append(ids, v.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invlib: query file ids: %w", err)
	}

	return ids, nil
}
