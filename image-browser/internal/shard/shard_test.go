package shard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	invlib "inv-lib"
)

// newTestStore opens a fresh, isolated inventory database for the duration
// of t and points INVENTORY_MANAGER_DB at it so Run (which opens the
// database itself via invlib.DefaultPath) uses the same one.
func newTestStore(t *testing.T) *invlib.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	t.Setenv("INVENTORY_MANAGER_DB", path)
	db, err := invlib.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedFile writes content to path and inserts a matching files row,
// returning the inserted row.
func seedFile(t *testing.T, inv *invlib.DB, path, content string) *invlib.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &invlib.File{Path: path, Filename: filepath.Base(path), Filesize: int64(len(content))}
	if err := inv.CreateFile(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRunReportOnlyDoesNotMoveFiles(t *testing.T) {
	inv := newTestStore(t)
	srcDir := t.TempDir()
	root := t.TempDir()

	srcPath := filepath.Join(srcDir, "a.jpg")
	seedFile(t, inv, srcPath, "aaa")

	if err := Run([]string{"-root", root, "-capacity", "4096"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		t.Errorf("source file moved despite report-only run: %v", err)
	}

	got, err := inv.GetFileByPath(context.Background(), srcPath)
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if got.Path != srcPath {
		t.Errorf("database path changed despite report-only run: got %s, want %s", got.Path, srcPath)
	}
}

func TestRunApplyShardsAndUpdatesDatabase(t *testing.T) {
	inv := newTestStore(t)
	srcDir := t.TempDir()
	root := t.TempDir()

	var files []*invlib.File
	for i, name := range []string{"a.jpg", "b.jpg", "c.jpg"} {
		path := filepath.Join(srcDir, name)
		files = append(files, seedFile(t, inv, path, []string{"aaa", "bb", "c"}[i]))
	}

	// Capacity 2: the third file must land in a second shard directory.
	if err := Run([]string{"-root", root, "-capacity", "2", "-apply"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	shardDirs := make(map[string]int)
	for _, f := range files {
		updated, err := inv.GetFile(context.Background(), f.ID)
		if err != nil {
			t.Fatalf("GetFile: %v", err)
		}

		if _, err := os.Stat(updated.Path); err != nil {
			t.Errorf("file %d not found at updated path %s: %v", f.ID, updated.Path, err)
		}
		if !invlib.IsSharded(root, updated.Path) {
			t.Errorf("file %d at %s is not recognized as sharded under %s", f.ID, updated.Path, root)
		}
		if updated.Path == f.Path {
			t.Errorf("file %d's database path was not updated: still %s", f.ID, updated.Path)
		}

		shardDirs[filepath.Dir(updated.Path)]++
	}

	if len(shardDirs) != 2 {
		t.Errorf("got files spread across %d shard directories, want 2 (capacity 2, 3 files): %+v", len(shardDirs), shardDirs)
	}
}

func TestRunSkipsAlreadyShardedFiles(t *testing.T) {
	inv := newTestStore(t)
	root := t.TempDir()

	// Already correctly placed: a direct child of one of root's shard
	// directories.
	shardedPath := filepath.Join(root, "0000000000", "already.jpg")
	seedFile(t, inv, shardedPath, "aaa")

	if err := Run([]string{"-root", root, "-apply"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := inv.GetFileByPath(context.Background(), shardedPath)
	if err != nil {
		t.Fatalf("GetFileByPath: already-sharded file was moved or its path changed: %v", err)
	}
	if got.Path != shardedPath {
		t.Errorf("already-sharded file's path changed: got %s, want %s", got.Path, shardedPath)
	}
}
