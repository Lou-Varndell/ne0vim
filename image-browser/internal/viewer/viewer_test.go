package viewer

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"image-browser/internal/images"
	"image-browser/internal/manifest"
)

func writeTestPNG(t *testing.T, path string, size int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: 10, G: 200, B: 30, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestLoadThumbnailUsesDiskCacheWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.png")
	writeTestPNG(t, src, 400)

	cacheDir := t.TempDir()
	cache := images.NewCache(cacheDir, 100)

	img := loadThumbnail(cache, src, 100)
	if img == nil {
		t.Fatal("loadThumbnail() = nil, want a decoded thumbnail")
	}
	if bounds := img.Bounds(); bounds.Dx() > 100 || bounds.Dy() > 100 {
		t.Fatalf("loadThumbnail() size = %dx%d, want at most 100x100", bounds.Dx(), bounds.Dy())
	}

	hash, err := images.Hash(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, hash[:2], hash+".jpg")); err != nil {
		t.Fatalf("loadThumbnail() did not populate the disk cache: %v", err)
	}
}

func TestLoadThumbnailWithoutCacheStillResizes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src, 400)

	img := loadThumbnail(nil, src, 100)
	if img == nil {
		t.Fatal("loadThumbnail() = nil, want a decoded thumbnail")
	}
	if bounds := img.Bounds(); bounds.Dx() > 100 || bounds.Dy() > 100 {
		t.Fatalf("loadThumbnail() size = %dx%d, want at most 100x100", bounds.Dx(), bounds.Dy())
	}
}

func TestLoadThumbnailFallsBackWhenCacheCannotGenerate(t *testing.T) {
	src := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(src, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	cache := images.NewCache(t.TempDir(), 100)
	if img := loadThumbnail(cache, src, 100); img != nil {
		t.Fatalf("loadThumbnail() = %v, want nil for an undecodable source", img)
	}
}

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
	if result.Moved != 2 || result.Duplicates != 0 || len(result.Failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !reflect.DeepEqual(result.MovedPaths, []string{src1, src2}) {
		t.Fatalf("movedPaths = %v, want %v", result.MovedPaths, []string{src1, src2})
	}

	for _, name := range []string{"one.jpg", "two.jpg"} {
		if _, err := os.Stat(filepath.Join(destDir, name)); err != nil {
			t.Fatalf("moved file %s not found: %v", name, err)
		}
	}
}

func TestMoveAllImagesRemovesMovedEntryFromSiblingManifest(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "source")
	destDir := filepath.Join(dir, "dest")
	if err := os.Mkdir(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(srcDir, "one.jpg")
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestPath := filepath.Join(srcDir, "image-manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`[{"site":"s","source_url":"u","file":"one.jpg","status":"processed"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := moveAllImages([]string{src}, destDir)
	if result.Moved != 1 || len(result.Failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	// The moved entry is removed from the source manifest — a manifest
	// only describes images in its own directory tree, and the image just
	// moved out of it — rather than rewritten in place to point at destDir.
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var entries []manifest.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("manifest is no longer valid JSON: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want 0 (moved entry should be removed, not rewritten in place)", entries)
	}

	want := filepath.Join(destDir, "one.jpg")
	destManifest := filepath.Join(destDir, "image-manifest.json")
	raw, err = os.ReadFile(destManifest)
	if err != nil {
		t.Fatalf("destination manifest was not created: %v", err)
	}
	var destEntries []manifest.Entry
	if err := json.Unmarshal(raw, &destEntries); err != nil {
		t.Fatalf("destination manifest is not a valid entry array: %v", err)
	}
	if len(destEntries) != 1 || destEntries[0].File != want {
		t.Fatalf("destination manifest = %+v, want 1 entry with File %q", destEntries, want)
	}
}

// TestMoveAllImagesUpdatesNestedManifestsWithAbsoluteFilePaths reproduces a
// real-world layout: a root with per-listing subdirectories, each holding
// several images and one manifest.json array whose File values are already
// absolute paths (not bare filenames), discovered the same way `view` with
// no pattern does — via images.Find walking the whole root recursively.
func TestMoveAllImagesUpdatesNestedManifestsWithAbsoluteFilePaths(t *testing.T) {
	root := t.TempDir()
	destDir := t.TempDir()

	dir432 := filepath.Join(root, "mlsgrid_sync", "photos", "laksr.v2", "LBR166432")
	dir429 := filepath.Join(root, "mlsgrid_sync", "photos", "laksr.v2", "LBR166429")
	if err := os.MkdirAll(dir432, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir429, 0o755); err != nil {
		t.Fatal(err)
	}

	one432 := filepath.Join(dir432, "001.jpg")
	two432 := filepath.Join(dir432, "002.jpg")
	one429 := filepath.Join(dir429, "003.jpg")
	// Content must differ per file: ResolveDestination's duplicate detection
	// is by size+hash, so identical fixtures would make it correctly treat
	// the 2nd and 3rd files as duplicates of the 1st and skip the move.
	if err := os.WriteFile(one432, []byte("432-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(two432, []byte("432-two"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(one429, []byte("429-one"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifest432 := filepath.Join(dir432, "manifest.json")
	entries432 := []manifest.Entry{
		{Site: "s", SourceURL: "u1", File: one432, Status: "processed", DiscoveredAt: time.Now()},
		{Site: "s", SourceURL: "u2", File: two432, Status: "processed", DiscoveredAt: time.Now()},
	}
	raw432, err := json.Marshal(entries432)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest432, raw432, 0o644); err != nil {
		t.Fatal(err)
	}

	manifest429 := filepath.Join(dir429, "manifest.json")
	entries429 := []manifest.Entry{
		{Site: "s", SourceURL: "u3", File: one429, Status: "processed", DiscoveredAt: time.Now()},
	}
	raw429, err := json.Marshal(entries429)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest429, raw429, 0o644); err != nil {
		t.Fatal(err)
	}

	// Mirrors how `view -root <root>` with no pattern discovers matches:
	// images.Find(root, "") walks the whole tree.
	matches, err := images.Find(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 3 {
		t.Fatalf("images.Find found %d images, want 3: %v", len(matches), matches)
	}

	result := moveAllImages(matches, destDir)
	if len(result.Failures) != 0 {
		t.Fatalf("moveAllImages failures: %v", result.Failures)
	}
	if result.Moved != 3 {
		t.Fatalf("result.Moved = %d, want 3", result.Moved)
	}

	// Every entry in both source manifests matched the move, so each
	// manifest is left with its moved entries removed entirely — a
	// manifest only describes images in its own directory tree — rather
	// than rewritten in place to point at destDir. The destEntries check
	// below confirms they landed correctly instead.
	assertManifestEmptied := func(manifestPath string) {
		t.Helper()
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("reading %s: %v", manifestPath, err)
		}
		var entries []manifest.Entry
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s is no longer a valid entry array: %v", manifestPath, err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s = %+v, want 0 (moved entries should be removed, not rewritten in place)", manifestPath, entries)
		}
	}

	assertManifestEmptied(manifest432)
	assertManifestEmptied(manifest429)

	destManifest := filepath.Join(destDir, "image-manifest.json")
	raw, err := os.ReadFile(destManifest)
	if err != nil {
		t.Fatalf("destination manifest was not created: %v", err)
	}
	var destEntries []manifest.Entry
	if err := json.Unmarshal(raw, &destEntries); err != nil {
		t.Fatalf("destination manifest is not a valid entry array: %v", err)
	}
	if len(destEntries) != 3 {
		t.Fatalf("len(destEntries) = %d, want 3", len(destEntries))
	}
}

func TestRemoveImagesDropsMovedPathsAndClampsIndex(t *testing.T) {
	b := &Browser{
		images:     []string{"/a.jpg", "/b.jpg", "/c.jpg"},
		index:      2,
		thumbCache: newImageCache(4),
		fullCache:  newImageCache(4),
	}

	b.removeImages([]string{"/b.jpg", "/c.jpg"})

	want := []string{"/a.jpg"}
	if !reflect.DeepEqual(b.images, want) {
		t.Fatalf("images = %v, want %v", b.images, want)
	}
	if b.index != 0 {
		t.Fatalf("index = %d, want 0", b.index)
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
	if result.Moved != 0 || result.Duplicates != 1 || len(result.Failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("duplicate source should remain: %v", err)
	}
}
