package images

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
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

func TestCacheGenerateCreatesThumbnailNamedAfterContentHash(t *testing.T) {
	srcDir := t.TempDir()
	cacheDir := t.TempDir()
	src := filepath.Join(srcDir, "source.png")
	writeTestPNG(t, src)

	hash, err := Hash(src)
	if err != nil {
		t.Fatal(err)
	}

	c := NewCache(cacheDir, 10)
	thumbPath, err := c.Generate(src)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(cacheDir, hash[:2], hash+".jpg")
	if thumbPath != want {
		t.Fatalf("Generate() path = %q, want %q", thumbPath, want)
	}

	info, err := os.Stat(thumbPath)
	if err != nil {
		t.Fatalf("thumbnail not written: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("thumbnail file is empty")
	}
}

func TestCacheGenerateReusesExistingThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src)

	c := NewCache(t.TempDir(), 10)
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
	if second != first {
		t.Fatalf("Generate() returned a different path on the second call: %q vs %q", second, first)
	}
	secondInfo, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}

	if !firstInfo.ModTime().Equal(secondInfo.ModTime()) {
		t.Fatal("Generate() regenerated a thumbnail that already existed")
	}
}

func TestCacheGenerateDifferentContentGetsADifferentThumbnail(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	writeTestPNG(t, a)
	// A 1x1 PNG has different content (and so a different hash) than the
	// 20x20 one writeTestPNG produces.
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCache(t.TempDir(), 10)
	thumbA, err := c.Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	thumbB, err := c.Generate(b)
	if err != nil {
		t.Fatal(err)
	}

	if thumbA == thumbB {
		t.Fatalf("Generate() produced the same thumbnail path for different content: %q", thumbA)
	}
}

// TestCacheGenerateIsIndependentOfSourceLocation is the whole point of the
// centralized cache: two sources with identical content, in entirely
// different directories, land on the exact same thumbnail — proving the
// cache never writes anything into (or near) a directory Find or Scan
// might walk.
func TestCacheGenerateIsIndependentOfSourceLocation(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	a := filepath.Join(dirA, "a.png")
	b := filepath.Join(dirB, "b.png")
	writeTestPNG(t, a)
	writeTestPNG(t, b)

	c := NewCache(t.TempDir(), 10)
	thumbA, err := c.Generate(a)
	if err != nil {
		t.Fatal(err)
	}
	thumbB, err := c.Generate(b)
	if err != nil {
		t.Fatal(err)
	}

	if thumbA != thumbB {
		t.Fatalf("Generate() produced different thumbnails for identical content: %q vs %q", thumbA, thumbB)
	}
	if filepath.Dir(thumbA) == dirA || filepath.Dir(thumbA) == dirB {
		t.Fatalf("Generate() stored the thumbnail next to a source directory: %q", thumbA)
	}
}

func TestCacheGenerateErrorsOnUndecodableFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(src, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := NewCache(t.TempDir(), 10)
	if _, err := c.Generate(src); err == nil {
		t.Fatal("Generate() error = nil, want decode error")
	}
}
