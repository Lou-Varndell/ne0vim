package invlib

import (
	"context"
	"testing"
)

func TestEnsureFile(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/ensure.jpg", Filename: "ensure.jpg", Filesize: 10}
	inserted, err := db.EnsureFile(ctx, f)
	if err != nil {
		t.Fatalf("EnsureFile (create): %v", err)
	}
	if !inserted {
		t.Fatal("expected EnsureFile to insert a new row")
	}
	if f.ID == 0 {
		t.Fatal("EnsureFile did not set an id")
	}
	firstID := f.ID

	again := &File{Path: "/photos/ensure.jpg", Filename: "ensure.jpg", Filesize: 999}
	inserted, err = db.EnsureFile(ctx, again)
	if err != nil {
		t.Fatalf("EnsureFile (existing): %v", err)
	}
	if inserted {
		t.Fatal("expected EnsureFile to report no insert for an existing path")
	}
	if again.ID != firstID {
		t.Fatalf("expected existing id %d, got %d", firstID, again.ID)
	}

	// The existing row's filesize must be untouched by the no-op path.
	got, err := db.GetFile(ctx, firstID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.Filesize != 10 {
		t.Fatalf("expected EnsureFile to leave existing row untouched, got filesize %d", got.Filesize)
	}
}

func TestEnsureOrigin(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	site := "example.com"
	identifier := "listing-1"
	o := &Origin{Type: "web", Site: &site, Identifier: &identifier}
	inserted, err := db.EnsureOrigin(ctx, o)
	if err != nil {
		t.Fatalf("EnsureOrigin (create): %v", err)
	}
	if !inserted {
		t.Fatal("expected EnsureOrigin to insert a new row")
	}
	firstID := o.ID

	again := &Origin{Type: "manifest", Site: &site, Identifier: &identifier}
	inserted, err = db.EnsureOrigin(ctx, again)
	if err != nil {
		t.Fatalf("EnsureOrigin (existing): %v", err)
	}
	if inserted {
		t.Fatal("expected EnsureOrigin to report no insert for an existing (site, identifier)")
	}
	if again.ID != firstID {
		t.Fatalf("expected existing id %d, got %d", firstID, again.ID)
	}

	got, err := db.GetOrigin(ctx, firstID)
	if err != nil {
		t.Fatalf("GetOrigin: %v", err)
	}
	if got.Type != "web" {
		t.Fatalf("expected EnsureOrigin to leave existing row untouched, got type %q", got.Type)
	}

	// Nil Site/Identifier never conflict with each other, so two origins
	// with nil fields are both inserted fresh.
	a := &Origin{Type: "fs"}
	b := &Origin{Type: "fs"}
	insA, err := db.EnsureOrigin(ctx, a)
	if err != nil {
		t.Fatalf("EnsureOrigin (nil a): %v", err)
	}
	insB, err := db.EnsureOrigin(ctx, b)
	if err != nil {
		t.Fatalf("EnsureOrigin (nil b): %v", err)
	}
	if !insA || !insB {
		t.Fatal("expected both nil-keyed origins to be inserted as new rows")
	}
	if a.ID == b.ID {
		t.Fatal("expected distinct ids for two nil-keyed origins")
	}
}

func TestInsertImageIfAbsent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/img.jpg", Filename: "img.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	w1, h1 := 100, 200
	inserted, err := db.InsertImageIfAbsent(ctx, &Image{FileID: f.ID, Width: &w1, Height: &h1})
	if err != nil {
		t.Fatalf("InsertImageIfAbsent (create): %v", err)
	}
	if !inserted {
		t.Fatal("expected first InsertImageIfAbsent to insert")
	}

	w2, h2 := 999, 999
	inserted, err = db.InsertImageIfAbsent(ctx, &Image{FileID: f.ID, Width: &w2, Height: &h2})
	if err != nil {
		t.Fatalf("InsertImageIfAbsent (skip): %v", err)
	}
	if inserted {
		t.Fatal("expected second InsertImageIfAbsent to skip an existing row")
	}

	got, err := db.GetImage(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if *got.Width != 100 || *got.Height != 200 {
		t.Fatalf("expected InsertImageIfAbsent to leave existing row untouched, got %+v", got)
	}
}

func TestCreateFilesTx(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	existing := &File{Path: "/photos/batch-existing.jpg", Filename: "batch-existing.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, existing); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	batch := []*File{
		{Path: "/photos/batch-existing.jpg", Filename: "batch-existing.jpg", Filesize: 999},
		{Path: "/photos/batch-new-1.jpg", Filename: "batch-new-1.jpg", Filesize: 2},
		{Path: "/photos/batch-new-2.jpg", Filename: "batch-new-2.jpg", Filesize: 3},
	}
	if err := db.CreateFilesTx(ctx, batch); err != nil {
		t.Fatalf("CreateFilesTx: %v", err)
	}

	if batch[0].ID != existing.ID {
		t.Fatalf("expected existing path to resolve to id %d, got %d", existing.ID, batch[0].ID)
	}
	if batch[1].ID == 0 || batch[2].ID == 0 {
		t.Fatalf("expected new files to get ids, got %+v", batch)
	}

	got, err := db.GetFile(ctx, existing.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.Filesize != 1 {
		t.Fatalf("expected CreateFilesTx to leave existing row untouched, got filesize %d", got.Filesize)
	}

	all, err := db.ListFiles(ctx)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 files total, got %d", len(all))
	}
}

func TestCreateFilesTxRollsBackOnError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	// A file referencing a non-existent origin_id violates the files.
	// origin_id foreign key (foreign_keys enforcement is on by DSN), giving
	// a genuine mid-transaction failure to verify the whole batch rolls
	// back rather than partially committing.
	missingOriginID := int64(99999)
	batch := []*File{
		{Path: "/photos/rollback-1.jpg", Filename: "rollback-1.jpg", Filesize: 1},
		{Path: "/photos/rollback-2.jpg", Filename: "rollback-2.jpg", Filesize: 1, OriginID: &missingOriginID},
	}
	if err := db.CreateFilesTx(ctx, batch); err == nil {
		t.Fatal("expected CreateFilesTx to fail on a foreign key violation")
	}

	if _, err := db.GetFileByPath(ctx, "/photos/rollback-1.jpg"); err == nil {
		t.Fatal("expected the first file to be rolled back along with the failing second one")
	}
}

func TestUpdateFilePath(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/old/path.jpg", Filename: "path.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	if err := db.UpdateFilePath(ctx, f.ID, "/new/path.jpg", "renamed.jpg"); err != nil {
		t.Fatalf("UpdateFilePath: %v", err)
	}

	got, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.Path != "/new/path.jpg" || got.Filename != "renamed.jpg" {
		t.Fatalf("UpdateFilePath did not persist: %+v", got)
	}
	if got.Filesize != 1 {
		t.Fatalf("UpdateFilePath must not touch other columns, got filesize %d", got.Filesize)
	}
}

func TestUpdateFileStatus(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/status.jpg", Filename: "status.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	status := "processed"
	if err := db.UpdateFileStatus(ctx, f.ID, &status, nil); err != nil {
		t.Fatalf("UpdateFileStatus: %v", err)
	}

	got, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.Status == nil || *got.Status != "processed" {
		t.Fatalf("UpdateFileStatus did not persist status: %+v", got)
	}
	if got.Error != nil {
		t.Fatalf("expected nil error to persist as NULL, got %+v", got.Error)
	}
}

func TestUpdateFileOrigin(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/origin.jpg", Filename: "origin.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	site := "example.com"
	o := &Origin{Type: "manual", Site: &site}
	if err := db.CreateOrigin(ctx, o); err != nil {
		t.Fatalf("CreateOrigin: %v", err)
	}

	if err := db.UpdateFileOrigin(ctx, f.ID, &o.ID); err != nil {
		t.Fatalf("UpdateFileOrigin (set): %v", err)
	}
	got, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginID == nil || *got.OriginID != o.ID {
		t.Fatalf("UpdateFileOrigin did not persist: %+v", got)
	}

	if err := db.UpdateFileOrigin(ctx, f.ID, nil); err != nil {
		t.Fatalf("UpdateFileOrigin (clear): %v", err)
	}
	got, err = db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.OriginID != nil {
		t.Fatalf("expected UpdateFileOrigin(nil) to clear origin_id, got %+v", got.OriginID)
	}
}

func TestSetFileTags(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/tags.jpg", Filename: "tags.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	if err := db.SetFileTags(ctx, f.ID, []string{"Beach", "Sunset"}); err != nil {
		t.Fatalf("SetFileTags (initial): %v", err)
	}
	tags, err := db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("expected 2 tags, got %+v", tags)
	}

	// Normalization applies via GetOrCreateTag, so "beach" (already present
	// as "beach" after normalization) plus a new "mountains" should result
	// in exactly beach+mountains, with sunset removed.
	if err := db.SetFileTags(ctx, f.ID, []string{"beach", "Mountains"}); err != nil {
		t.Fatalf("SetFileTags (reconcile): %v", err)
	}
	tags, err = db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	names := make(map[string]bool, len(tags))
	for _, t := range tags {
		names[t.Name] = true
	}
	if len(names) != 2 || !names["beach"] || !names["mountains"] {
		t.Fatalf("unexpected tag set after reconcile: %+v", tags)
	}

	if err := db.SetFileTags(ctx, f.ID, nil); err != nil {
		t.Fatalf("SetFileTags (clear): %v", err)
	}
	tags, err = db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected no tags after clearing, got %+v", tags)
	}
}
