package invlib

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesSchemaAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "inv.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	db.Close()

	// Re-opening an already-migrated database must not error, since
	// schema.sql guards every CREATE with IF NOT EXISTS.
	db2, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	db2.Close()
}

func TestFileCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/a.jpg", Filename: "a.jpg", Filesize: 1234}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("CreateFile did not set an id")
	}
	if f.ScannedAt.IsZero() {
		t.Fatal("CreateFile did not backfill scanned_at")
	}

	got, err := db.GetFileByPath(ctx, f.Path)
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got.ID != f.ID || got.Filesize != 1234 {
		t.Fatalf("GetFileByPath mismatch: %+v", got)
	}

	hash := "deadbeef"
	got.Hash = &hash
	if err := db.UpdateFile(ctx, got); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}

	again, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if again.Hash == nil || *again.Hash != hash {
		t.Fatalf("UpdateFile did not persist hash: %+v", again)
	}

	if err := db.DeleteFile(ctx, f.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := db.GetFile(ctx, f.ID); err == nil {
		t.Fatal("expected sql.ErrNoRows after DeleteFile")
	}
}

func TestImageUpsertAndCascadeDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/b.jpg", Filename: "b.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	width, height := 800, 600
	img := &Image{FileID: f.ID, Width: &width, Height: &height}
	if err := db.UpsertImage(ctx, img); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}

	got, err := db.GetImage(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if *got.Width != 800 || *got.Height != 600 {
		t.Fatalf("GetImage mismatch: %+v", got)
	}

	// Deleting the parent file must cascade to its images row.
	if err := db.DeleteFile(ctx, f.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if _, err := db.GetImage(ctx, f.ID); err == nil {
		t.Fatal("expected images row to be cascade-deleted with its file")
	}
}

func TestOriginCRUD(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	site := "example.com"
	identifier := "listing-123"
	o := &Origin{Type: "scrape", Site: &site, Identifier: &identifier}
	if err := db.CreateOrigin(ctx, o); err != nil {
		t.Fatalf("CreateOrigin: %v", err)
	}

	got, err := db.GetOriginBySiteIdentifier(ctx, site, identifier)
	if err != nil {
		t.Fatalf("GetOriginBySiteIdentifier: %v", err)
	}
	if got.ID != o.ID {
		t.Fatalf("GetOriginBySiteIdentifier mismatch: %+v", got)
	}

	got.Type = "manifest"
	if err := db.UpdateOrigin(ctx, got); err != nil {
		t.Fatalf("UpdateOrigin: %v", err)
	}
	again, err := db.GetOrigin(ctx, o.ID)
	if err != nil {
		t.Fatalf("GetOrigin: %v", err)
	}
	if again.Type != "manifest" {
		t.Fatalf("UpdateOrigin did not persist: %+v", again)
	}
}

func TestFileTagsJunctionWithoutNormalization(t *testing.T) {
	// Exercises TagFile/UntagFile/ListTagsForFile/ListFilesForTag directly,
	// bypassing GetOrCreateTag since normalizeTagName is left for you to
	// implement in tags.go.
	ctx := context.Background()
	db := openTestDB(t)

	f := &File{Path: "/photos/c.jpg", Filename: "c.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	res, err := db.ExecContext(ctx, `INSERT INTO tags (name) VALUES ('beach')`)
	if err != nil {
		t.Fatalf("insert tag: %v", err)
	}
	tagID, _ := res.LastInsertId()

	if err := db.TagFile(ctx, f.ID, tagID); err != nil {
		t.Fatalf("TagFile: %v", err)
	}
	// Re-tagging must be idempotent.
	if err := db.TagFile(ctx, f.ID, tagID); err != nil {
		t.Fatalf("TagFile (repeat): %v", err)
	}

	tags, err := db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "beach" {
		t.Fatalf("ListTagsForFile mismatch: %+v", tags)
	}

	files, err := db.ListFilesForTag(ctx, tagID)
	if err != nil {
		t.Fatalf("ListFilesForTag: %v", err)
	}
	if len(files) != 1 || files[0].ID != f.ID {
		t.Fatalf("ListFilesForTag mismatch: %+v", files)
	}

	if err := db.UntagFile(ctx, f.ID, tagID); err != nil {
		t.Fatalf("UntagFile: %v", err)
	}
	tags, err = db.ListTagsForFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("ListTagsForFile after untag: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected no tags after UntagFile, got %+v", tags)
	}
}

func TestTimeRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	now := time.Now().UTC().Truncate(time.Second)
	f := &File{Path: "/photos/d.jpg", Filename: "d.jpg", Filesize: 1, CreatedAt: &now}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	got, err := db.GetFile(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetFile: %v", err)
	}
	if got.CreatedAt == nil || !got.CreatedAt.Equal(now) {
		t.Fatalf("time round-trip mismatch: got %v want %v", got.CreatedAt, now)
	}
}
