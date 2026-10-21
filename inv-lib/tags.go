package invlib

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// normalizeTagName decides how two tag spellings are treated as "the same
// tag" before they ever reach the UNIQUE(name) constraint in SQLite.
//
// Chosen rule: trim surrounding whitespace, then fold to lowercase. This
// treats "Beach", "beach", and " beach " as one tag and discards original
// casing, favoring consistent free-text tagging (as typed into a comma-
// separated field) over case-sensitive distinctions like "NYC" (place) vs.
// "nyc" (username) — a photo library tagging scheme is expected to want the
// former far more often than the latter.
func normalizeTagName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// tagExecer is satisfied by both *DB and *sql.Tx, letting the tag helpers
// below run either standalone or inside a caller-managed transaction (see
// SetFileTags).
type tagExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetOrCreateTag returns the tag matching normalizeTagName(name), creating
// it if it doesn't already exist.
func (db *DB) GetOrCreateTag(ctx context.Context, name string) (*Tag, error) {
	return getOrCreateTag(ctx, db, name)
}

func getOrCreateTag(ctx context.Context, exec tagExecer, name string) (*Tag, error) {
	normalized := normalizeTagName(name)

	tag, err := getTagByName(ctx, exec, normalized)
	if err == nil {
		return tag, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	res, err := exec.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?)`, normalized)
	if err != nil {
		return nil, fmt.Errorf("invlib: create tag %q: %w", normalized, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("invlib: create tag %q: %w", normalized, err)
	}
	return &Tag{ID: id, Name: normalized}, nil
}

// GetTagByName returns the tag with the given exact name, or sql.ErrNoRows
// if none exists.
func (db *DB) GetTagByName(ctx context.Context, name string) (*Tag, error) {
	return getTagByName(ctx, db, name)
}

func getTagByName(ctx context.Context, exec tagExecer, name string) (*Tag, error) {
	var t Tag
	err := exec.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name = ?`, name).Scan(&t.ID, &t.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: get tag %q: %w", name, err)
	}
	return &t, nil
}

// ListTags returns every tag, ordered by name.
func (db *DB) ListTags(ctx context.Context) ([]*Tag, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("invlib: list tags: %w", err)
	}
	defer rows.Close()

	var out []*Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("invlib: list tags: %w", err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// DeleteTag deletes the tag itself, along with every file_tags row
// referencing it (ON DELETE CASCADE). It does not touch the tagged files.
func (db *DB) DeleteTag(ctx context.Context, id int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM tags WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("invlib: delete tag %d: %w", id, err)
	}
	return nil
}

// TagFile associates tagID with fileID. It is idempotent: tagging the same
// file with the same tag twice is not an error.
func (db *DB) TagFile(ctx context.Context, fileID, tagID int64) error {
	return tagFile(ctx, db, fileID, tagID)
}

func tagFile(ctx context.Context, exec tagExecer, fileID, tagID int64) error {
	_, err := exec.ExecContext(ctx, `
		INSERT INTO file_tags (file_id, tag_id) VALUES (?, ?)
		ON CONFLICT (file_id, tag_id) DO NOTHING`, fileID, tagID,
	)
	if err != nil {
		return fmt.Errorf("invlib: tag file %d with tag %d: %w", fileID, tagID, err)
	}
	return nil
}

// UntagFile removes the association between tagID and fileID, if present.
func (db *DB) UntagFile(ctx context.Context, fileID, tagID int64) error {
	return untagFile(ctx, db, fileID, tagID)
}

func untagFile(ctx context.Context, exec tagExecer, fileID, tagID int64) error {
	_, err := exec.ExecContext(ctx, `
		DELETE FROM file_tags WHERE file_id = ? AND tag_id = ?`, fileID, tagID,
	)
	if err != nil {
		return fmt.Errorf("invlib: untag file %d from tag %d: %w", fileID, tagID, err)
	}
	return nil
}

// ListTagsForFile returns every tag attached to fileID, ordered by name.
func (db *DB) ListTagsForFile(ctx context.Context, fileID int64) ([]*Tag, error) {
	return listTagsForFile(ctx, db, fileID)
}

func listTagsForFile(ctx context.Context, exec tagExecer, fileID int64) ([]*Tag, error) {
	rows, err := exec.QueryContext(ctx, `
		SELECT t.id, t.name
		FROM tags t
		JOIN file_tags ft ON ft.tag_id = t.id
		WHERE ft.file_id = ?
		ORDER BY t.name`, fileID,
	)
	if err != nil {
		return nil, fmt.Errorf("invlib: list tags for file %d: %w", fileID, err)
	}
	defer rows.Close()

	var out []*Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("invlib: list tags for file %d: %w", fileID, err)
		}
		out = append(out, &t)
	}
	return out, rows.Err()
}

// SetFileTags replaces fileID's tag set with the tags named in names,
// creating any that don't exist yet (via GetOrCreateTag, so
// normalizeTagName applies) and untagging whatever is no longer present.
// The whole reconciliation runs in one transaction, so a failure partway
// through (e.g. on the third of five new tags) leaves the file's tag set
// unchanged rather than half-updated.
func (db *DB) SetFileTags(ctx context.Context, fileID int64, names []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("invlib: set file tags: begin: %w", err)
	}
	defer tx.Rollback()

	current, err := listTagsForFile(ctx, tx, fileID)
	if err != nil {
		return fmt.Errorf("invlib: set file tags: %w", err)
	}
	currentByName := make(map[string]*Tag, len(current))
	for _, t := range current {
		currentByName[t.Name] = t
	}

	keep := make(map[string]bool, len(names))
	for _, name := range names {
		tag, err := getOrCreateTag(ctx, tx, name)
		if err != nil {
			return fmt.Errorf("invlib: set file tags: %w", err)
		}
		keep[tag.Name] = true
		if _, ok := currentByName[tag.Name]; !ok {
			if err := tagFile(ctx, tx, fileID, tag.ID); err != nil {
				return fmt.Errorf("invlib: set file tags: %w", err)
			}
		}
	}

	for name, tag := range currentByName {
		if !keep[name] {
			if err := untagFile(ctx, tx, fileID, tag.ID); err != nil {
				return fmt.Errorf("invlib: set file tags: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("invlib: set file tags: commit: %w", err)
	}
	return nil
}

// ListFilesForTag returns every file tagged with tagID, ordered by id.
func (db *DB) ListFilesForTag(ctx context.Context, tagID int64) ([]*File, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+fileColumns+`
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		WHERE ft.tag_id = ?
		ORDER BY f.id`, tagID,
	)
	if err != nil {
		return nil, fmt.Errorf("invlib: list files for tag %d: %w", tagID, err)
	}
	defer rows.Close()
	return collectFiles(rows)
}
