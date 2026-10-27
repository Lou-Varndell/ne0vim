package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	invlib "inv-lib"
)

func TestResolveImageDir(t *testing.T) {
	t.Run("explicit flag wins", func(t *testing.T) {
		got, err := resolveImageDir("/tmp/whatever")
		if err != nil {
			t.Fatalf("resolveImageDir: %v", err)
		}
		if got != "/tmp/whatever" {
			t.Errorf("got %q, want /tmp/whatever", got)
		}
	})

	t.Run("empty flag falls back to $HOME/Images", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home directory available: %v", err)
		}

		got, err := resolveImageDir("")
		if err != nil {
			t.Fatalf("resolveImageDir: %v", err)
		}
		if want := filepath.Join(home, "Images"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// TestImageFileServer guards the security fix: individual files under
// imageDir are served, but any path resolving to a directory (including the
// root) returns 404 rather than a listing.
func TestImageFileServer(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := http.StripPrefix("/images/", imageFileServer(dir))

	cases := []struct {
		path string
		want int
	}{
		{"/images/", http.StatusNotFound},
		{"/images/sub/", http.StatusNotFound},
		{"/images/sub/f.txt", http.StatusOK},
		{"/images/missing.txt", http.StatusNotFound},
		{"/images/../../../etc/passwd", http.StatusNotFound},
	}

	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.want {
				t.Errorf("GET %s = %d, want %d", c.path, rec.Code, c.want)
			}
		})
	}
}

// TestImageFileServer_Head verifies HEAD requests get headers but no body,
// matching the GET/HEAD-only routing in newRouter.
func TestImageFileServer_Head(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	h := http.StripPrefix("/images/", imageFileServer(dir))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/images/f.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("HEAD /images/f.txt = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD response body = %d bytes, want 0", rec.Body.Len())
	}
}

// writeTestPNG writes a large, high-frequency (and so poorly PNG-
// compressible) source image, so a 50px thumbnail of it is reliably
// smaller than the source file — a small or already-simple source image
// can otherwise encode larger as a JPEG thumbnail than it was as PNG.
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	const n = 1000
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for y := range n {
		for x := range n {
			img.Set(x, y, color.RGBA{R: uint8(x * y), G: uint8(x ^ y), B: uint8(x + y), A: 255})
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

// TestThumbFileServer proves the new /thumbs route actually generates and
// serves a real, smaller-than-the-original thumbnail through inv-lib's
// shared ThumbCache — not just the full-size file media-site used to serve
// in its place.
func TestThumbFileServer(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "photo.png")
	writeTestPNG(t, srcPath)

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	cache := &invlib.ThumbCache{Dir: t.TempDir(), Size: 50}
	h := http.StripPrefix("/thumbs/", thumbFileServer(dir, cache))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thumbs/photo.png", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /thumbs/photo.png = %d, want 200", rec.Code)
	}

	body := rec.Body.Bytes()
	if len(body) == 0 {
		t.Fatal("thumbnail response body is empty")
	}
	if int64(len(body)) >= srcInfo.Size() {
		t.Errorf("thumbnail (%d bytes) is not smaller than the source PNG (%d bytes)", len(body), srcInfo.Size())
	}

	if _, _, err := image.Decode(bytes.NewReader(body)); err != nil {
		t.Errorf("thumbnail response is not a decodable image: %v", err)
	}

	// A second request must be served from the cache rather than failing
	// or regenerating — Generate's own idempotency (see inv-lib) is what
	// makes repeat requests for the same image cheap.
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/thumbs/photo.png", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("second GET /thumbs/photo.png = %d, want 200", rec2.Code)
	}
	if !bytes.Equal(rec2.Body.Bytes(), body) {
		t.Error("second request returned different thumbnail bytes than the first")
	}
}

// TestThumbFileServer_MissingSource confirms a request for a path with no
// backing file 404s instead of panicking or hanging on cache.Generate.
func TestThumbFileServer_MissingSource(t *testing.T) {
	dir := t.TempDir()
	cache := &invlib.ThumbCache{Dir: t.TempDir(), Size: 50}
	h := http.StripPrefix("/thumbs/", thumbFileServer(dir, cache))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thumbs/missing.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /thumbs/missing.png = %d, want 404", rec.Code)
	}
}
