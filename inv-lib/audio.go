package invlib

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertAudio inserts or replaces the audio row for a.FileID.
func (db *DB) UpsertAudio(ctx context.Context, a *Audio) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO audio (file_id, duration_ms, codec, sample_rate, channels, bitrate)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_id) DO UPDATE SET
			duration_ms = excluded.duration_ms,
			codec = excluded.codec,
			sample_rate = excluded.sample_rate,
			channels = excluded.channels,
			bitrate = excluded.bitrate`,
		a.FileID, a.DurationMs, a.Codec, a.SampleRate, a.Channels, a.Bitrate,
	)
	if err != nil {
		return fmt.Errorf("invlib: upsert audio for file %d: %w", a.FileID, err)
	}
	return nil
}

// GetAudio returns the audio row for fileID, or sql.ErrNoRows if none exists.
func (db *DB) GetAudio(ctx context.Context, fileID int64) (*Audio, error) {
	row := db.QueryRowContext(ctx, `
		SELECT file_id, duration_ms, codec, sample_rate, channels, bitrate
		FROM audio WHERE file_id = ?`, fileID,
	)

	var a Audio
	err := row.Scan(&a.FileID, &a.DurationMs, &a.Codec, &a.SampleRate, &a.Channels, &a.Bitrate)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: get audio for file %d: %w", fileID, err)
	}
	return &a, nil
}

// DeleteAudio removes the audio row for fileID without touching the
// underlying file.
func (db *DB) DeleteAudio(ctx context.Context, fileID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM audio WHERE file_id = ?`, fileID)
	if err != nil {
		return fmt.Errorf("invlib: delete audio for file %d: %w", fileID, err)
	}
	return nil
}
