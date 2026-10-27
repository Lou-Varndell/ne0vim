package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"media-site/internal/catalogmodel"
)

// TotalFileCount reports how many catalogued images live at or beneath
// prefix.
func (s *Store) TotalFileCount(ctx context.Context, prefix string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM files f
		JOIN images i ON i.file_id = f.id
		WHERE f.path LIKE ?
	`, prefix+"%").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("store: count files under %q: %w", prefix, err)
	}
	return count, nil
}

// TagCounts returns every distinct tag carried by a file at or beneath
// prefix, with how many files carry it, ordered most-common first.
func (s *Store) TagCounts(ctx context.Context, prefix string) ([]catalogmodel.TagCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.name, COUNT(DISTINCT f.id) AS n
		FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
		JOIN tags t ON t.id = ft.tag_id
		WHERE f.path LIKE ?
		GROUP BY t.name
		ORDER BY n DESC, t.name
	`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("store: tag counts under %q: %w", prefix, err)
	}
	defer rows.Close()

	var counts []catalogmodel.TagCount
	for rows.Next() {
		var c catalogmodel.TagCount
		if err := rows.Scan(&c.Name, &c.Count); err != nil {
			return nil, fmt.Errorf("store: scan tag count: %w", err)
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

// SiteCounts returns every distinct origin site among files at or beneath
// prefix, with how many files came from it, ordered most-common first.
func (s *Store) SiteCounts(ctx context.Context, prefix string) ([]catalogmodel.SiteCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.site, COUNT(*) AS n
		FROM files f
		JOIN origins o ON o.id = f.origin_id
		WHERE f.path LIKE ? AND o.site IS NOT NULL AND o.site != ''
		GROUP BY o.site
		ORDER BY n DESC, o.site
	`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("store: site counts under %q: %w", prefix, err)
	}
	defer rows.Close()

	var counts []catalogmodel.SiteCount
	for rows.Next() {
		var c catalogmodel.SiteCount
		if err := rows.Scan(&c.Site, &c.Count); err != nil {
			return nil, fmt.Errorf("store: scan site count: %w", err)
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

// StatusCounts returns every distinct files.status among files at or
// beneath prefix, with how many files carry it, ordered most-common first.
func (s *Store) StatusCounts(ctx context.Context, prefix string) ([]catalogmodel.StatusCount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.status, COUNT(*) AS n
		FROM files f
		WHERE f.path LIKE ? AND f.status IS NOT NULL AND f.status != ''
		GROUP BY f.status
		ORDER BY n DESC, f.status
	`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("store: status counts under %q: %w", prefix, err)
	}
	defer rows.Close()

	var counts []catalogmodel.StatusCount
	for rows.Next() {
		var c catalogmodel.StatusCount
		if err := rows.Scan(&c.Status, &c.Count); err != nil {
			return nil, fmt.Errorf("store: scan status count: %w", err)
		}
		counts = append(counts, c)
	}
	return counts, rows.Err()
}

// DimensionRange reports the min/max image width and height among files at
// or beneath prefix. Fields are zero if no catalogued image there has
// dimensions.
func (s *Store) DimensionRange(ctx context.Context, prefix string) (catalogmodel.DimensionRange, error) {
	var widthMin, widthMax, heightMin, heightMax sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(i.width), MAX(i.width), MIN(i.height), MAX(i.height)
		FROM files f
		JOIN images i ON i.file_id = f.id
		WHERE f.path LIKE ?
	`, prefix+"%").Scan(&widthMin, &widthMax, &heightMin, &heightMax)
	if err != nil {
		return catalogmodel.DimensionRange{}, fmt.Errorf("store: dimension range under %q: %w", prefix, err)
	}
	return catalogmodel.DimensionRange{
		WidthMin:  int(widthMin.Int64),
		WidthMax:  int(widthMax.Int64),
		HeightMin: int(heightMin.Int64),
		HeightMax: int(heightMax.Int64),
	}, nil
}

// DiscoveredRange reports the earliest and latest origins.discovered_at
// among files at or beneath prefix, formatted YYYY-MM-DD. Both fields are
// empty if no file there has a known origin.
func (s *Store) DiscoveredRange(ctx context.Context, prefix string) (catalogmodel.DateRange, error) {
	var from, to sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(o.discovered_at), MAX(o.discovered_at)
		FROM files f
		JOIN origins o ON o.id = f.origin_id
		WHERE f.path LIKE ? AND o.discovered_at IS NOT NULL
	`, prefix+"%").Scan(&from, &to)
	if err != nil {
		return catalogmodel.DateRange{}, fmt.Errorf("store: discovered range under %q: %w", prefix, err)
	}
	return catalogmodel.DateRange{
		From: dateOnly(from.String),
		To:   dateOnly(to.String),
	}, nil
}

// dateOnly trims a DATETIME string down to its leading YYYY-MM-DD, for
// display and for pre-filling a <input type="date">.
func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// ListFilteredImagePaths returns up to limit catalogued image paths at or
// beneath prefix that match filter, ordered by path, starting after the
// first offset of them. Unlike ListImagePaths, it searches the whole
// subtree recursively rather than one directory's direct children — a
// filter is a search, not a listing.
func (s *Store) ListFilteredImagePaths(ctx context.Context, prefix string, filter catalogmodel.FileFilter, offset, limit int) ([]string, error) {
	clause, args := filterClause(filter)

	query := `
		SELECT DISTINCT f.path
		FROM files f
		JOIN images i ON i.file_id = f.id
		LEFT JOIN origins o ON o.id = f.origin_id
		WHERE f.path LIKE ?` + clause + `
		ORDER BY f.path
		LIMIT ? OFFSET ?
	`
	queryArgs := append([]any{prefix + "%"}, args...)
	queryArgs = append(queryArgs, limit, offset)

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("store: list filtered images under %q: %w", prefix, err)
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, fmt.Errorf("store: scan filtered image: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, rows.Err()
}

// filterClause builds the " AND ..." SQL fragment and matching args for
// filter, assuming the query already joins files as f, images as i, and
// left-joins origins as o. Returns ("", nil) if filter has no criteria.
//
// Each selected tag becomes its own "f.id IN (...)" clause, so multiple
// tags are ANDed together — a file must carry every selected tag, not just
// one of them. That's the more useful default for narrowing a search; a
// single clause with "IN (?, ?, ...)" would give OR semantics instead, if
// that's ever preferred.
func filterClause(filter catalogmodel.FileFilter) (string, []any) {
	var clauses []string
	var args []any

	if filter.Site != "" {
		clauses = append(clauses, "o.site LIKE ?")
		args = append(args, "%"+filter.Site+"%")
	}
	if filter.Status != "" {
		clauses = append(clauses, "f.status = ?")
		args = append(args, filter.Status)
	}
	if filter.WidthMin > 0 {
		clauses = append(clauses, "i.width >= ?")
		args = append(args, filter.WidthMin)
	}
	if filter.WidthMax > 0 {
		clauses = append(clauses, "i.width <= ?")
		args = append(args, filter.WidthMax)
	}
	if filter.HeightMin > 0 {
		clauses = append(clauses, "i.height >= ?")
		args = append(args, filter.HeightMin)
	}
	if filter.HeightMax > 0 {
		clauses = append(clauses, "i.height <= ?")
		args = append(args, filter.HeightMax)
	}
	if filter.DiscoveredFrom != "" {
		clauses = append(clauses, "o.discovered_at >= ?")
		args = append(args, filter.DiscoveredFrom)
	}
	if filter.DiscoveredTo != "" {
		clauses = append(clauses, "o.discovered_at <= ?")
		args = append(args, filter.DiscoveredTo)
	}
	for _, tag := range filter.Tags {
		clauses = append(clauses, `f.id IN (
			SELECT ft.file_id FROM file_tags ft JOIN tags t ON t.id = ft.tag_id WHERE t.name = ?
		)`)
		args = append(args, tag)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(clauses, " AND "), args
}
