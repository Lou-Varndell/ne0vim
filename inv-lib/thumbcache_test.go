package invlib

import (
	"bytes"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeTestPNG(t *testing.T, path string, size int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
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

func TestThumbCacheGenerateCreatesThumbnailNamedAfterContentHash(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src, 20)

	hash, err := HashFile(src)
	if err != nil {
		t.Fatal(err)
	}

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	thumbPath, err := c.Generate(src, false)
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(c.Dir, hash[:2], hash+".jpg")
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

func TestThumbCacheGenerateReusesExistingThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src, 20)

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	first, err := c.Generate(src, false)
	if err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := c.Generate(src, false)
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

func TestThumbCacheGenerateForceRegeneratesExistingThumbnail(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source.png")
	writeTestPNG(t, src, 20)

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	first, err := c.Generate(src, false)
	if err != nil {
		t.Fatal(err)
	}

	second, err := c.Generate(src, true)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("Generate(force=true) changed the thumbnail path: %q vs %q", second, first)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("thumbnail missing after forced regeneration: %v", err)
	}
}

func TestThumbCacheGenerateDifferentContentGetsADifferentThumbnail(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	b := filepath.Join(dir, "b.png")
	writeTestPNG(t, a, 20)
	writeTestPNG(t, b, 1)

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	thumbA, err := c.Generate(a, false)
	if err != nil {
		t.Fatal(err)
	}
	thumbB, err := c.Generate(b, false)
	if err != nil {
		t.Fatal(err)
	}

	if thumbA == thumbB {
		t.Fatalf("Generate() produced the same thumbnail path for different content: %q", thumbA)
	}
}

// TestThumbCacheGenerateIsIndependentOfSourceLocation is the whole point of
// a centralized, process-shared cache: two sources with identical content,
// in entirely different directories (standing in for two different
// programs' own image trees), land on the exact same thumbnail.
func TestThumbCacheGenerateIsIndependentOfSourceLocation(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	a := filepath.Join(dirA, "a.png")
	b := filepath.Join(dirB, "b.png")
	writeTestPNG(t, a, 20)
	writeTestPNG(t, b, 20)

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	thumbA, err := c.Generate(a, false)
	if err != nil {
		t.Fatal(err)
	}
	thumbB, err := c.Generate(b, false)
	if err != nil {
		t.Fatal(err)
	}

	if thumbA != thumbB {
		t.Fatalf("Generate() produced different thumbnails for identical content: %q vs %q", thumbA, thumbB)
	}
}

func TestThumbCacheGenerateErrorsOnUndecodableFile(t *testing.T) {
	src := filepath.Join(t.TempDir(), "not-an-image.png")
	if err := os.WriteFile(src, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	if _, err := c.Generate(src, false); err == nil {
		t.Fatal("Generate() error = nil, want decode error")
	}
}

func TestThumbCachePathMissingFile(t *testing.T) {
	c := &ThumbCache{Dir: t.TempDir(), Size: 150}
	if _, err := c.Path(filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Error("expected an error for a missing file, got nil")
	}
}

// animatedWebP is a 2-frame (red, then blue) 64x32 animated WebP.
const animatedWebPHex = "524946469000000057454250565038580a000000020000003f00001f0000414e494d06000000ffffffff0000414e4d462e0000000000000000003f00001f0000640000025650384c150000002f3fc007000710f58ffe078084f0ffbc1ad1ffd40700414e4d462e0000000000000000003f00001f0000640000005650384c150000002f3fc007000710d1fffe078084f0ffbc1ad1ffd40700"

func writeAnimatedWebP(t *testing.T, path string) {
	t.Helper()
	data, err := hex.DecodeString(animatedWebPHex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestThumbCacheGenerateAnimatedWebPUsesFirstFrame(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "anim.webp")
	writeAnimatedWebP(t, src)

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	dst, err := c.Generate(src, false)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	thumb, _, err := image.Decode(f)
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if b := thumb.Bounds(); b.Dx() != 10 || b.Dy() != 5 {
		t.Errorf("thumbnail size = %dx%d, want 10x5", b.Dx(), b.Dy())
	}
	r, g, b, _ := thumb.At(5, 2).RGBA()
	if r>>8 < 200 || g>>8 > 60 || b>>8 > 60 {
		t.Errorf("thumbnail centre = (%d,%d,%d), want the red first frame", r>>8, g>>8, b>>8)
	}
}

func TestThumbCacheGenerateAnimatedWebPWithoutFFmpeg(t *testing.T) {
	src := filepath.Join(t.TempDir(), "anim.webp")
	writeAnimatedWebP(t, src)
	t.Setenv("PATH", t.TempDir())

	c := &ThumbCache{Dir: t.TempDir(), Size: 10}
	if _, err := c.Generate(src, false); !errors.Is(err, ErrAnimatedWebP) {
		t.Fatalf("Generate() error = %v, want ErrAnimatedWebP", err)
	}
}
