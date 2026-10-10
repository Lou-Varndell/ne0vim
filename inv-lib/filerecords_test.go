package invlib

import (
	"context"
	"testing"
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
