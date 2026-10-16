package ui

import (
	"context"
	"fmt"
	"time"

	invlib "inv-lib"
)

// imageRecord is the browser's view model: a file's 1:1 image extension
// joined with its site (via its origin, if any) and tags — the three
// things the UI searches, displays, and edits. invlib keeps File/Image/
// Origin/Tag as separate rows per schema.sql; this is just how the UI
// looks at them together.
type imageRecord struct {
	FileID int64
	Path   string
	Size   int64
	Width  int
	Height int
	Site   string
	Tags   []string
}

// Filter selects image records by metadata for Browser.Search. A
// zero-value field means "don't filter on it"; most string fields match
// case-insensitive substrings (Hash is an exact match; see its own doc
// comment). The zero-value Filter{} matches every indexed image.
//
// FileIDs, when non-nil, is set by the Advanced Search window's raw-SQL
// mode (see search.go) and overrides every other field below — see
// invlib.FileQuery.FileIDs for the exact nil-vs-empty-slice semantics
// that distinction relies on.
type Filter struct {
	Site string
	Tag  string
	Path string

	URL        string
	Identifier string
	Status     string
	Hash       string // exact match, not substring

	WidthMin, WidthMax   int
	HeightMin, HeightMax int

	DiscoveredFrom, DiscoveredTo *time.Time

	FileIDs []int64
}

// toFileQuery translates filter's fields into the corresponding
// invlib.FileQuery fields, leaving RequireImage/ExcludeMissing/WithTags/
// Limit/Offset for the caller to set afterward — those vary between the
// paged ListFileRecords call and the unpaged CountFileRecords call that
// searchRecords makes side by side.
func toFileQuery(filter Filter) invlib.FileQuery {
	return invlib.FileQuery{
		Site:           filter.Site,
		Tag:            filter.Tag,
		Path:           filter.Path,
		URL:            filter.URL,
		Identifier:     filter.Identifier,
		Status:         filter.Status,
		Hash:           filter.Hash,
		WidthMin:       filter.WidthMin,
		WidthMax:       filter.WidthMax,
		HeightMin:      filter.HeightMin,
		HeightMax:      filter.HeightMax,
		DiscoveredFrom: filter.DiscoveredFrom,
		DiscoveredTo:   filter.DiscoveredTo,
		FileIDs:        filter.FileIDs,
	}
}

// searchRecords returns up to limit image records matching filter,
// skipping the first offset matches, ordered by path, with each record's
// Tags populated — plus the total number of records filter matches,
// ignoring limit/offset, so callers can show "page X of Y" without
// fetching every matching row. A limit of 0 means no limit: every
// matching record is returned, and total equals len(records). An empty
// filter matches every indexed image. Only files with an images row (i.e.
// recognized as images, per schema.sql) are returned — this app never
// displays videos or audio. Files a reconcile pass has marked missing
// (missing_since set, per reconcile.md) are excluded too, since there's
// nothing on disk for the browser to show.
func searchRecords(ctx context.Context, db *invlib.DB, filter Filter, limit, offset int) ([]imageRecord, int, error) {
	base := toFileQuery(filter)

	query := base
	query.RequireImage = true
	query.ExcludeMissing = true
	query.WithTags = true
	query.Limit = limit
	query.Offset = offset

	countQuery := base
	countQuery.RequireImage = true
	countQuery.ExcludeMissing = true

	total, err := db.CountFileRecords(ctx, countQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("search records: count: %w", err)
	}

	results, err := db.ListFileRecords(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("search records: %w", err)
	}

	records := make([]imageRecord, len(results))
	for i, r := range results {
		rec := imageRecord{FileID: r.File.ID, Path: r.File.Path, Size: r.File.Filesize}
		if r.Image != nil {
			if r.Image.Width != nil {
				rec.Width = *r.Image.Width
			}
			if r.Image.Height != nil {
				rec.Height = *r.Image.Height
			}
		}
		if r.Origin != nil && r.Origin.Site != nil {
			rec.Site = *r.Origin.Site
		}
		rec.Tags = make([]string, len(r.Tags))
		for j, t := range r.Tags {
			rec.Tags[j] = t.Name
		}
		records[i] = rec
	}
	return records, total, nil
}

// setSite attaches site as fileID's origin: creating a new "manual" origin
// if the file has none, updating its existing origin's site in place if it
// does, or clearing the association if site is blank. It never deletes an
// origin row, since another file could reference the same one.
func setSite(ctx context.Context, db *invlib.DB, fileID int64, site string) error {
	file, err := db.GetFile(ctx, fileID)
	if err != nil {
		return fmt.Errorf("set site: %w", err)
	}

	if site == "" {
		if file.OriginID == nil {
			return nil
		}
		file.OriginID = nil
		return db.UpdateFile(ctx, file)
	}

	if file.OriginID != nil {
		origin, err := db.GetOrigin(ctx, *file.OriginID)
		if err != nil {
			return fmt.Errorf("set site: %w", err)
		}
		origin.Site = &site
		return db.UpdateOrigin(ctx, origin)
	}

	origin := &invlib.Origin{Type: "manual", Site: &site}
	if err := db.CreateOrigin(ctx, origin); err != nil {
		return fmt.Errorf("set site: %w", err)
	}
	file.OriginID = &origin.ID
	return db.UpdateFile(ctx, file)
}

// setTags replaces fileID's tags with desired, creating any tag names that
// don't exist yet (via GetOrCreateTag, which applies normalizeTagName) and
// untagging whatever is no longer present.
func setTags(ctx context.Context, db *invlib.DB, fileID int64, desired []string) error {
	if err := db.SetFileTags(ctx, fileID, desired); err != nil {
		return fmt.Errorf("set tags: %w", err)
	}
	return nil
}

// addTagsToFiles attaches every tag in tags (creating any that don't
// already exist, via GetOrCreateTag) to every file in fileIDs, leaving each
// file's other existing tags untouched — unlike setTags, which replaces one
// file's whole tag set. Used by the browser's bulk tag dialog.
func addTagsToFiles(ctx context.Context, db *invlib.DB, fileIDs []int64, tags []string) error {
	tagIDs := make([]int64, len(tags))
	for i, name := range tags {
		tag, err := db.GetOrCreateTag(ctx, name)
		if err != nil {
			return fmt.Errorf("add tags to files: %w", err)
		}
		tagIDs[i] = tag.ID
	}

	for _, fileID := range fileIDs {
		for _, tagID := range tagIDs {
			if err := db.TagFile(ctx, fileID, tagID); err != nil {
				return fmt.Errorf("add tags to files: %w", err)
			}
		}
	}
	return nil
}
