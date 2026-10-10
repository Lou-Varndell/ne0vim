// Package invlib provides a Go library for creating, migrating, and
// querying the inventory SQLite database defined by schema.sql.
package invlib

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed indexes.sql
var indexes string

// DB wraps a *sql.DB connected to an inventory database. Embedding *sql.DB
// gives callers direct access to Begin, Close, Ping, etc. when the CRUD
// helpers below aren't enough.
type DB struct {
	*sql.DB
}

// Open creates (if necessary) and connects to the SQLite database at path,
// applying the schema in schema.sql. The DSN enables foreign key
// enforcement and TEXT->time.Time scanning for every pooled connection,
// since SQLite pragmas are connection-scoped rather than database-wide.
func Open(ctx context.Context, path string) (*DB, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_texttotime=true",
		path,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("invlib: open %s: %w", path, err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("invlib: ping %s: %w", path, err)
	}

	db := &DB{sqlDB}
	if err := db.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}

	return db, nil
}

// migrate applies schema.sql, repairs any pre-inv-lib legacy column shapes
// (see legacy_migrate.go), then applies indexes.sql. Every schema.sql
// statement is guarded with IF NOT EXISTS, and the legacy-column repair is a
// no-op once already applied, so running migrate against an already-
// migrated database is a no-op. The legacy-column repair must run between
// schema creation and index creation: several indexes reference columns
// (hash, origin_id, status) a pre-migration table doesn't have yet.
func (db *DB) migrate(ctx context.Context) error {
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("invlib: migrate: %w", err)
	}
	if err := db.migrateLegacyColumns(ctx); err != nil {
		return fmt.Errorf("invlib: migrate: %w", err)
	}
	if _, err := db.ExecContext(ctx, indexes); err != nil {
		return fmt.Errorf("invlib: migrate: create indexes: %w", err)
	}
	return nil
}
