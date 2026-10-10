package ui

import (
	"context"
	"fmt"

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
// zero-value field means "don't filter on it"; Site and Tag match
// case-insensitive substrings. The zero-value Filter{} matches every
// indexed image.
type Filter struct {
	Site string
	Tag  string
}

// searchRecords returns every image record matching filter, ordered by
// path, with each record's Tags populated. An empty filter matches every
// indexed image. Only files with an images row (i.e. recognized as images,
// per schema.sql) are returned — this app never displays videos or audio.
func searchRecords(ctx context.Context, db *invlib.DB, filter Filter) ([]imageRecord, error) {
	results, err := db.ListFileRecords(ctx, invlib.FileQuery{
		Site:         filter.Site,
		Tag:          filter.Tag,
		RequireImage: true,
		WithTags:     true,
	})
	if err != nil {
		return nil, fmt.Errorf("search records: %w", err)
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
	return records, nil
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
