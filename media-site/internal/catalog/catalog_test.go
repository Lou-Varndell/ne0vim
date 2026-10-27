package catalog

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"media-site/internal/db"
	"media-site/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// canonicalTempDir returns a fresh temp directory, symlink-resolved exactly
// as canonicalRoot resolves handler.imageDir — tests that go through a
// handler (which canonicalizes imageDir before building query prefixes)
// must seed under this same resolved form, or the absolute paths the store
// records won't match what the handler looks up (macOS routes t.TempDir()
// through /var/folders, a symlink to /private/var/folders).
func canonicalTempDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

// newTestStore opens a fresh shared-schema inventory database backed by a
// temp-dir SQLite file, closed automatically at the end of the test.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	st := store.New(conn)
	t.Cleanup(func() { st.Close() })
	return st
}

// seedImage inserts a single catalogued image row at root/relPath directly
// into st, bypassing the filesystem entirely — the catalog package never
// scans, so every test seeds the index this way rather than writing real
// image files.
func seedImage(t *testing.T, st *store.Store, root, relPath string, width, height int) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relPath))
	id, _, err := st.AddFile(context.Background(), store.File{
		Path:     path,
		Filename: filepath.Base(path),
	})
	if err != nil {
		t.Fatalf("add file %s: %v", relPath, err)
	}
	if _, err := st.AddImage(context.Background(), store.Image{FileID: id, Width: width, Height: height, Format: "png"}); err != nil {
		t.Fatalf("add image %s: %v", relPath, err)
	}
}

func TestResolveDir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	absRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	cases := []struct {
		name   string
		dir    string
		want   string
		wantOK bool
	}{
		{"empty resolves to root", "", absRoot, true},
		{"sub resolves under root", "sub", filepath.Join(absRoot, "sub"), true},
		// ".." segments are clamped at the root, so this lands at root/etc
		// (inside the jail), never at the real /etc.
		{"dot-dot cannot escape", "../../../etc", filepath.Join(absRoot, "etc"), true},
		{"leading slash is treated relative", "/sub", filepath.Join(absRoot, "sub"), true},
		{"deep traversal is clamped", "sub/../../..", absRoot, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resolveDir(root, c.dir)
			if ok != c.wantOK {
				t.Fatalf("resolveDir(%q) ok = %v, want %v", c.dir, ok, c.wantOK)
			}
			if ok && got != c.want {
				t.Errorf("resolveDir(%q) = %q, want %q", c.dir, got, c.want)
			}
		})
	}
}
