package catalog

import (
	"fmt"
	"testing"

	"media-site/internal/catalogdb"
)

// seedImages inserts n images directly into dir, bypassing a filesystem
// scan since loadImageBatch only ever reads from the index.
func seedImages(t *testing.T, db *catalogdb.DB, n int) {
	t.Helper()
	for i := range n {
		name := fmt.Sprintf("img%04d.png", i)
		if err := db.InsertImage(name, name, ""); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}
}

// TestLoadImageBatch_HasMoreBoundary verifies the batch boundary logic
// around exactly ImagePageSize images: the extra probe row loadImageBatch
// requests must flip HasMore without leaking into the rendered batch.
func TestLoadImageBatch_HasMoreBoundary(t *testing.T) {
	t.Run("fewer than a full page", func(t *testing.T) {
		db := newTestDB(t)
		seedImages(t, db, ImagePageSize-1)

		batch, err := loadImageBatch(db, "", 0)
		if err != nil {
			t.Fatalf("loadImageBatch: %v", err)
		}
		if batch.HasMore {
			t.Error("HasMore = true, want false (fewer images than page size)")
		}
		if len(batch.Images) != ImagePageSize-1 {
			t.Errorf("len(Images) = %d, want %d", len(batch.Images), ImagePageSize-1)
		}
		if batch.NextOffset != ImagePageSize-1 {
			t.Errorf("NextOffset = %d, want %d", batch.NextOffset, ImagePageSize-1)
		}
	})

	t.Run("exactly a full page", func(t *testing.T) {
		db := newTestDB(t)
		seedImages(t, db, ImagePageSize)

		batch, err := loadImageBatch(db, "", 0)
		if err != nil {
			t.Fatalf("loadImageBatch: %v", err)
		}
		if batch.HasMore {
			t.Error("HasMore = true, want false (exactly one page of images)")
		}
		if len(batch.Images) != ImagePageSize {
			t.Errorf("len(Images) = %d, want %d", len(batch.Images), ImagePageSize)
		}
	})

	t.Run("one more than a full page", func(t *testing.T) {
		db := newTestDB(t)
		seedImages(t, db, ImagePageSize+1)

		first, err := loadImageBatch(db, "", 0)
		if err != nil {
			t.Fatalf("loadImageBatch first: %v", err)
		}
		if !first.HasMore {
			t.Error("HasMore = false, want true (one image beyond the first page)")
		}
		if len(first.Images) != ImagePageSize {
			t.Errorf("len(Images) = %d, want %d (overflow row must not leak into the batch)", len(first.Images), ImagePageSize)
		}
		if first.NextOffset != ImagePageSize {
			t.Errorf("NextOffset = %d, want %d", first.NextOffset, ImagePageSize)
		}

		second, err := loadImageBatch(db, "", first.NextOffset)
		if err != nil {
			t.Fatalf("loadImageBatch second: %v", err)
		}
		if second.HasMore {
			t.Error("HasMore = true on the final batch, want false")
		}
		if len(second.Images) != 1 {
			t.Errorf("len(Images) = %d, want 1 (the overflow row)", len(second.Images))
		}
	})
}
