package catalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerView(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	writePNG(t, filepath.Join(dir, "sub", "photo.png"), 10, 10)

	db := newTestDB(t)
	if err := ScanToDB(dir, db, quietLogger()); err != nil {
		t.Fatalf("ScanToDB: %v", err)
	}

	h := &handler{log: quietLogger(), imageDir: dir, db: db}

	t.Run("serves an image with a back link to its containing directory", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/view?path=sub/photo.png", nil)
		rec := httptest.NewRecorder()
		h.view(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "/catalog?dir=sub") {
			t.Errorf("response missing back link to sub, got: %s", body)
		}
		if !strings.Contains(body, `src="/images/sub/photo.png"`) {
			t.Errorf("response missing absolute image src, got: %s", body)
		}
	})

	t.Run("404s for a directory", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/view?path=sub", nil)
		rec := httptest.NewRecorder()
		h.view(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("404s for a missing file", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/view?path=sub/missing.png", nil)
		rec := httptest.NewRecorder()
		h.view(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("clamps a traversal attempt inside the jail rather than escaping", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/view?path=../../../etc/passwd", nil)
		rec := httptest.NewRecorder()
		h.view(rec, req)
		// resolveDir clamps ".." at the root rather than rejecting it, so
		// this lands on a nonexistent path inside dir, not outside it.
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404 (never 200)", rec.Code)
		}
	})
}

// TestHandlerMoreImages covers the infinite-scroll continuation endpoint:
// it must render only the next batch of cells (never the breadcrumb or
// directory listing the initial page already sent), and must reject a
// malformed offset instead of silently falling back to zero.
func TestHandlerMoreImages(t *testing.T) {
	dir := t.TempDir()

	db := newTestDB(t)
	seedImages(t, db, ImagePageSize+1)

	h := &handler{log: quietLogger(), imageDir: dir, db: db}

	t.Run("serves the overflow image and no further sentinel", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/catalog/images/more?dir=&offset=%d", ImagePageSize), nil)
		rec := httptest.NewRecorder()
		h.moreImages(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if strings.Count(body, "cell") != 1 {
			t.Errorf("body has %d cells, want exactly 1 (the final overflow image): %s", strings.Count(body, "cell"), body)
		}
		if strings.Contains(body, "scroll-sentinel") {
			t.Errorf("response includes a sentinel past the last image: %s", body)
		}
		if strings.Contains(body, "navbar") {
			t.Errorf("response includes breadcrumb chrome, want image cells only: %s", body)
		}
	})

	t.Run("serves a sentinel mid-way through the images", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/images/more?dir=&offset=0", nil)
		rec := httptest.NewRecorder()
		h.moreImages(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), "scroll-sentinel") {
			t.Error("response missing sentinel, want one since images remain beyond this batch")
		}
	})

	t.Run("400s on a malformed offset", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/catalog/images/more?dir=&offset=not-a-number", nil)
		rec := httptest.NewRecorder()
		h.moreImages(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})
}
