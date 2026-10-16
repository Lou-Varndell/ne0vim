package invlib

// This file repairs databases created by site-scraper's and image-browser's
// pre-inv-lib SQLite code (their own, byte-identical internal/db/migrate.go
// files). It is purely historical: schema.sql has never contained the
// legacy columns these steps touch, so every step is a no-op against a
// brand-new or already-migrated database. Safe to delete in a future
// release once no pre-cutover database files remain in the wild.

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateLegacyColumns brings an existing files/origins table up to the
// current column set, for databases created before inv-lib unified the
// schema. It must run after schema.sql's CREATE TABLE IF NOT EXISTS (which
// only applies to a brand new database) and before indexes.sql, several of
// whose indexes reference columns a pre-migration table doesn't have yet.
func (db *DB) migrateLegacyColumns(ctx context.Context) error {
	if err := db.migrateLegacyFilesColumns(ctx); err != nil {
		return fmt.Errorf("migrate legacy files columns: %w", err)
	}
	if err := db.migrateLegacyOriginsColumns(ctx); err != nil {
		return fmt.Errorf("migrate legacy origins columns: %w", err)
	}
	return nil
}

func (db *DB) migrateLegacyFilesColumns(ctx context.Context) error {
	cols, err := tableColumns(ctx, db, "files")
	if err != nil {
		return fmt.Errorf("inspect files columns: %w", err)
	}

	switch {
	case cols["blake3"] && !cols["hash"]:
		// The common case: nothing has ever written to hash, so renaming
		// the column in place loses nothing.
		if _, err := db.ExecContext(ctx, `ALTER TABLE files RENAME COLUMN blake3 TO hash`); err != nil {
			return fmt.Errorf("rename blake3 to hash: %w", err)
		}
		cols["hash"] = true
		delete(cols, "blake3")

	case cols["blake3"] && cols["hash"]:
		// A database two tools migrated independently (one renamed blake3
		// to hash, the other kept re-adding blake3 by name) can end up with
		// both. The two never overlap in practice — each row was written by
		// exactly one of the two code paths — so backfill hash from any
		// blake3 value it's still missing, then drop blake3 for good. The
		// index has to go first: SQLite won't drop a column a surviving
		// index still references.
		if _, err := db.ExecContext(ctx, `
			UPDATE files SET hash = blake3
			WHERE (hash IS NULL OR hash = '') AND blake3 IS NOT NULL AND blake3 != ''
		`); err != nil {
			return fmt.Errorf("backfill hash from blake3: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DROP INDEX IF EXISTS idx_files_blake3`); err != nil {
			return fmt.Errorf("drop idx_files_blake3: %w", err)
		}
		if _, err := db.ExecContext(ctx, `ALTER TABLE files DROP COLUMN blake3`); err != nil {
			return fmt.Errorf("drop blake3 column: %w", err)
		}
		delete(cols, "blake3")
	}

	// Order matters only for readability here: ADD COLUMN with a constant
	// default is independent of other columns, so these can run in any
	// order or be skipped individually depending on what's already present.
	additions := []struct{ name, ddl string }{
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
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE files ADD COLUMN %s %s`, a.name, a.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", a.name, err)
		}
	}

	// site/source_url/original_file/discovered_at are a flat-schema
	// predecessor of the normalized origins table (see
	// migrateLegacyOriginsColumns): site and url already live on origins,
	// and original_file/discovered_at were added there too. Nothing
	// backfills these — every live row's values were empty in practice —
	// so there's nothing to lose by dropping them outright.
	for _, legacy := range []string{"site", "source_url", "original_file", "discovered_at"} {
		if !cols[legacy] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE files DROP COLUMN %s`, legacy)); err != nil {
			return fmt.Errorf("drop legacy column %s: %w", legacy, err)
		}
		delete(cols, legacy)
	}

	return nil
}

func (db *DB) migrateLegacyOriginsColumns(ctx context.Context) error {
	cols, err := tableColumns(ctx, db, "origins")
	if err != nil {
		return fmt.Errorf("inspect origins columns: %w", err)
	}

	additions := []struct{ name, ddl string }{
		{"original_file", "TEXT"},
		{"discovered_at", "DATETIME"},
	}
	for _, a := range additions {
		if cols[a.name] {
			continue
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`ALTER TABLE origins ADD COLUMN %s %s`, a.name, a.ddl)); err != nil {
			return fmt.Errorf("add column %s: %w", a.name, err)
		}
	}
	return nil
}

// tableColumns returns the set of column names present on table, by name.
// table is always one of this package's own fixed table names (never user
// input), so building the PRAGMA statement with fmt.Sprintf carries no
// injection risk — PRAGMA table_info also doesn't accept a bound parameter
// for its table name argument anyway.
func tableColumns(ctx context.Context, db *DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, fmt.Sprintf(`PRAGMA table_info(%s)`, table))
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
