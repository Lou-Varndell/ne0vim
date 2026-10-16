package ui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	records, total, err := searchRecords(ctx, db, Filter{}, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("searchRecords(empty) = %d records, want 2", len(records))
	}
	if total != 2 {
		t.Fatalf("searchRecords(empty) total = %d, want 2", total)
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

	records, _, err := searchRecords(ctx, db, Filter{Site: "example"}, 0, 0)
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

func TestAddTagsToFilesPreservesExistingTags(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := createTestFile(t, db, "/photos/a.jpg")
	b := createTestFile(t, db, "/photos/b.jpg")

	if err := setTags(ctx, db, a.ID, []string{"beach"}); err != nil {
		t.Fatalf("setTags: %v", err)
	}

	if err := addTagsToFiles(ctx, db, []int64{a.ID, b.ID}, []string{"Vacation", "beach"}); err != nil {
		t.Fatalf("addTagsToFiles: %v", err)
	}

	aTags, err := db.ListTagsForFile(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile(a): %v", err)
	}
	// a already had "beach" — addTagsToFiles must not duplicate it, and
	// must add the new "vacation" tag alongside it rather than replacing it.
	if strings.Join(tagNames(aTags), ",") != "beach,vacation" {
		t.Fatalf("a tags = %v, want [beach vacation]", tagNames(aTags))
	}

	bTags, err := db.ListTagsForFile(ctx, b.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile(b): %v", err)
	}
	if strings.Join(tagNames(bTags), ",") != "beach,vacation" {
		t.Fatalf("b tags = %v, want [beach vacation]", tagNames(bTags))
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

	records, _, err := searchRecords(ctx, db, Filter{Tag: "vaca"}, 0, 0)
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

func TestSearchRecordsByPathSubstring(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	createTestFile(t, db, "/photos/vacation/beach.jpg")
	createTestFile(t, db, "/photos/work/office.jpg")

	// "vacation" is not a leading prefix of either path, so this only
	// matches if Filter.Path is a substring search, as its "Path
	// contains…" placeholder in browser.go promises.
	records, _, err := searchRecords(ctx, db, Filter{Path: "vacation"}, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords(Path): %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/vacation/beach.jpg" {
		t.Fatalf("searchRecords(Path) = %v, want [/photos/vacation/beach.jpg]", records)
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

	records, _, err := searchRecords(ctx, db, Filter{}, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/b.jpg" {
		t.Fatalf("searchRecords after delete = %v, want only [/photos/b.jpg]", records)
	}

	// A tag search for the deleted file's (cascade-removed) tag must not
	// resurrect it.
	records, _, err = searchRecords(ctx, db, Filter{Tag: "keep"}, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords(Tag) after delete: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("searchRecords(Tag) after delete = %v, want none", records)
	}
}

func TestSearchRecordsExcludesMissingFile(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	a := createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	now := time.Now()
	a.MissingSince = &now
	if err := db.UpdateFile(ctx, a); err != nil {
		t.Fatalf("UpdateFile (mark missing): %v", err)
	}

	records, total, err := searchRecords(ctx, db, Filter{}, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 1 || records[0].Path != "/photos/b.jpg" {
		t.Fatalf("searchRecords with a missing file = %v, want only [/photos/b.jpg]", records)
	}
	if total != 1 {
		t.Fatalf("total = %d, want 1", total)
	}
}

func tagNames(tags []*invlib.Tag) []string {
	names := make([]string, len(tags))
	for i, t := range tags {
		names[i] = t.Name
	}
	return names
}

func TestSearchRecordsPagination(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	paths := []string{"/photos/a.jpg", "/photos/b.jpg", "/photos/c.jpg"}
	for _, p := range paths {
		createTestFile(t, db, p)
	}

	page1, total, err := searchRecords(ctx, db, Filter{}, 2, 0)
	if err != nil {
		t.Fatalf("searchRecords (page1): %v", err)
	}
	if len(page1) != 2 || page1[0].Path != paths[0] || page1[1].Path != paths[1] {
		t.Fatalf("page1 = %v, want first 2 paths", page1)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3 (ignoring the limit)", total)
	}

	page2, total, err := searchRecords(ctx, db, Filter{}, 2, 2)
	if err != nil {
		t.Fatalf("searchRecords (page2): %v", err)
	}
	if len(page2) != 1 || page2[0].Path != paths[2] {
		t.Fatalf("page2 = %v, want only %s", page2, paths[2])
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
}
