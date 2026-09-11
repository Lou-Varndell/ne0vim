package viewer

import (
	"image"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestImageCacheEvictsOldestOnceFull(t *testing.T) {
	c := newImageCache(2)

	a := image.NewRGBA(image.Rect(0, 0, 1, 1))
	b := image.NewRGBA(image.Rect(0, 0, 1, 1))
	d := image.NewRGBA(image.Rect(0, 0, 1, 1))

	c.Put("a", a)
	c.Put("b", b)
	c.Put("d", d) // over capacity: evicts "a", the oldest insertion

	if _, ok := c.Get("a"); ok {
		t.Fatal("Get(a) = found, want evicted")
	}
	if got, ok := c.Get("b"); !ok || got != b {
		t.Fatalf("Get(b) = %v, %v, want %v, true", got, ok, b)
	}
	if got, ok := c.Get("d"); !ok || got != d {
		t.Fatalf("Get(d) = %v, %v, want %v, true", got, ok, d)
	}
}

func TestImageCachePutIgnoresExistingKey(t *testing.T) {
	c := newImageCache(2)

	a := image.NewRGBA(image.Rect(0, 0, 1, 1))
	a2 := image.NewRGBA(image.Rect(0, 0, 2, 2))

	c.Put("a", a)
	c.Put("a", a2) // key already cached: Put is a no-op, order is unaffected

	got, ok := c.Get("a")
	if !ok || got != a {
		t.Fatalf("Get(a) = %v, %v, want %v, true (first value retained)", got, ok, a)
	}
}

func TestImageCacheDelete(t *testing.T) {
	c := newImageCache(2)
	c.Put("a", image.NewRGBA(image.Rect(0, 0, 1, 1)))

	c.Delete("a")

	if _, ok := c.Get("a"); ok {
		t.Fatal("Get(a) = found after Delete, want not found")
	}
}

func TestGridPathsUngroupedPreservesOrder(t *testing.T) {
	b := &Browser{images: []string{"/z/two.jpg", "/a/one.jpg"}}

	got := b.gridPaths()

	want := []string{"/z/two.jpg", "/a/one.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gridPaths() = %v, want %v", got, want)
	}
}

func TestGridPathsGroupedSortsByDirectoryThenName(t *testing.T) {
	b := &Browser{
		groupDirs: true,
		images: []string{
			"/b/two.jpg",
			"/a/two.jpg",
			"/a/one.jpg",
		},
	}

	got := b.gridPaths()

	want := []string{"/a/one.jpg", "/a/two.jpg", "/b/two.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gridPaths() = %v, want %v", got, want)
	}
}

func TestGridPathsDoesNotMutateBrowserImages(t *testing.T) {
	original := []string{"/b/two.jpg", "/a/one.jpg"}
	b := &Browser{groupDirs: true, images: original}

	b.gridPaths()

	want := []string{"/b/two.jpg", "/a/one.jpg"}
	if !reflect.DeepEqual(b.images, want) {
		t.Fatalf("b.images = %v, want unchanged %v", b.images, want)
	}
}

func TestMoveAllImages(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	src1 := filepath.Join(srcDir, "one.jpg")
	src2 := filepath.Join(srcDir, "two.jpg")
	if err := os.WriteFile(src1, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := moveAllImages([]string{src1, src2}, destDir)
	if result.moved != 2 || result.duplicates != 0 || len(result.failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	for _, name := range []string{"one.jpg", "two.jpg"} {
		if _, err := os.Stat(filepath.Join(destDir, name)); err != nil {
			t.Fatalf("moved file %s not found: %v", name, err)
		}
	}
}

func TestMoveAllImagesSkipsDuplicates(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(srcDir, "source.jpg")
	if err := os.WriteFile(src, []byte("same image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destDir, "already-there.jpg"), []byte("same image"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := moveAllImages([]string{src}, destDir)
	if result.moved != 0 || result.duplicates != 1 || len(result.failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("duplicate source should remain: %v", err)
	}
}
