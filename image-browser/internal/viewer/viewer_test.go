package viewer

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	invlib "inv-lib"

	"image-browser/internal/images"
)

// TestMain points every test's inv.db writes at a throwaway temp file,
// instead of the real, shared ~/.config/inventory-manager/db/inv.db that
// invlib.DefaultPath would otherwise resolve to (see moveAllImages, which
// opens it on every call).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "image-browser-viewer-test-db")
	if err != nil {
		panic(err)
	}
	os.Setenv("INVENTORY_MANAGER_DB", filepath.Join(dir, "inv.db"))

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// openTestStore opens the shared test database at its current
// (TestMain-set) path, for a test to seed rows into or assert against.
func openTestStore(t *testing.T) *invlib.DB {
	t.Helper()
	path, err := invlib.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	database, err := invlib.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func recordByPath(t *testing.T, inv *invlib.DB, path string) (*invlib.FileRecord, bool) {
	t.Helper()
	all, err := inv.ListFileRecords(context.Background(), invlib.FileQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.File.Path == path {
			return r, true
		}
	}
	return nil, false
}

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
	inv := openTestStore(t)
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

	result := moveAllImages(context.Background(), inv, []string{src1, src2}, destDir)
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

func TestMoveAllImagesUpdatesDatabaseRecord(t *testing.T) {
	inv := openTestStore(t)
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

	extension := ".jpg"
	if err := inv.CreateFile(context.Background(), &invlib.File{
		Path: src, Filename: "one.jpg", Extension: &extension, Filesize: 3,
	}); err != nil {
		t.Fatal(err)
	}

	result := moveAllImages(context.Background(), inv, []string{src}, destDir)
	if result.Moved != 1 || len(result.Failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}

	// The record's path is updated in place to its new, post-move
	// location — there is no sidecar manifest.json to keep in sync.
	want := filepath.Join(destDir, "one.jpg")
	if _, ok := recordByPath(t, inv, src); ok {
		t.Fatalf("expected no record left at the old path %q", src)
	}
	got, ok := recordByPath(t, inv, want)
	if !ok {
		t.Fatalf("expected a record at the new path %q", want)
	}
	if got.File.Filename != "one.jpg" {
		t.Fatalf("record.Filename = %q, want %q", got.File.Filename, "one.jpg")
	}
}

// TestMoveAllImagesUpdatesMultipleDatabaseRecords reproduces a real-world
// layout: a root with per-listing subdirectories, each holding several
// images already recorded in the database, discovered the same way `view`
// with no pattern does — via images.Find walking the whole root
// recursively.
func TestMoveAllImagesUpdatesMultipleDatabaseRecords(t *testing.T) {
	inv := openTestStore(t)
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

	ctx := context.Background()
	extension := ".jpg"
	for _, path := range []string{one432, two432, one429} {
		if err := inv.CreateFile(ctx, &invlib.File{
			Path: path, Filename: filepath.Base(path), Extension: &extension, Filesize: 7,
		}); err != nil {
			t.Fatal(err)
		}
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

	result := moveAllImages(ctx, inv, matches, destDir)
	if len(result.Failures) != 0 {
		t.Fatalf("moveAllImages failures: %v", result.Failures)
	}
	if result.Moved != 3 {
		t.Fatalf("result.Moved = %d, want 3", result.Moved)
	}

	for _, src := range []string{one432, two432, one429} {
		if _, ok := recordByPath(t, inv, src); ok {
			t.Fatalf("expected no record left at the old path %q", src)
		}
		want := filepath.Join(destDir, filepath.Base(src))
		if _, ok := recordByPath(t, inv, want); !ok {
			t.Fatalf("expected a record at the new path %q", want)
		}
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
	inv := openTestStore(t)
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

	result := moveAllImages(context.Background(), inv, []string{src}, destDir)
	if result.Moved != 0 || result.Duplicates != 1 || len(result.Failures) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("duplicate source should remain: %v", err)
	}
}
