package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	for y := range 20 {
		for x := range 20 {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	return &Cache{Dir: t.TempDir(), Size: 10}
}

func TestCacheGenerateCreatesThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src)

	c := newTestCache(t)
	thumbPath, err := c.Generate(src)
	if err != nil {
		t.Fatal(err)
	}

	if thumbPath != c.Path(src) {
		t.Fatalf("Generate() path = %q, want %q", thumbPath, c.Path(src))
	}

	info, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("thumbnail not written: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("thumbnail file is empty")
	}
}

func TestCacheGenerateReusesFreshThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src)

	c := newTestCache(t)
	first, err := c.Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := c.Generate(src)
	if err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}

	if !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Fatal("Generate() regenerated a thumbnail that was already fresh")
	}
}

func TestCacheGenerateRegeneratesStaleThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src)

	c := newTestCache(t)
	thumbPath, err := c.Generate(src)
	if err != nil {
		t.Fatal(err)
	}

	// Back-date the cached thumbnail so it looks older than the source,
	// simulating a source file edited after the thumbnail was cached.
	stale := time.Now().Add(-time.Hour)
	if err := os.Chtimes(thumbPath, stale, stale); err != nil {
		t.Fatal(err)
	}
	staleInfo, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := c.Generate(src); err != nil {
		t.Fatal(err)
	}
	refreshedInfo, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatal(err)
	}

	if refreshedInfo.ModTime().Equal(staleInfo.ModTime()) {
		t.Fatal("Generate() did not regenerate a stale thumbnail")
	}
}

func TestCacheGenerateErrorsOnUndecodableFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(src, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := newTestCache(t)
	if _, err := c.Generate(src); err == nil {
		t.Fatal("Generate() error = nil, want decode error")
	}
}

func TestNewCacheCreatesDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	c, err := NewCache(42)
	if err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(c.Dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("NewCache() did not create its cache directory: %v", err)
	}
}
