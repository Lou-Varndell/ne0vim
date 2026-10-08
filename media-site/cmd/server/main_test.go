package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
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
