package invlib

import (
	"context"
	"testing"
	"time"
)

func seedFileRecordFixtures(t *testing.T, ctx context.Context, db *DB) (withImage, withoutImage *File) {
	t.Helper()

	site := "example.com"
	origin := &Origin{Type: "web", Site: &site}
	if err := db.CreateOrigin(ctx, origin); err != nil {
		t.Fatalf("CreateOrigin: %v", err)
	}

	withImage = &File{Path: "/library/a/photo.jpg", Filename: "photo.jpg", Filesize: 1, OriginID: &origin.ID}
	if err := db.CreateFile(ctx, withImage); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	w, h := 100, 200
	if err := db.UpsertImage(ctx, &Image{FileID: withImage.ID, Width: &w, Height: &h}); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}
	if err := db.SetFileTags(ctx, withImage.ID, []string{"beach"}); err != nil {
		t.Fatalf("SetFileTags: %v", err)
	}

	withoutImage = &File{Path: "/library/b/doc.pdf", Filename: "doc.pdf", Filesize: 1}
	if err := db.CreateFile(ctx, withoutImage); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	return withImage, withoutImage
}

func TestListFileRecordsUnfiltered(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, withoutImage := seedFileRecordFixtures(t, ctx, db)

	records, err := db.ListFileRecords(ctx, FileQuery{})
	if err != nil {
		t.Fatalf("ListFileRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	byPath := make(map[string]*FileRecord, len(records))
	for _, r := range records {
		byPath[r.File.Path] = r
	}

	a := byPath[withImage.Path]
	if a == nil || a.Image == nil || *a.Image.Width != 100 {
		t.Fatalf("expected %s to have an image, got %+v", withImage.Path, a)
	}
	if a.Origin == nil || a.Origin.Site == nil || *a.Origin.Site != "example.com" {
		t.Fatalf("expected %s to have an origin, got %+v", withImage.Path, a)
	}

	b := byPath[withoutImage.Path]
	if b == nil || b.Image != nil {
		t.Fatalf("expected %s to have no image, got %+v", withoutImage.Path, b)
	}
	if b.Origin != nil {
		t.Fatalf("expected %s to have no origin, got %+v", withoutImage.Path, b)
	}
}

func TestListFileRecordsRequireImage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, _ := seedFileRecordFixtures(t, ctx, db)

	records, err := db.ListFileRecords(ctx, FileQuery{RequireImage: true})
	if err != nil {
		t.Fatalf("ListFileRecords: %v", err)
	}
	if len(records) != 1 || records[0].File.Path != withImage.Path {
		t.Fatalf("expected only %s, got %+v", withImage.Path, records)
	}
}

func TestListFileRecordsMissingImage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	_, withoutImage := seedFileRecordFixtures(t, ctx, db)

	records, err := db.ListFileRecords(ctx, FileQuery{MissingImage: true})
	if err != nil {
		t.Fatalf("ListFileRecords: %v", err)
	}
	if len(records) != 1 || records[0].File.Path != withoutImage.Path {
		t.Fatalf("expected only %s, got %+v", withoutImage.Path, records)
	}
}

func TestListFileRecordsMutuallyExclusiveError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.ListFileRecords(ctx, FileQuery{RequireImage: true, MissingImage: true}); err == nil {
		t.Fatal("expected an error when both RequireImage and MissingImage are set")
	}
}

func TestListFileRecordsFilters(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, _ := seedFileRecordFixtures(t, ctx, db)

	byPrefix, err := db.ListFileRecords(ctx, FileQuery{PathPrefix: "/library/a"})
	if err != nil {
		t.Fatalf("ListFileRecords (prefix): %v", err)
	}
	if len(byPrefix) != 1 || byPrefix[0].File.Path != withImage.Path {
		t.Fatalf("expected prefix filter to match only %s, got %+v", withImage.Path, byPrefix)
	}

	byPath, err := db.ListFileRecords(ctx, FileQuery{Path: "a/photo"})
	if err != nil {
		t.Fatalf("ListFileRecords (path substring): %v", err)
	}
	if len(byPath) != 1 || byPath[0].File.Path != withImage.Path {
		t.Fatalf("expected path substring filter to match only %s, got %+v", withImage.Path, byPath)
	}

	bySite, err := db.ListFileRecords(ctx, FileQuery{Site: "EXAMPLE"})
	if err != nil {
		t.Fatalf("ListFileRecords (site): %v", err)
	}
	if len(bySite) != 1 || bySite[0].File.Path != withImage.Path {
		t.Fatalf("expected case-insensitive site filter to match only %s, got %+v", withImage.Path, bySite)
	}

	byTag, err := db.ListFileRecords(ctx, FileQuery{Tag: "beac", WithTags: true})
	if err != nil {
		t.Fatalf("ListFileRecords (tag): %v", err)
	}
	if len(byTag) != 1 || byTag[0].File.Path != withImage.Path {
		t.Fatalf("expected tag substring filter to match only %s, got %+v", withImage.Path, byTag)
	}
	if len(byTag[0].Tags) != 1 || byTag[0].Tags[0].Name != "beach" {
		t.Fatalf("expected WithTags to populate Tags, got %+v", byTag[0].Tags)
	}
}

func TestListFileRecordsAdvancedFilters(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	discovered := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	url := "https://example.com/photo.jpg"
	identifier := "listing-42"
	site := "example.com"
	origin := &Origin{Type: "web", Site: &site, URL: &url, Identifier: &identifier, DiscoveredAt: &discovered}
	if err := db.CreateOrigin(ctx, origin); err != nil {
		t.Fatalf("CreateOrigin: %v", err)
	}

	status := "processed"
	hash := "deadbeef"
	withImage := &File{Path: "/library/a/photo.jpg", Filename: "photo.jpg", Filesize: 1, OriginID: &origin.ID, Status: &status, Hash: &hash}
	if err := db.CreateFile(ctx, withImage); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	w, h := 1920, 1080
	if err := db.UpsertImage(ctx, &Image{FileID: withImage.ID, Width: &w, Height: &h}); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}

	other := &File{Path: "/library/b/other.jpg", Filename: "other.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, other); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	ow, oh := 100, 100
	if err := db.UpsertImage(ctx, &Image{FileID: other.ID, Width: &ow, Height: &oh}); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}

	cases := []struct {
		name  string
		query FileQuery
	}{
		{"url", FileQuery{URL: "example.com/photo"}},
		{"identifier", FileQuery{Identifier: "listing-42"}},
		{"status", FileQuery{Status: "process"}},
		{"hash", FileQuery{Hash: "DEADBEEF"}}, // exact match, case-insensitive
		{"width range", FileQuery{WidthMin: 1000, WidthMax: 2000}},
		{"height range min only", FileQuery{HeightMin: 1000}},
		{"discovered range", FileQuery{
			DiscoveredFrom: new(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			DiscoveredTo:   new(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)),
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			records, err := db.ListFileRecords(ctx, c.query)
			if err != nil {
				t.Fatalf("ListFileRecords(%s): %v", c.name, err)
			}
			if len(records) != 1 || records[0].File.Path != withImage.Path {
				t.Fatalf("ListFileRecords(%s) = %+v, want only %s", c.name, records, withImage.Path)
			}
		})
	}

	// A width filter that excludes everything (e.g. a lower bound above
	// every image's width) must return zero matches, not every file.
	none, err := db.ListFileRecords(ctx, FileQuery{WidthMin: 5000})
	if err != nil {
		t.Fatalf("ListFileRecords (width excludes all): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListFileRecords (width excludes all) = %+v, want none", none)
	}
}

func TestListFileRecordsFileIDs(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, _ := seedFileRecordFixtures(t, ctx, db)

	// FileIDs restricts to exactly the given IDs, overriding every other
	// field — Site here would otherwise match nothing, proving FileIDs
	// takes priority rather than being ANDed with it.
	records, err := db.ListFileRecords(ctx, FileQuery{FileIDs: []int64{withImage.ID}, Site: "nonexistent-site"})
	if err != nil {
		t.Fatalf("ListFileRecords (FileIDs): %v", err)
	}
	if len(records) != 1 || records[0].File.Path != withImage.Path {
		t.Fatalf("ListFileRecords (FileIDs) = %+v, want only %s", records, withImage.Path)
	}

	// A nil FileIDs (the zero value) means "no restriction": every file
	// matches, same as before this field existed.
	all, err := db.ListFileRecords(ctx, FileQuery{})
	if err != nil {
		t.Fatalf("ListFileRecords (nil FileIDs): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListFileRecords (nil FileIDs) = %+v, want both files", all)
	}

	// A non-nil but empty FileIDs means "match nothing" — the shape
	// QueryFileIDs returns when a raw SQL search found zero rows.
	none, err := db.ListFileRecords(ctx, FileQuery{FileIDs: []int64{}})
	if err != nil {
		t.Fatalf("ListFileRecords (empty FileIDs): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListFileRecords (empty FileIDs) = %+v, want none", none)
	}
}

// TestListFileRecordsFileIDsStillRespectsExcludeMissing is a regression
// test: FileIDs must not bypass ExcludeMissing/MissingImage, since callers
// (e.g. inventory-manager's browser) rely on those as structural
// invariants that hold regardless of which content filter is active —
// including a raw-SQL search resolved down to an explicit FileIDs list.
func TestListFileRecordsFileIDsStillRespectsExcludeMissing(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, _ := seedFileRecordFixtures(t, ctx, db)

	now := time.Now()
	withImage.MissingSince = &now
	if err := db.UpdateFile(ctx, withImage); err != nil {
		t.Fatalf("UpdateFile (mark missing): %v", err)
	}

	records, err := db.ListFileRecords(ctx, FileQuery{FileIDs: []int64{withImage.ID}, ExcludeMissing: true})
	if err != nil {
		t.Fatalf("ListFileRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("ListFileRecords (FileIDs + ExcludeMissing) = %+v, want none — FileIDs must not resurrect a missing file", records)
	}
}

func TestListImageResolutions(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	withImage, _ := seedFileRecordFixtures(t, ctx, db)

	groups, err := db.ListImageResolutions(ctx, "")
	if err != nil {
		t.Fatalf("ListImageResolutions: %v", err)
	}
	if len(groups) != 1 || groups[0].Path != withImage.Path || groups[0].Width != 100 || groups[0].Height != 200 {
		t.Fatalf("unexpected groups: %+v", groups)
	}

	none, err := db.ListImageResolutions(ctx, "/nowhere")
	if err != nil {
		t.Fatalf("ListImageResolutions (prefix miss): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no matches for an unrelated prefix, got %+v", none)
	}
}
