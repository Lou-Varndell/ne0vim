package catalog

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"media-site/internal/catalogmodel"
	"media-site/internal/store"
)

// seedImages inserts n images directly at root's top level, bypassing a
// filesystem scan since loadImageBatch only ever reads from the index. The
// paths are never written to disk — the index is all loadImageBatch reads.
func seedImages(t *testing.T, st *store.Store, root string, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		name := fmt.Sprintf("img%04d.png", i)
		id, _, err := st.AddFile(ctx, store.File{
			Path:     filepath.Join(root, name),
			Filename: name,
		})
		if err != nil {
			t.Fatalf("add file %s: %v", name, err)
		}
		if _, err := st.AddImage(ctx, store.Image{FileID: id, Width: 1, Height: 1, Format: "png"}); err != nil {
			t.Fatalf("add image %s: %v", name, err)
		}
	}
}

// TestLoadImageBatch_HasMoreBoundary verifies the batch boundary logic
// around exactly ImagePageSize images: the extra probe row loadImageBatch
// requests must flip HasMore without leaking into the rendered batch.
func TestLoadImageBatch_HasMoreBoundary(t *testing.T) {
	t.Run("fewer than a full page", func(t *testing.T) {
		root := t.TempDir()
		st := newTestStore(t)
		seedImages(t, st, root, ImagePageSize-1)

		ctx := context.Background()
		batch, err := loadImageBatch(ctx, st, root, "", 0)
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
		root := t.TempDir()
		st := newTestStore(t)
		seedImages(t, st, root, ImagePageSize)

		ctx := context.Background()
		batch, err := loadImageBatch(ctx, st, root, "", 0)
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
		root := t.TempDir()
		st := newTestStore(t)
		seedImages(t, st, root, ImagePageSize+1)

		ctx := context.Background()
		first, err := loadImageBatch(ctx, st, root, "", 0)
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

		second, err := loadImageBatch(ctx, st, root, "", first.NextOffset)
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

// TestLoadCatalogPage_FilterSearchesSubtree verifies that applying a filter
// (as the flyout panel does) searches the current directory and everything
// beneath it, not just its direct children — matching the aggregated scope
// LoadFlyoutData advertises as pickable.
func TestLoadCatalogPage_FilterSearchesSubtree(t *testing.T) {
	root := t.TempDir()

	st := newTestStore(t)
	ctx := context.Background()
	seedImage(t, st, root, "top.png", 10, 10)
	seedImage(t, st, root, "sub/nested.png", 10, 10)

	page, err := LoadCatalogPage(ctx, st, root, "", catalogmodel.FileFilter{})
	if err != nil {
		t.Fatalf("LoadCatalogPage (unfiltered): %v", err)
	}
	if len(page.Images) != 1 {
		t.Fatalf("unfiltered root images = %+v, want just top.png (direct children only)", page.Images)
	}

	filtered, err := LoadCatalogPage(ctx, st, root, "", catalogmodel.FileFilter{WidthMin: 1})
	if err != nil {
		t.Fatalf("LoadCatalogPage (filtered): %v", err)
	}
	if len(filtered.Images) != 2 {
		t.Errorf("filtered root images = %+v, want both top.png and sub/nested.png", filtered.Images)
	}
	if filtered.Filter.WidthMin != 1 {
		t.Errorf("Filter echoed back = %+v, want WidthMin 1", filtered.Filter)
	}
}
