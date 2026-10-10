package invlib

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertMediaProbe inserts or replaces the raw ffprobe output for p.FileID.
// probed_at is left to SQLite's column default on insert; call GetMediaProbe
// afterward if the caller needs the exact timestamp.
func (db *DB) UpsertMediaProbe(ctx context.Context, p *MediaProbe) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_probe (file_id, ffprobe_json)
		VALUES (?, ?)
		ON CONFLICT (file_id) DO UPDATE SET
			ffprobe_json = excluded.ffprobe_json,
			probed_at = CURRENT_TIMESTAMP`,
		p.FileID, p.FFprobeJSON,
	)
	if err != nil {
		return fmt.Errorf("invlib: upsert media probe for file %d: %w", p.FileID, err)
	}
	return nil
}

// GetMediaProbe returns the media_probe row for fileID, or sql.ErrNoRows if
// none exists.
func (db *DB) GetMediaProbe(ctx context.Context, fileID int64) (*MediaProbe, error) {
	row := db.QueryRowContext(ctx, `
		SELECT file_id, ffprobe_json, probed_at FROM media_probe WHERE file_id = ?`, fileID,
	)

	var p MediaProbe
	err := row.Scan(&p.FileID, &p.FFprobeJSON, &p.ProbedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: get media probe for file %d: %w", fileID, err)
	}
	return &p, nil
}

// DeleteMediaProbe removes the media_probe row for fileID.
func (db *DB) DeleteMediaProbe(ctx context.Context, fileID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM media_probe WHERE file_id = ?`, fileID)
	if err != nil {
		return fmt.Errorf("invlib: delete media probe for file %d: %w", fileID, err)
	}
	return nil
}
