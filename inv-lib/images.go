package invlib

import (
	"context"
	"database/sql"
	"fmt"
)

// UpsertImage inserts or replaces the image row for img.FileID.
func (db *DB) UpsertImage(ctx context.Context, img *Image) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO images (file_id, width, height, format, color_space)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (file_id) DO UPDATE SET
			width = excluded.width,
			height = excluded.height,
			format = excluded.format,
			color_space = excluded.color_space`,
		img.FileID, img.Width, img.Height, img.Format, img.ColorSpace,
	)
	if err != nil {
		return fmt.Errorf("invlib: upsert image for file %d: %w", img.FileID, err)
	}
	return nil
}

// InsertImageIfAbsent inserts the image row for img.FileID only if one
// doesn't already exist, reporting whether it did. Unlike UpsertImage, it
// never overwrites an existing row.
func (db *DB) InsertImageIfAbsent(ctx context.Context, img *Image) (bool, error) {
	res, err := db.ExecContext(ctx, `
		INSERT INTO images (file_id, width, height, format, color_space)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (file_id) DO NOTHING`,
		img.FileID, img.Width, img.Height, img.Format, img.ColorSpace,
	)
	if err != nil {
		return false, fmt.Errorf("invlib: insert image if absent for file %d: %w", img.FileID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("invlib: insert image if absent for file %d: %w", img.FileID, err)
	}
	return affected > 0, nil
}

// GetImage returns the image row for fileID, or sql.ErrNoRows if none exists.
func (db *DB) GetImage(ctx context.Context, fileID int64) (*Image, error) {
	row := db.QueryRowContext(ctx, `
		SELECT file_id, width, height, format, color_space FROM images WHERE file_id = ?`, fileID,
	)

	var img Image
	err := row.Scan(&img.FileID, &img.Width, &img.Height, &img.Format, &img.ColorSpace)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, fmt.Errorf("invlib: get image for file %d: %w", fileID, err)
	}
	return &img, nil
}

// DeleteImage removes the image row for fileID without touching the
// underlying file.
func (db *DB) DeleteImage(ctx context.Context, fileID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM images WHERE file_id = ?`, fileID)
	if err != nil {
		return fmt.Errorf("invlib: delete image for file %d: %w", fileID, err)
	}
	return nil
}

// ListImageFiles returns every file with a corresponding images row — the
// files inv-lib considers images, as opposed to a video or audio file —
// excluding any already marked missing, ordered by id. WarmAll uses this
// to find the set of files a thumbnail cache should have an entry for.
func (db *DB) ListImageFiles(ctx context.Context) ([]*File, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT `+fileColumns+`
		FROM files
		JOIN images ON images.file_id = files.id
		WHERE files.missing_since IS NULL
		ORDER BY files.id`)
	if err != nil {
		return nil, fmt.Errorf("invlib: list image files: %w", err)
	}
	defer rows.Close()
	return collectFiles(rows)
}
