package process

import (
	"context"
	"database/sql"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"image-browser/internal/db"
	"image-browser/internal/store"
)

// TestMain points every test's inv.db writes at a throwaway temp file,
// instead of the real, shared ~/.config/inventory-manager/db/inv.db that
// db.DefaultPath would otherwise resolve to (see Run, which opens it via
// db.DefaultPath). Individual tests that need full isolation from each
// other additionally call newTestStore, which opens its own temp db
// directly rather than relying on this shared default.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "image-browser-process-test-db")
	if err != nil {
		panic(err)
	}
	os.Setenv("INVENTORY_MANAGER_DB", filepath.Join(dir, "inv.db"))

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// newTestStore opens a fresh, isolated inventory database for the
// duration of t.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return store.New(database)
}

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// addUnprocessedFile inserts a files row with no corresponding images row,
// the shape processImages looks for via store.UnprocessedImages.
func addUnprocessedFile(t *testing.T, inv *store.Store, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := inv.AddFile(context.Background(), store.File{
		Path:       path,
		Filename:   filepath.Base(path),
		Extension:  filepath.Ext(path),
		Filesize:   info.Size(),
		ModifiedAt: sql.NullTime{Time: info.ModTime(), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func fileRecord(t *testing.T, inv *store.Store, path string) store.FileRecord {
	t.Helper()
	all, err := inv.AllFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.Path == path {
			return r
		}
	}
	t.Fatalf("no files row for %s", path)
	return store.FileRecord{}
}

func TestIsImageFile(t *testing.T) {
	for _, path := range []string{"image.jpg", "image.JPEG", "image.png", "image.gif", "image.webp"} {
		if !isImageFile(path) {
			t.Errorf("isImageFile(%q) = false, want true", path)
		}
	}

	for _, path := range []string{"image.svg", "image.avif", "image.php", "image", ""} {
		if isImageFile(path) {
			t.Errorf("isImageFile(%q) = true, want false", path)
		}
	}
}

func TestUnderRoot(t *testing.T) {
	root := "/images/gallery"
	cases := []struct {
		path string
		want bool
	}{
		{"/images/gallery", true},
		{"/images/gallery/photo.jpg", true},
		{"/images/gallery/sub/photo.jpg", true},
		{"/images/galleryx/photo.jpg", false},
		{"/images/other/photo.jpg", false},
	}
	for _, c := range cases {
		if got := underRoot(c.path, root); got != c.want {
			t.Errorf("underRoot(%q, %q) = %v, want %v", c.path, root, got, c.want)
		}
	}
}

func TestProcessImagesFillsInDimensionsForUnprocessedFiles(t *testing.T) {
	inv := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	writePNG(t, imagePath, 17, 29)
	addUnprocessedFile(t, inv, imagePath)

	if err := processImages(ctx, inv, "", 2); err != nil {
		t.Fatal(err)
	}

	got := fileRecord(t, inv, imagePath)
	if got.Width != 17 || got.Height != 29 {
		t.Fatalf("record = %+v, want 17x29", got)
	}
	if got.Status != "processed" {
		t.Fatalf("status = %q, want processed", got.Status)
	}
}

func TestProcessImagesMarksFailedOnDecodeError(t *testing.T) {
	inv := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	badPath := filepath.Join(dir, "corrupt.jpg")
	if err := os.WriteFile(badPath, []byte("not actually an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	addUnprocessedFile(t, inv, badPath)

	if err := processImages(ctx, inv, "", 2); err != nil {
		t.Fatal(err)
	}

	got := fileRecord(t, inv, badPath)
	if got.Status != "failed" {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Error == "" {
		t.Fatal("expected a non-empty error message")
	}
	if got.Width != 0 || got.Height != 0 {
		t.Fatalf("record = %+v, want no dimensions recorded", got)
	}
}

func TestProcessImagesSkipsNonImageExtensions(t *testing.T) {
	inv := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	textPath := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	addUnprocessedFile(t, inv, textPath)

	if err := processImages(ctx, inv, "", 2); err != nil {
		t.Fatal(err)
	}

	got := fileRecord(t, inv, textPath)
	if got.Status != "" {
		t.Fatalf("status = %q, want untouched (empty)", got.Status)
	}
	if got.Width != 0 || got.Height != 0 {
		t.Fatalf("record = %+v, want no dimensions recorded", got)
	}
}

func TestProcessImagesRespectsRootFilter(t *testing.T) {
	inv := newTestStore(t)
	ctx := context.Background()

	rootA := filepath.Join(t.TempDir(), "a")
	rootB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(rootA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(rootB, 0o755); err != nil {
		t.Fatal(err)
	}

	imageA := filepath.Join(rootA, "image.png")
	imageB := filepath.Join(rootB, "image.png")
	writePNG(t, imageA, 11, 22)
	writePNG(t, imageB, 33, 44)
	addUnprocessedFile(t, inv, imageA)
	addUnprocessedFile(t, inv, imageB)

	if err := processImages(ctx, inv, rootA, 2); err != nil {
		t.Fatal(err)
	}

	gotA := fileRecord(t, inv, imageA)
	if gotA.Status != "processed" || gotA.Width != 11 || gotA.Height != 22 {
		t.Fatalf("rootA record = %+v, want processed 11x22", gotA)
	}

	gotB := fileRecord(t, inv, imageB)
	if gotB.Status != "" {
		t.Fatalf("rootB record = %+v, want untouched (outside -root)", gotB)
	}
}

func TestRunProcessesEveryUnprocessedFileInDatabase(t *testing.T) {
	dbPath, err := db.DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	inv := store.New(database)

	dir := t.TempDir()
	imagePath := filepath.Join(dir, "run-image.png")
	writePNG(t, imagePath, 5, 6)
	addUnprocessedFile(t, inv, imagePath)
	database.Close()

	if err := Run(nil); err != nil {
		t.Fatal(err)
	}

	database, err = db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	inv = store.New(database)

	got := fileRecord(t, inv, imagePath)
	if got.Status != "processed" || got.Width != 5 || got.Height != 6 {
		t.Fatalf("record = %+v, want processed 5x6", got)
	}
}

func TestRunRejectsPositionalArguments(t *testing.T) {
	if err := Run([]string{"unexpected"}); err == nil {
		t.Fatal("expected an error for an unexpected positional argument")
	}
}

func TestRunRejectsNonPositiveWorkers(t *testing.T) {
	if err := Run([]string{"-workers", "0"}); err == nil {
		t.Fatal("expected an error for -workers 0")
	}
}
