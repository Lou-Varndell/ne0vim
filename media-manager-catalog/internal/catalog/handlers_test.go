package catalog

import (
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

	h := &handler{log: quietLogger(), imageDir: dir}

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
