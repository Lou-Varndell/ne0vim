package warmthumbs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	invlib "inv-lib"
)

func writeTestPNG(t *testing.T, path string, size int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 50, A: 255})
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

// setupWarmTestDB creates a fresh database containing one file+image row
// for imgPath, returning the database's path.
func setupWarmTestDB(t *testing.T, imgPath string) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "inv.db")
	db, err := invlib.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	f := &invlib.File{Path: imgPath, Filename: filepath.Base(imgPath), Filesize: 1}
	if err := db.CreateFile(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertImage(context.Background(), &invlib.Image{FileID: f.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// runCapturingStdout runs Run(args) with os.Stdout redirected to a buffer,
// so a test can assert on Run's printed summary line (e.g. "N generated,
// M already cached") — the only place WarmAllResult's counts surface
// through this command-line interface.
func runCapturingStdout(t *testing.T, args []string) (output string, runErr error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	runErr = Run(args)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String(), runErr
}

// TestRunWarmsMissingThumbnails exercises Run end to end against an
// isolated database and image directory, asserting a thumbnail actually
// lands in the shared cache for a file row that didn't have one yet.
//
// HOME is redirected to a temp directory so invlib.NewThumbCache (via
// os.UserCacheDir) never touches the real, shared cache this test's host
// machine might already have.
func TestRunWarmsMissingThumbnails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath, 20)
	dbPath := setupWarmTestDB(t, imgPath)

	if err := Run([]string{"-db", dbPath, "-quiet"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	cache, err := invlib.NewThumbCache(ThumbnailSize)
	if err != nil {
		t.Fatal(err)
	}
	thumbPath, err := cache.Path(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(thumbPath); err != nil {
		t.Errorf("thumbnail was not generated for %s: %v", imgPath, err)
	}
}

// TestRunRejectsPositionalArguments guards the same usage-error convention
// every other subcommand in this program follows.
func TestRunRejectsPositionalArguments(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Run([]string{"unexpected"}); err == nil {
		t.Error("Run() error = nil, want an error for an unexpected positional argument")
	}
}

// TestRunDefaultOnlyGeneratesMissingThumbnails proves the no-flags case:
// a first run generates the one missing thumbnail, and an immediate
// second run (no -refresh) leaves it alone, reporting it as already
// cached rather than regenerating it.
func TestRunDefaultOnlyGeneratesMissingThumbnails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath, 20)
	dbPath := setupWarmTestDB(t, imgPath)

	out, err := runCapturingStdout(t, []string{"-db", dbPath, "-quiet"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out, "1 generated, 0 already cached") {
		t.Fatalf("first run summary = %q, want it to report 1 generated", out)
	}

	out, err = runCapturingStdout(t, []string{"-db", dbPath, "-quiet"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out, "0 generated, 1 already cached") {
		t.Errorf("second run summary = %q, want it to report 1 already cached, 0 generated", out)
	}
}

// TestRunRefreshRegeneratesAlreadyCachedThumbnails proves -refresh
// bypasses the "already cached" shortcut: a second run with -refresh
// after a first, unflagged run must still report the file as generated,
// not already cached.
func TestRunRefreshRegeneratesAlreadyCachedThumbnails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath, 20)
	dbPath := setupWarmTestDB(t, imgPath)

	if _, err := runCapturingStdout(t, []string{"-db", dbPath, "-quiet"}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	out, err := runCapturingStdout(t, []string{"-db", dbPath, "-quiet", "-refresh"})
	if err != nil {
		t.Fatalf("Run() with -refresh error = %v", err)
	}
	if !strings.Contains(out, "1 generated, 0 already cached") {
		t.Errorf("-refresh run summary = %q, want it to report 1 generated, 0 already cached", out)
	}
}

// TestRunThumbnailSizeUsesASeparateCacheAndImpliesRefresh proves
// -ThumbnailSize both (a) reads and writes a different, size-specific
// cache directory than the default, and (b) forces regeneration on its
// own — a second run with the exact same -ThumbnailSize, and no
// -refresh, must still report its file as generated rather than
// already cached.
func TestRunThumbnailSizeUsesASeparateCacheAndImpliesRefresh(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	imgPath := filepath.Join(dir, "a.png")
	writeTestPNG(t, imgPath, 20)
	dbPath := setupWarmTestDB(t, imgPath)

	const customSize = 75

	out, err := runCapturingStdout(t, []string{"-db", dbPath, "-quiet", "-ThumbnailSize", "75"})
	if err != nil {
		t.Fatalf("Run() with -ThumbnailSize error = %v", err)
	}
	if !strings.Contains(out, "1 generated, 0 already cached") {
		t.Fatalf("first -ThumbnailSize run summary = %q, want 1 generated", out)
	}

	customCache, err := invlib.NewThumbCache(customSize)
	if err != nil {
		t.Fatal(err)
	}
	customThumb, err := customCache.Path(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(customThumb); err != nil {
		t.Errorf("thumbnail at the custom size was not generated: %v", err)
	}

	defaultCache, err := invlib.NewThumbCache(ThumbnailSize)
	if err != nil {
		t.Fatal(err)
	}
	defaultThumb, err := defaultCache.Path(imgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultThumb); err == nil {
		t.Errorf("-ThumbnailSize=75 also wrote into the default-size cache at %s", defaultThumb)
	}

	// Run again at the same custom size, without -refresh: the implied
	// force must still regenerate, not skip, this file.
	out, err = runCapturingStdout(t, []string{"-db", dbPath, "-quiet", "-ThumbnailSize", "75"})
	if err != nil {
		t.Fatalf("second Run() with -ThumbnailSize error = %v", err)
	}
	if !strings.Contains(out, "1 generated, 0 already cached") {
		t.Errorf("second -ThumbnailSize run summary = %q, want 1 generated (implied -refresh), not already cached", out)
	}
}

// TestRunRejectsNonPositiveThumbnailSize guards against a size that would
// make cache.Generate's resize call meaningless or panic.
func TestRunRejectsNonPositiveThumbnailSize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := Run([]string{"-ThumbnailSize", "0"}); err == nil {
		t.Error("Run() error = nil, want an error for -ThumbnailSize=0")
	}
}
