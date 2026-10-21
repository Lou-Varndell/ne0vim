package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCachePathKeyedByContentNotPath(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jpg")
	b := filepath.Join(dir, "subdir", "b.jpg")

	if err := os.MkdirAll(filepath.Dir(b), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a, []byte("same content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same content"), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Cache{Dir: t.TempDir(), Size: 150}

	pathA, err := c.Path(a)
	if err != nil {
		t.Fatalf("Path(a): %v", err)
	}
	pathB, err := c.Path(b)
	if err != nil {
		t.Fatalf("Path(b): %v", err)
	}

	// Two different source paths with identical content must map to the
	// same cache entry — this is the whole point of keying by content hash
	// rather than by path, and the same hash image-browser's own cache (and
	// invlib's files.hash column) already use.
	if pathA != pathB {
		t.Errorf("Path(a) = %q, Path(b) = %q; want identical content to share a cache entry", pathA, pathB)
	}

	if err := os.WriteFile(b, []byte("different content"), 0o600); err != nil {
		t.Fatal(err)
	}
	pathB2, err := c.Path(b)
	if err != nil {
		t.Fatalf("Path(b) after rewrite: %v", err)
	}
	if pathA == pathB2 {
		t.Errorf("Path(a) and Path(b) matched after b's content changed: %q", pathA)
	}
}

func TestCachePathMissingFile(t *testing.T) {
	c := &Cache{Dir: t.TempDir(), Size: 150}
	if _, err := c.Path(filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Error("expected an error for a missing file, got nil")
	}
}
