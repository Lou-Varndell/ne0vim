package invlib

import (
	"context"
	"database/sql"
	"fmt"
)

// CreateOrigin inserts o and sets o.ID to the new row's id.
func (db *DB) CreateOrigin(ctx context.Context, o *Origin) error {
	res, err := db.ExecContext(ctx, `
		INSERT INTO origins (type, site, url, identifier, metadata, original_file, discovered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		o.Type, o.Site, o.URL, o.Identifier, o.Metadata, o.OriginalFile, o.DiscoveredAt,
	)
	if err != nil {
		return fmt.Errorf("invlib: create origin: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("invlib: create origin: %w", err)
	}
	o.ID = id
	return nil
}

// GetOrigin returns the origin with the given id, or sql.ErrNoRows if none exists.
func (db *DB) GetOrigin(ctx context.Context, id int64) (*Origin, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, type, site, url, identifier, metadata, original_file, discovered_at
		FROM origins WHERE id = ?`, id,
	)
	return scanOrigin(row)
}

// GetOriginBySiteIdentifier looks up an origin by its (site, identifier)
// unique key, or returns sql.ErrNoRows if none exists.
func (db *DB) GetOriginBySiteIdentifier(ctx context.Context, site, identifier string) (*Origin, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, type, site, url, identifier, metadata, original_file, discovered_at
		FROM origins WHERE site = ? AND identifier = ?`, site, identifier,
	)
	return scanOrigin(row)
}

// EnsureOrigin inserts o if no origin exists matching (o.Site,
// o.Identifier) yet, or leaves the existing row untouched and sets o.ID to
// its id otherwise. It reports whether a new row was inserted. SQLite
// treats a NULL Site or Identifier as distinct from every other NULL for
// UNIQUE purposes, so an origin with either field nil is always inserted
// fresh rather than matched against a prior nil-valued row.
func (db *DB) EnsureOrigin(ctx context.Context, o *Origin) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO origins (type, site, url, identifier, metadata, original_file, discovered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (site, identifier) DO NOTHING`,
		o.Type, o.Site, o.URL, o.Identifier, o.Metadata, o.OriginalFile, o.DiscoveredAt,
	)
	if err != nil {
		return false, fmt.Errorf("invlib: ensure origin: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("invlib: ensure origin: %w", err)
	}
	if affected > 0 {
		id, err := res.LastInsertId()
		if err != nil {
			return false, fmt.Errorf("invlib: ensure origin: %w", err)
		}
		o.ID = id
		return true, nil
	}

	// A conflict only occurs when both Site and Identifier are non-NULL
	// (see the doc comment above), so both pointers are safe to
	// dereference here.
	existing, err := db.GetOriginBySiteIdentifier(ctx, *o.Site, *o.Identifier)
	if err != nil {
		return false, fmt.Errorf("invlib: ensure origin: %w", err)
	}
	o.ID = existing.ID
	return false, nil
}

// ListOrigins returns every origin, ordered by id.
func (db *DB) ListOrigins(ctx context.Context) ([]*Origin, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, type, site, url, identifier, metadata, original_file, discovered_at
		FROM origins ORDER BY id`,
	)
	if err != nil {
		return nil, fmt.Errorf("invlib: list origins: %w", err)
	}
	defer rows.Close()

	var out []*Origin
	for rows.Next() {
		o, err := scanOrigin(rows)
		if err != nil {
			return nil, fmt.Errorf("invlib: list origins: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOrigin updates every column of the origin matching o.ID.
func (db *DB) UpdateOrigin(ctx context.Context, o *Origin) error {
	_, err := db.ExecContext(ctx, `
		UPDATE origins SET
			type = ?, site = ?, url = ?, identifier = ?,
			metadata = ?, original_file = ?, discovered_at = ?
		WHERE id = ?`,
		o.Type, o.Site, o.URL, o.Identifier, o.Metadata, o.OriginalFile, o.DiscoveredAt, o.ID,
	)
	if err != nil {
		return fmt.Errorf("invlib: update origin %d: %w", o.ID, err)
	}
	return nil
}

// DeleteOrigin deletes the origin with the given id. Files referencing it
// keep their origin_id column (the schema does not cascade this FK).
func (db *DB) DeleteOrigin(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM origins WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("invlib: delete origin %d: %w", id, err)
	}
	return nil
}

// rowScanner lets scanOrigin accept either *sql.Row or *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrigin(row rowScanner) (*Origin, error) {
	var o Origin
	err := row.Scan(
		&o.ID, &o.Type, &o.Site, &o.URL, &o.Identifier,
		&o.Metadata, &o.OriginalFile, &o.DiscoveredAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: scan origin: %w", err)
	}
	return &o, nil
}
