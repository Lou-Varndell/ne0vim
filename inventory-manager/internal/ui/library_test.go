package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	invlib "inv-lib"
)

func openTestDB(t *testing.T) *invlib.DB {
	t.Helper()
	db, err := invlib.Open(context.Background(), filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func createTestFile(t *testing.T, db *invlib.DB, path string) *invlib.File {
	t.Helper()
	ctx := context.Background()

	f := &invlib.File{Path: path, Filename: filepath.Base(path), Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile(%q): %v", path, err)
	}
	width, height := 800, 600
	if err := db.UpsertImage(ctx, &invlib.Image{FileID: f.ID, Width: &width, Height: &height}); err != nil {
		t.Fatalf("UpsertImage(%q): %v", path, err)
	}
	return f
}

func TestSearchRecordsEmptyFilterMatchesEverything(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	records, err := searchRecords(ctx, db, Filter{})
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("searchRecords(empty) = %d records, want 2", len(records))
	}
	if records[0].Path != "/photos/a.jpg" || records[1].Path != "/photos/b.jpg" {
		t.Fatalf("searchRecords(empty) order = %v, want a.jpg then b.jpg", records)
	}
	if records[0].Width != 800 || records[0].Height != 600 {
		t.Fatalf("searchRecords dimensions = %+v, want 800x600", records[0])
	}
}

func TestSearchRecordsBySite(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	if err := setSite(ctx, db, a.ID, "Example.com"); err != nil {
		t.Fatalf("setSite: %v", err)
	}

	records, err := searchRecords(ctx, db, Filter{Site: "example"})
	if err != nil {
		t.Fatalf("searchRecords(Site): %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/a.jpg" {
		t.Fatalf("searchRecords(Site) = %v, want [/photos/a.jpg]", records)
	}
	if records[0].Site != "Example.com" {
		t.Fatalf("Site = %q, want %q", records[0].Site, "Example.com")
	}
}

func TestSetSiteReusesOriginThenClears(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	f := createTestFile(t, db, "/photos/a.jpg")

	if err := setSite(ctx, db, f.ID, "example.com"); err != nil {
		t.Fatalf("setSite (create): %v", err)
	}
	got, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginID == nil {
		t.Fatal("setSite (create) did not attach an origin")
	}
	firstOriginID := *got.OriginID

	// Changing the site on an already-attached file must update the
	// existing origin in place, not create a second one.
	if err := setSite(ctx, db, f.ID, "other.example.com"); err != nil {
		t.Fatalf("setSite (update): %v", err)
	}
	got, err = db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginID == nil || *got.OriginID != firstOriginID {
		t.Fatalf("setSite (update) origin id = %v, want unchanged %d", got.OriginID, firstOriginID)
	}
	origin, err := db.GetOrigin(ctx, firstOriginID)
	if err != nil {
		t.Fatalf("GetOrigin: %v", err)
	}
	if origin.Site == nil || *origin.Site != "other.example.com" {
		t.Fatalf("origin.Site = %v, want other.example.com", origin.Site)
	}

	// Clearing the site detaches the origin without deleting it (another
	// file could reference the same origin row).
	if err := setSite(ctx, db, f.ID, ""); err != nil {
		t.Fatalf("setSite (clear): %v", err)
	}
	got, err = db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginID != nil {
		t.Fatalf("setSite (clear) origin id = %v, want nil", got.OriginID)
	}
}

func TestSetTagsDiffsAndNormalizes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	f := createTestFile(t, db, "/photos/a.jpg")

	if err := setTags(ctx, db, f.ID, []string{"Beach", " vacation ", "beach"}); err != nil {
		t.Fatalf("setTags: %v", err)
	}
	tags, err := db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	names := tagNames(tags)
	if strings.Join(names, ",") != "beach,vacation" {
		t.Fatalf("tags after first setTags = %v, want [beach vacation] (normalized, deduped)", names)
	}

	// Replacing with a different set must untag what's no longer present
	// and tag what's new, without erroring on the tag already shared.
	if err := setTags(ctx, db, f.ID, []string{"beach", "sunset"}); err != nil {
		t.Fatalf("setTags (replace): %v", err)
	}
	tags, err = db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	names = tagNames(tags)
	if strings.Join(names, ",") != "beach,sunset" {
		t.Fatalf("tags after replace = %v, want [beach sunset]", names)
	}

	// Clearing entirely must remove every tag association.
	if err := setTags(ctx, db, f.ID, nil); err != nil {
		t.Fatalf("setTags (clear): %v", err)
	}
	tags, err = db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("tags after clear = %v, want none", tags)
	}
}

func TestSearchRecordsByTagSubstring(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	if err := setTags(ctx, db, a.ID, []string{"vacation"}); err != nil {
		t.Fatalf("setTags: %v", err)
	}

	records, err := searchRecords(ctx, db, Filter{Tag: "vaca"})
	if err != nil {
		t.Fatalf("searchRecords(Tag): %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/a.jpg" {
		t.Fatalf("searchRecords(Tag) = %v, want [/photos/a.jpg]", records)
	}
	if strings.Join(records[0].Tags, ",") != "vacation" {
		t.Fatalf("Tags = %v, want [vacation]", records[0].Tags)
	}
}

func TestSearchRecordsExcludesDeletedFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	if err := setTags(ctx, db, a.ID, []string{"keep"}); err != nil {
		t.Fatalf("setTags: %v", err)
	}
	if err := db.DeleteFile(ctx, a.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	records, err := searchRecords(ctx, db, Filter{})
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/b.jpg" {
		t.Fatalf("searchRecords after delete = %v, want only [/photos/b.jpg]", records)
	}

	// A tag search for the deleted file's (cascade-removed) tag must not
	// resurrect it.
	records, err = searchRecords(ctx, db, Filter{Tag: "keep"})
	if err != nil {
		t.Fatalf("searchRecords(Tag) after delete: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("searchRecords(Tag) after delete = %v, want none", records)
	}
}

func tagNames(tags []*invlib.Tag) []string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names
}
