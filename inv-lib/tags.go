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

// GetOrCreateTag returns the tag matching normalizeTagName(name), creating
// it if it doesn't already exist.
func (db *DB) GetOrCreateTag(ctx context.Context, name string) (*Tag, error) {
	normalized := normalizeTagName(name)

	tag, err := db.GetTagByName(ctx, normalized)
	if err == nil {
		return tag, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	res, err := db.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?)`, normalized)
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
	var t Tag
	err := db.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name = ?`, name).Scan(&t.ID, &t.Name)
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
	_, err := db.ExecContext(ctx, `
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
	_, err := db.ExecContext(ctx, `
		DELETE FROM file_tags WHERE file_id = ? AND tag_id = ?`, fileID, tagID,
	)
	if err != nil {
		return fmt.Errorf("invlib: untag file %d from tag %d: %w", fileID, tagID, err)
	}
	return nil
}

// ListTagsForFile returns every tag attached to fileID, ordered by name.
func (db *DB) ListTagsForFile(ctx context.Context, fileID int64) ([]*Tag, error) {
	rows, err := db.QueryContext(ctx, `
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
func (db *DB) SetFileTags(ctx context.Context, fileID int64, names []string) error {
	current, err := db.ListTagsForFile(ctx, fileID)
	if err != nil {
		return fmt.Errorf("invlib: set file tags: %w", err)
	}
	currentByName := make(map[string]*Tag, len(current))
	for _, t := range current {
		currentByName[t.Name] = t
	}

	keep := make(map[string]bool, len(names))
	for _, name := range names {
		tag, err := db.GetOrCreateTag(ctx, name)
		if err != nil {
			return fmt.Errorf("invlib: set file tags: %w", err)
		}
		keep[tag.Name] = true
		if _, ok := currentByName[tag.Name]; !ok {
			if err := db.TagFile(ctx, fileID, tag.ID); err != nil {
				return fmt.Errorf("invlib: set file tags: %w", err)
			}
		}
	}

	for name, tag := range currentByName {
		if !keep[name] {
			if err := db.UntagFile(ctx, fileID, tag.ID); err != nil {
				return fmt.Errorf("invlib: set file tags: %w", err)
			}
		}
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
