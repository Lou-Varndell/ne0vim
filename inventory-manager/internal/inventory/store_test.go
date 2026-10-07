package inventory

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "inventory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestUpsertAndCount(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	rec := Record{
		Path:      "/photos/a.jpg",
		Hash:      "deadbeef",
		Size:      1024,
		Width:     800,
		Height:    600,
		ScannedAt: time.Now(),
	}
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	n, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count = %d, want 1", n)
	}

	// Upserting the same path again updates in place rather than inserting
	// a second row.
	rec.Hash = "newhash"
	if err := store.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert (update): %v", err)
	}
	n, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Fatalf("Count after update = %d, want 1", n)
	}
}

func TestDuplicatesOf(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	now := time.Now()
	for _, rec := range []Record{
		{Path: "/photos/a.jpg", Hash: "same", Size: 1, Width: 1, Height: 1, ScannedAt: now},
		{Path: "/photos/b.jpg", Hash: "same", Size: 1, Width: 1, Height: 1, ScannedAt: now},
		{Path: "/photos/c.jpg", Hash: "different", Size: 1, Width: 1, Height: 1, ScannedAt: now},
	} {
		if err := store.Upsert(ctx, rec); err != nil {
			t.Fatalf("Upsert(%q): %v", rec.Path, err)
		}
	}

	dupes, err := store.DuplicatesOf(ctx, "/photos/a.jpg", "same")
	if err != nil {
		t.Fatalf("DuplicatesOf: %v", err)
	}
	if len(dupes) != 1 || dupes[0] != "/photos/b.jpg" {
		t.Fatalf("DuplicatesOf = %v, want [/photos/b.jpg]", dupes)
	}

	dupes, err = store.DuplicatesOf(ctx, "/photos/c.jpg", "different")
	if err != nil {
		t.Fatalf("DuplicatesOf: %v", err)
	}
	if len(dupes) != 0 {
		t.Fatalf("DuplicatesOf = %v, want none", dupes)
	}
}
