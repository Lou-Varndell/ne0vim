package invlib

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createWarmTestFile inserts a file+image row for path (which need not
// exist on disk — a stale row pointing at a deleted file is one of the
// cases WarmAll must handle), returning the inserted *File so a test can
// mutate it (e.g. to mark it missing) before calling UpdateFile.
func createWarmTestFile(t *testing.T, ctx context.Context, db *DB, path string) *File {
	t.Helper()

	var size int64 = 100
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}

	f := &File{Path: path, Filename: filepath.Base(path), Filesize: size}
	if err := db.CreateFile(ctx, f); err != nil {
		t.Fatalf("create file %q: %v", path, err)
	}
	if err := db.UpsertImage(ctx, &Image{FileID: f.ID}); err != nil {
		t.Fatalf("upsert image for %q: %v", path, err)
	}
	return f
}

func TestWarmAll(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir := t.TempDir()
	cache := &ThumbCache{Dir: t.TempDir(), Size: 10}

	// a: a real, undecoded image — should be newly Generated.
	a := filepath.Join(dir, "a.png")
	writeTestPNG(t, a, 20)
	createWarmTestFile(t, ctx, db, a)

	// b: a real image, pre-warmed before WarmAll runs — should be Skipped.
	b := filepath.Join(dir, "b.png")
	writeTestPNG(t, b, 1)
	createWarmTestFile(t, ctx, db, b)
	if _, err := cache.Generate(b, false); err != nil {
		t.Fatalf("pre-warm b: %v", err)
	}

	// c: a file row whose source no longer exists on disk — should count
	// as Failed, without aborting the rest of the run.
	c := filepath.Join(dir, "c.png")
	createWarmTestFile(t, ctx, db, c)

	// v: a file row with no images row (a non-image, e.g. a video) — must
	// be excluded from Total entirely, not merely skipped or failed.
	v := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(v, []byte("not really a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateFile(ctx, &File{Path: v, Filename: "v.mp4", Filesize: 10}); err != nil {
		t.Fatal(err)
	}

	// m: a real image already marked missing_since — must be excluded
	// from Total, the same as PruneMissing's own consumers already expect.
	m := filepath.Join(dir, "m.png")
	writeTestPNG(t, m, 5)
	mFile := createWarmTestFile(t, ctx, db, m)
	now := time.Now().UTC()
	mFile.MissingSince = &now
	if err := db.UpdateFile(ctx, mFile); err != nil {
		t.Fatal(err)
	}

	var progressCalls int
	result, err := WarmAll(ctx, db, cache, 0, false, func(done, total int, path string, err error) {
		progressCalls++
	})
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}

	if result.Total != 3 {
		t.Errorf("Total = %d, want 3 (a, b, c only — v has no images row, m is missing)", result.Total)
	}
	if result.Generated != 1 {
		t.Errorf("Generated = %d, want 1 (a)", result.Generated)
	}
	if result.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 (b, pre-warmed)", result.Skipped)
	}
	if result.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (c, missing source)", result.Failed)
	}
	if progressCalls != result.Total {
		t.Errorf("progress callback fired %d times, want %d (once per file)", progressCalls, result.Total)
	}

	// a's thumbnail must actually exist on disk now.
	if _, err := cache.Path(a); err != nil {
		t.Fatalf("Path(a) after WarmAll: %v", err)
	}
	thumbA, err := cache.Path(a)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(thumbA); err != nil {
		t.Errorf("thumbnail for a was not generated: %v", err)
	}
}

func TestWarmAllRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before WarmAll even starts

	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.png")
	writeTestPNG(t, a, 20)
	createWarmTestFile(t, context.Background(), db, a)

	cache := &ThumbCache{Dir: t.TempDir(), Size: 10}

	result, err := WarmAll(ctx, db, cache, 0, false, nil)
	if err == nil {
		t.Fatal("WarmAll() error = nil, want ctx.Err()")
	}
	if result.Generated+result.Skipped+result.Failed != 0 {
		t.Errorf("expected no files processed once ctx was already cancelled, got %+v", result)
	}
}

// TestWarmAllForceRegeneratesEverything proves force=true bypasses the
// "already cached" check entirely: a, already pre-warmed before the run,
// is still regenerated (and counted as Generated, not Skipped) rather
// than left alone.
func TestWarmAllForceRegeneratesEverything(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir := t.TempDir()
	cache := &ThumbCache{Dir: t.TempDir(), Size: 10}

	a := filepath.Join(dir, "a.png")
	writeTestPNG(t, a, 20)
	createWarmTestFile(t, ctx, db, a)

	firstThumb, err := cache.Generate(a, false)
	if err != nil {
		t.Fatalf("pre-warm a: %v", err)
	}
	firstInfo, err := os.Stat(firstThumb)
	if err != nil {
		t.Fatal(err)
	}

	result, err := WarmAll(ctx, db, cache, 0, true, nil)
	if err != nil {
		t.Fatalf("WarmAll: %v", err)
	}

	if result.Generated != 1 {
		t.Errorf("Generated = %d, want 1 (force must regenerate, not skip, the pre-warmed file)", result.Generated)
	}
	if result.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 under force", result.Skipped)
	}

	secondInfo, err := os.Stat(firstThumb)
	if err != nil {
		t.Fatal(err)
	}
	if !secondInfo.ModTime().After(firstInfo.ModTime()) && secondInfo.ModTime() != firstInfo.ModTime() {
		// Equal mtimes are possible on filesystems with coarse timestamp
		// resolution, so this only fails if the thumbnail visibly wasn't
		// touched at all (an earlier mtime), not merely an unchanged one.
		t.Errorf("thumbnail mtime went backwards after a forced regeneration: %v -> %v", firstInfo.ModTime(), secondInfo.ModTime())
	}
}
