package invlib

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// FileRecord is a files row left-joined with its origin and image rows,
// and optionally its tags. Origin is nil when the file has no origin_id;
// Image is nil when the file has no images row; Tags is nil unless
// FileQuery.WithTags was set.
type FileRecord struct {
	File   *File
	Origin *Origin
	Image  *Image
	Tags   []*Tag
}

// FileQuery filters and shapes ListFileRecords. The zero value matches
// every file. PathPrefix, Path, Site, and Tag are case-insensitive
// substring (or, for PathPrefix, prefix) matches against path, path again,
// the file's origin site, and its tag names respectively.
type FileQuery struct {
	// PathPrefix restricts results to files whose path starts with this
	// value (case-insensitive). Empty matches every path.
	PathPrefix string

	// Path restricts results to files whose path contains this value
	// (case-insensitive substring). Empty matches every path. Independent
	// of PathPrefix — both apply if both are set.
	Path string

	// Site restricts results to files whose origin's site contains this
	// value (case-insensitive substring). Empty matches every site,
	// including files with no origin.
	Site string

	// Tag restricts results to files tagged with a tag whose name contains
	// this value (case-insensitive substring). Empty matches every file.
	Tag string

	// RequireImage restricts results to files with an images row (an INNER
	// JOIN), for a UI that only ever displays recognized images.
	RequireImage bool

	// MissingImage restricts results to files WITHOUT an images row (an
	// anti-join), for finding files never decoded into one. Mutually
	// exclusive with RequireImage.
	MissingImage bool

	// ExcludeMissing restricts results to files whose missing_since is
	// NULL, hiding files a reconcile pass found absent from disk (see
	// reconcile.md). False matches every file regardless of missing_since.
	ExcludeMissing bool

	// URL restricts results to files whose origin's url contains this
	// value (case-insensitive substring). Empty matches every url,
	// including files with no origin.
	URL string

	// Identifier restricts results to files whose origin's identifier
	// contains this value (case-insensitive substring). Empty matches
	// every identifier, including files with no origin.
	Identifier string

	// Status restricts results to files whose status contains this value
	// (case-insensitive substring). Empty matches every status, including
	// files with no status set.
	Status string

	// Hash restricts results to files whose hash exactly equals this value
	// (case-insensitive, since hex case shouldn't matter). Empty matches
	// every hash, including files with no hash recorded.
	Hash string

	// WidthMin/WidthMax restrict results to images whose width falls
	// within [WidthMin, WidthMax]. Zero on either side means unbounded on
	// that side — consistent with how image-browser's own -width/-height
	// flags already treat zero as "unfiltered" rather than "exactly zero".
	WidthMin, WidthMax int

	// HeightMin/HeightMax restrict results to images whose height falls
	// within [HeightMin, HeightMax]. Zero on either side means unbounded
	// on that side.
	HeightMin, HeightMax int

	// DiscoveredFrom/DiscoveredTo restrict results to files whose origin's
	// discovered_at falls within [DiscoveredFrom, DiscoveredTo]. A nil
	// bound is unbounded on that side.
	DiscoveredFrom, DiscoveredTo *time.Time

	// WithTags batch-attaches each result's Tags. Leave false to skip the
	// extra query when callers don't need tags.
	WithTags bool

	// Limit caps the number of results ListFileRecords returns. Zero means
	// no limit: every matching record is returned. CountFileRecords ignores
	// Limit and always counts every match.
	Limit int

	// Offset skips this many matching results before collecting Limit of
	// them. Only meaningful alongside a positive Limit.
	Offset int

	// FileIDs, when non-nil, restricts results to exactly these file IDs
	// and takes priority over every other field above — every other
	// field is ignored once FileIDs is non-nil. A non-nil but empty slice
	// means "match nothing" (e.g. a raw SQL search that matched zero
	// files); a nil slice means "no ID restriction at all", which is the
	// zero value and so behaves exactly as before this field existed.
	FileIDs []int64
}

// fileQueryJoinAndConds builds the images JOIN clause and WHERE conditions
// shared by ListFileRecords and CountFileRecords.
func fileQueryJoinAndConds(q FileQuery) (join string, conds []string, args []any, err error) {
	if q.RequireImage && q.MissingImage {
		return "", nil, nil, fmt.Errorf("RequireImage and MissingImage are mutually exclusive")
	}

	join = "LEFT JOIN images i ON i.file_id = f.id"
	if q.RequireImage {
		join = "JOIN images i ON i.file_id = f.id"
	}

	conds = []string{"1 = 1"}

	// RequireImage/MissingImage/ExcludeMissing are structural invariants a
	// caller sets independently of what the user is actually searching
	// for (e.g. the inventory-manager browser always sets RequireImage
	// and ExcludeMissing, regardless of which Filter fields are active) —
	// so they apply whether or not FileIDs narrows things further below,
	// unlike every other field.
	if q.MissingImage {
		conds = append(conds, "i.file_id IS NULL")
	}
	if q.ExcludeMissing {
		conds = append(conds, "f.missing_since IS NULL")
	}

	// FileIDs, when non-nil, replaces every *content* filter below it —
	// Site/Tag/Path/etc are ignored once it's set. This is the bridge for
	// a raw-SQL search (see readonly.go): once a caller has already
	// resolved an arbitrary query down to a set of file IDs, re-applying
	// Site/Tag/Path/etc on top of that set would have no well-defined
	// meaning (AND? OR?), so we don't.
	if q.FileIDs != nil {
		if len(q.FileIDs) == 0 {
			conds = append(conds, "1 = 0")
		} else {
			placeholders := make([]string, len(q.FileIDs))
			for i, id := range q.FileIDs {
				placeholders[i] = "?"
				args = append(args, id)
			}
			conds = append(conds, "f.id IN ("+strings.Join(placeholders, ",")+")")
		}
		return join, conds, args, nil
	}

	if q.PathPrefix != "" {
		conds = append(conds, "f.path LIKE ? COLLATE NOCASE")
		args = append(args, q.PathPrefix+"%")
	}
	if q.Path != "" {
		conds = append(conds, "f.path LIKE ? COLLATE NOCASE")
		args = append(args, "%"+q.Path+"%")
	}
	if q.Site != "" {
		conds = append(conds, "o.site LIKE ? COLLATE NOCASE")
		args = append(args, "%"+q.Site+"%")
	}
	if q.URL != "" {
		conds = append(conds, "o.url LIKE ? COLLATE NOCASE")
		args = append(args, "%"+q.URL+"%")
	}
	if q.Identifier != "" {
		conds = append(conds, "o.identifier LIKE ? COLLATE NOCASE")
		args = append(args, "%"+q.Identifier+"%")
	}
	if q.Status != "" {
		conds = append(conds, "f.status LIKE ? COLLATE NOCASE")
		args = append(args, "%"+q.Status+"%")
	}
	if q.Hash != "" {
		conds = append(conds, "f.hash = ? COLLATE NOCASE")
		args = append(args, q.Hash)
	}
	if q.WidthMin != 0 {
		conds = append(conds, "i.width >= ?")
		args = append(args, q.WidthMin)
	}
	if q.WidthMax != 0 {
		conds = append(conds, "i.width <= ?")
		args = append(args, q.WidthMax)
	}
	if q.HeightMin != 0 {
		conds = append(conds, "i.height >= ?")
		args = append(args, q.HeightMin)
	}
	if q.HeightMax != 0 {
		conds = append(conds, "i.height <= ?")
		args = append(args, q.HeightMax)
	}
	if q.DiscoveredFrom != nil {
		conds = append(conds, "o.discovered_at >= ?")
		args = append(args, q.DiscoveredFrom)
	}
	if q.DiscoveredTo != nil {
		conds = append(conds, "o.discovered_at <= ?")
		args = append(args, q.DiscoveredTo)
	}
	if q.Tag != "" {
		conds = append(conds, `f.id IN (
			SELECT ft.file_id FROM file_tags ft
			JOIN tags t ON t.id = ft.tag_id
			WHERE t.name LIKE ? COLLATE NOCASE
		)`)
		args = append(args, "%"+q.Tag+"%")
	}

	return join, conds, args, nil
}

// ListFileRecords returns every file matching q, ordered by path
// (case-insensitive). The zero-value FileQuery matches every file. When
// q.Limit is positive, at most Limit records are returned, skipping the
// first q.Offset matches.
func (db *DB) ListFileRecords(ctx context.Context, q FileQuery) ([]*FileRecord, error) {
	join, conds, args, err := fileQueryJoinAndConds(q)
	if err != nil {
		return nil, fmt.Errorf("invlib: list file records: %w", err)
	}

	query := `
SELECT
	f.id, f.path, f.filename, f.extension, f.mime_type, f.filesize, f.hash, f.origin_id,
	f.created_at, f.modified_at, f.scanned_at, f.last_verified_at, f.missing_since, f.status, f.error,
	i.file_id, i.width, i.height, i.format, i.color_space,
	o.id, o.type, o.site, o.url, o.identifier, o.metadata, o.original_file, o.discovered_at
FROM files f
` + join + `
LEFT JOIN origins o ON o.id = f.origin_id
WHERE ` + strings.Join(conds, " AND ") + `
ORDER BY f.path COLLATE NOCASE`

	if q.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, q.Limit, q.Offset)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("invlib: list file records: %w", err)
	}
	defer rows.Close()

	var records []*FileRecord
	for rows.Next() {
		r, err := scanFileRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("invlib: list file records: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("invlib: list file records: %w", err)
	}

	if q.WithTags {
		if err := db.attachTags(ctx, records); err != nil {
			return nil, fmt.Errorf("invlib: list file records: %w", err)
		}
	}

	return records, nil
}

// CountFileRecords returns the number of files matching q, ignoring
// q.Limit and q.Offset, so callers can show "page X of Y" without fetching
// every matching row.
func (db *DB) CountFileRecords(ctx context.Context, q FileQuery) (int, error) {
	join, conds, args, err := fileQueryJoinAndConds(q)
	if err != nil {
		return 0, fmt.Errorf("invlib: count file records: %w", err)
	}

	query := `
SELECT COUNT(*)
FROM files f
` + join + `
LEFT JOIN origins o ON o.id = f.origin_id
WHERE ` + strings.Join(conds, " AND ")

	var count int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("invlib: count file records: %w", err)
	}
	return count, nil
}

func scanFileRecord(row rowScanner) (*FileRecord, error) {
	var (
		f File

		imgFileID                sql.NullInt64
		imgWidth, imgHeight      *int
		imgFormat, imgColorSpace *string

		originID                                                                    sql.NullInt64
		originType                                                                  sql.NullString
		originSite, originURL, originIdentifier, originMetadata, originOriginalFile *string
		originDiscoveredAt                                                          *time.Time
	)

	err := row.Scan(
		&f.ID, &f.Path, &f.Filename, &f.Extension, &f.MimeType, &f.Filesize, &f.Hash, &f.OriginID,
		&f.CreatedAt, &f.ModifiedAt, &f.ScannedAt, &f.LastVerifiedAt, &f.MissingSince, &f.Status, &f.Error,
		&imgFileID, &imgWidth, &imgHeight, &imgFormat, &imgColorSpace,
		&originID, &originType, &originSite, &originURL, &originIdentifier, &originMetadata, &originOriginalFile, &originDiscoveredAt,
	)
	if err != nil {
		return nil, fmt.Errorf("invlib: scan file record: %w", err)
	}

	rec := &FileRecord{File: &f}

	if imgFileID.Valid {
		rec.Image = &Image{
			FileID:     imgFileID.Int64,
			Width:      imgWidth,
			Height:     imgHeight,
			Format:     imgFormat,
			ColorSpace: imgColorSpace,
		}
	}

	if originID.Valid {
		rec.Origin = &Origin{
			ID:           originID.Int64,
			Type:         originType.String,
			Site:         originSite,
			URL:          originURL,
			Identifier:   originIdentifier,
			Metadata:     originMetadata,
			OriginalFile: originOriginalFile,
			DiscoveredAt: originDiscoveredAt,
		}
	}

	return rec, nil
}

// attachTags batch-loads tags for records and sets each record's Tags
// field in place. Batching avoids exceeding SQLite's variable limit.
func (db *DB) attachTags(ctx context.Context, records []*FileRecord) error {
	if len(records) == 0 {
		return nil
	}

	const batchSize = 500

	for start := 0; start < len(records); start += batchSize {
		end := min(start+batchSize, len(records))
		batch := records[start:end]

		byID := make(map[int64]*FileRecord, len(batch))
		placeholders := make([]string, len(batch))
		args := make([]any, len(batch))

		for i, r := range batch {
			byID[r.File.ID] = r
			placeholders[i] = "?"
			args[i] = r.File.ID
		}

		query := `
SELECT ft.file_id, t.id, t.name
FROM file_tags ft
JOIN tags t ON t.id = ft.tag_id
WHERE ft.file_id IN (` + strings.Join(placeholders, ",") + `)
ORDER BY t.name COLLATE NOCASE`

		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("attach tags: %w", err)
		}

		for rows.Next() {
			var fileID int64
			var t Tag
			if err := rows.Scan(&fileID, &t.ID, &t.Name); err != nil {
				rows.Close()
				return fmt.Errorf("attach tags: %w", err)
			}
			if r, ok := byID[fileID]; ok {
				r.Tags = append(r.Tags, &t)
			}
		}

		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("attach tags: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("attach tags: %w", err)
		}
	}

	return nil
}

// ResolutionGroup is one processed image's dimensions and path.
type ResolutionGroup struct {
	Width, Height int
	Path          string
}

// ListImageResolutions returns width/height/path for every file with a
// decoded images row, optionally restricted to paths starting with
// pathPrefix (case-insensitive). An empty pathPrefix matches every path.
func (db *DB) ListImageResolutions(ctx context.Context, pathPrefix string) ([]ResolutionGroup, error) {
	conds := []string{"i.width IS NOT NULL", "i.height IS NOT NULL"}
	var args []any
	if pathPrefix != "" {
		conds = append(conds, "f.path LIKE ? COLLATE NOCASE")
		args = append(args, pathPrefix+"%")
	}

	query := `
SELECT i.width, i.height, f.path
FROM files f
JOIN images i ON i.file_id = f.id
WHERE ` + strings.Join(conds, " AND ")

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("invlib: list image resolutions: %w", err)
	}
	defer rows.Close()

	var out []ResolutionGroup
	for rows.Next() {
		var g ResolutionGroup
		if err := rows.Scan(&g.Width, &g.Height, &g.Path); err != nil {
			return nil, fmt.Errorf("invlib: list image resolutions: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
