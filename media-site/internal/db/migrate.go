package db

import (
	"database/sql"
	"fmt"
)

// migrateFiles brings an existing files table up to the current column set.
// schema.sql's CREATE TABLE IF NOT EXISTS only applies to a brand new
// database — for one created before this column set existed, this performs
// the equivalent ALTERs. It is always safe to call: every step first checks
// whether it has already been applied.
func migrateFiles(database *sql.DB) error {
	cols, err := columns(database, "files")
	if err != nil {
		return fmt.Errorf("inspect files columns: %w", err)
	}

	switch {
	case cols["blake3"] && !cols["hash"]:
		// The common case: nothing has ever written to hash, so renaming
		// the column in place loses nothing.
		if _, err := database.Exec(`ALTER TABLE files RENAME COLUMN blake3 TO hash`); err != nil {
			return fmt.Errorf("rename blake3 to hash: %w", err)
		}
		cols["hash"] = true
		delete(cols, "blake3")

	case cols["blake3"] && cols["hash"]:
		// A database two tools migrated independently (one renamed
		// blake3 to hash, the other kept re-adding blake3 by name) can
		// end up with both. The two never overlap in practice — each row
		// was written by exactly one of the two code paths — so backfill
		// hash from any blake3 value it's still missing, then drop
		// blake3 for good. The index has to go first: SQLite won't drop a
		// column a surviving index still references.
		if _, err := database.Exec(`
			UPDATE files SET hash = blake3
			WHERE (hash IS NULL OR hash = '') AND blake3 IS NOT NULL AND blake3 != ''
		`); err != nil {
			return fmt.Errorf("backfill hash from blake3: %w", err)
		}
		if _, err := database.Exec(`DROP INDEX IF EXISTS idx_files_blake3`); err != nil {
			return fmt.Errorf("drop idx_files_blake3: %w", err)
		}
		if _, err := database.Exec(`ALTER TABLE files DROP COLUMN blake3`); err != nil {
			return fmt.Errorf("drop blake3 column: %w", err)
		}
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
		{"status", "TEXT"},
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

	// site/source_url/original_file/discovered_at are a flat-schema
	// predecessor of the normalized origins table (see migrateOrigins):
	// site and url already live on origins, and original_file/
	// discovered_at are being added there now. Nothing backfills these —
	// every live row's values were empty in practice (ingest, the only
	// writer, had never run against them), so there's nothing to lose by
	// dropping them outright.
	for _, legacy := range []string{"site", "source_url", "original_file", "discovered_at"} {
		if !cols[legacy] {
			continue
		}
		if _, err := database.Exec(fmt.Sprintf(`ALTER TABLE files DROP COLUMN %s`, legacy)); err != nil {
			return fmt.Errorf("drop legacy column %s: %w", legacy, err)
		}
		delete(cols, legacy)
	}

	return nil
}

// migrateOrigins adds original_file and discovered_at to an origins table
// created before this column set existed. New databases get both straight
// from schema.sql; this only matters for one that already had an origins
// table.
func migrateOrigins(database *sql.DB) error {
	cols, err := columns(database, "origins")
	if err != nil {
		return fmt.Errorf("inspect origins columns: %w", err)
	}

	additions := []struct {
		name string
		ddl  string
	}{
		{"original_file", "TEXT"},
		{"discovered_at", "DATETIME"},
	}
	for _, a := range additions {
		if cols[a.name] {
			continue
		}
		if _, err := database.Exec(fmt.Sprintf(`ALTER TABLE origins ADD COLUMN %s %s`, a.name, a.ddl)); err != nil {
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
