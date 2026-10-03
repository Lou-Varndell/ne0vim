package scraper

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes(t *testing.T, w, h int, c color.Color) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func newTestServer(t *testing.T, imgA, imgB []byte) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<img src="/img/a.png">
			<a href="/img/b.png">linked image</a>
			<a href="/not-an-image">not an image</a>
		</body></html>`)
	})
	mux.HandleFunc("/img/a.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(imgA)
	})
	mux.HandleFunc("/img/b.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(imgB)
	})
	mux.HandleFunc("/not-an-image", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<html></html>")
	})
	mux.HandleFunc("/broken.png", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestDownloader builds a Downloader with a plain (non-SSRF-filtering)
// HTTP client, since httptest servers listen on loopback — which the
// production default client in httpx.NewClient deliberately refuses to
// dial.
func newTestDownloader(t *testing.T) *Downloader {
	t.Helper()

	d, err := NewDownloader(
		WithTempDir(t.TempDir()),
		WithDestDir(t.TempDir()),
		WithConcurrency(4),
		WithHTTPClient(&http.Client{}),
	)
	if err != nil {
		t.Fatalf("NewDownloader: %v", err)
	}
	return d
}

func TestRunEndToEnd(t *testing.T) {
	imgA := pngBytes(t, 4, 4, color.RGBA{R: 255, A: 255})
	imgB := pngBytes(t, 8, 8, color.RGBA{G: 255, A: 255})

	srv := newTestServer(t, imgA, imgB)
	d := newTestDownloader(t)

	got, err := d.Run(context.Background(), []string{
		srv.URL + "/page.html",
		srv.URL + "/broken.png",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	bySourceURL := make(map[string]manifestEntryLike, len(got.Entries))
	for _, e := range got.Entries {
		bySourceURL[e.SourceURL] = manifestEntryLike{e.File, e.Status, e.Width, e.Height}
	}

	a, ok := bySourceURL[srv.URL+"/img/a.png"]
	if !ok || a.status != "kept" || a.width != 4 || a.height != 4 {
		t.Errorf("a.png entry = %+v, want kept 4x4", a)
	}
	if !fileExists(a.file) {
		t.Errorf("a.png (%s) not found in destDir", a.file)
	}

	b, ok := bySourceURL[srv.URL+"/img/b.png"]
	if !ok || b.status != "kept" || b.width != 8 || b.height != 8 {
		t.Errorf("b.png entry = %+v, want kept 8x8", b)
	}

	broken, ok := bySourceURL[srv.URL+"/broken.png"]
	if !ok || broken.status == "kept" {
		t.Errorf("broken.png entry = %+v, want a download-failed status", broken)
	}

	if !fileExists(filepath.Join(d.destDir, "manifest.json")) {
		t.Error("manifest.json not written to destDir")
	}
}

// manifestEntryLike avoids importing internal/manifest just to name its
// Entry type in test assertions.
type manifestEntryLike struct {
	file   string
	status string
	width  int
	height int
}

func TestRunDeduplicatesDirectImageInputs(t *testing.T) {
	same := pngBytes(t, 2, 2, color.RGBA{B: 255, A: 255})

	mux := http.NewServeMux()
	mux.HandleFunc("/dup1/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(same)
	})
	mux.HandleFunc("/dup2/photo.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(same)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	d := newTestDownloader(t)

	got, err := d.Run(context.Background(), []string{
		srv.URL + "/dup1/photo.png",
		srv.URL + "/dup2/photo.png",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	kept := 0
	dup := 0
	for _, e := range got.Entries {
		switch e.Status {
		case "kept":
			kept++
		default:
			if len(e.Status) >= 13 && e.Status[:13] == "duplicate-of:" {
				dup++
			}
		}
	}

	if kept != 1 || dup != 1 {
		t.Fatalf("got kept=%d dup=%d, want kept=1 dup=1: %+v", kept, dup, got.Entries)
	}

	entries, err := os.ReadDir(d.destDir)
	if err != nil {
		t.Fatalf("read destDir: %v", err)
	}
	pngCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".png" {
			pngCount++
		}
	}
	if pngCount != 1 {
		t.Errorf("got %d .png files in destDir, want 1 (duplicate should not be moved)", pngCount)
	}
}
