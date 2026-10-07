package db

import (
	"database/sql"
	"fmt"
)

// migrateFiles brings an existing files table up to the current column set.
// schema.sql's CREATE TABLE IF NOT EXISTS only applies to a brand new
// database — for one created before this column set existed (notably, one
// with the original `blake3` column instead of `hash`, and none of
// origin_id/status/error/last_verified_at/missing_since), this performs the
// equivalent ALTERs. It is always safe to call: every step first checks
// whether it has already been applied.
func migrateFiles(database *sql.DB) error {
	cols, err := columns(database, "files")
	if err != nil {
		return fmt.Errorf("inspect files columns: %w", err)
	}

	if cols["blake3"] && !cols["hash"] {
		if _, err := database.Exec(`ALTER TABLE files RENAME COLUMN blake3 TO hash`); err != nil {
			return fmt.Errorf("rename blake3 to hash: %w", err)
		}
		cols["hash"] = true
		delete(cols, "blake3")
	}

	// Order matters only for readability here: ADD COLUMN with a constant
	// default is independent of other columns, so these can run in any
	// order or be skipped individually depending on what's already present.
	additions := []struct {
		name string
		ddl  string
	}{
		{"origin_id", "INTEGER REFERENCES origins(id)"},
		{"last_verified_at", "DATETIME"},
		{"missing_since", "DATETIME"},
		{"status", "TEXT NOT NULL DEFAULT 'processed'"},
		{"error", "TEXT"},
	}

	for _, a := range additions {
		if cols[a.name] {
			continue
		}
		if _, err := database.Exec(fmt.Sprintf(`ALTER TABLE files ADD COLUMN %s %s`, a.name, a.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", a.name, err)
		}
	}

	return nil
}

// columns returns the set of column names present on table, by name.
func columns(database *sql.DB, table string) (map[string]bool, error) {
	// table is always one of this package's own fixed table names (never
	// user input), so building the PRAGMA statement with fmt.Sprintf
	// carries no injection risk — PRAGMA table_info also doesn't accept a
	// bound parameter for its table name argument anyway.
	rows, err := database.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := make(map[string]bool)
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}
