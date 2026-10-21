package invlib

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertVideo inserts or replaces the video row for v.FileID.
func (db *DB) UpsertVideo(ctx context.Context, v *Video) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO videos (
			file_id, duration_ms, width, height, video_codec, video_profile,
			video_level, pix_fmt, fps, video_bitrate,
			audio_codec, audio_channels, audio_sample_rate, audio_bitrate
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_id) DO UPDATE SET
			duration_ms = excluded.duration_ms,
			width = excluded.width,
			height = excluded.height,
			video_codec = excluded.video_codec,
			video_profile = excluded.video_profile,
			video_level = excluded.video_level,
			pix_fmt = excluded.pix_fmt,
			fps = excluded.fps,
			video_bitrate = excluded.video_bitrate,
			audio_codec = excluded.audio_codec,
			audio_channels = excluded.audio_channels,
			audio_sample_rate = excluded.audio_sample_rate,
			audio_bitrate = excluded.audio_bitrate`,
		v.FileID, v.DurationMs, v.Width, v.Height, v.VideoCodec, v.VideoProfile,
		v.VideoLevel, v.PixFmt, v.FPS, v.VideoBitrate,
		v.AudioCodec, v.AudioChannels, v.AudioSampleRate, v.AudioBitrate,
	)
	if err != nil {
		return fmt.Errorf("invlib: upsert video for file %d: %w", v.FileID, err)
	}
	return nil
}

// GetVideo returns the video row for fileID, or sql.ErrNoRows if none exists.
func (db *DB) GetVideo(ctx context.Context, fileID int64) (*Video, error) {
	row := db.QueryRowContext(ctx, `
		SELECT file_id, duration_ms, width, height, video_codec, video_profile,
			video_level, pix_fmt, fps, video_bitrate,
			audio_codec, audio_channels, audio_sample_rate, audio_bitrate
		FROM videos WHERE file_id = ?`, fileID,
	)

	var v Video
	err := row.Scan(
		&v.FileID, &v.DurationMs, &v.Width, &v.Height, &v.VideoCodec, &v.VideoProfile,
		&v.VideoLevel, &v.PixFmt, &v.FPS, &v.VideoBitrate,
		&v.AudioCodec, &v.AudioChannels, &v.AudioSampleRate, &v.AudioBitrate,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: get video for file %d: %w", fileID, err)
	}
	return &v, nil
}

// DeleteVideo removes the video row for fileID without touching the
// underlying file.
func (db *DB) DeleteVideo(ctx context.Context, fileID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM videos WHERE file_id = ?`, fileID)
	if err != nil {
		return fmt.Errorf("invlib: delete video for file %d: %w", fileID, err)
	}
	return nil
}
