package size

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"image-browser/internal/db"
	"image-browser/internal/store"
)

// newTestStore opens a fresh, isolated inventory database for the
// duration of t.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return store.New(database)
}

// seedProcessedImage writes path to disk (unless skipped) and records a
// files+images row for it with the given resolution.
func seedProcessedImage(t *testing.T, inv *store.Store, path string, width, height int, writeFile bool) {
	t.Helper()
	ctx := context.Background()

	if writeFile {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	id, _, err := inv.AddFile(ctx, store.File{
		Path:      path,
		Filename:  filepath.Base(path),
		Extension: filepath.Ext(path),
		Filesize:  4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inv.AddImage(ctx, store.Image{FileID: id, Width: width, Height: height}); err != nil {
		t.Fatal(err)
	}
}

func TestLoadGroupsByResolution(t *testing.T) {
	inv := newTestStore(t)
	root := t.TempDir()

	a := filepath.Join(root, "a.jpg")
	b := filepath.Join(root, "b.jpg")
	c := filepath.Join(root, "c.jpg")

	seedProcessedImage(t, inv, a, 400, 600, true)
	seedProcessedImage(t, inv, b, 400, 600, true)
	seedProcessedImage(t, inv, c, 800, 600, true)

	groups, err := Load(context.Background(), inv, "")
	if err != nil {
		t.Fatal(err)
	}
	SortGroups(groups, false)

	want := []Group{
		{Width: 400, Height: 600, Files: []string{a, b}},
		{Width: 800, Height: 600, Files: []string{c}},
	}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

// TestLoadExcludesMissingFiles guards the "move" / view "Move All"
// invariant from size's side: a database record whose file no longer
// exists on disk must be pruned (see store.PruneMissing) before grouping,
// so it doesn't linger in the result or in the database for a later
// command that reads it.
func TestLoadExcludesMissingFiles(t *testing.T) {
	inv := newTestStore(t)
	root := t.TempDir()

	existing := filepath.Join(root, "existing.jpg")
	missing := filepath.Join(root, "missing.jpg")

	seedProcessedImage(t, inv, existing, 100, 100, true)
	seedProcessedImage(t, inv, missing, 100, 100, false)

	groups, err := Load(context.Background(), inv, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []Group{{Width: 100, Height: 100, Files: []string{existing}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}

	all, err := inv.AllFiles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		if r.Path == missing {
			t.Fatalf("expected the missing file's record to be pruned, found %+v", r)
		}
	}
}

func TestLoadScopesToRootPrefix(t *testing.T) {
	inv := newTestStore(t)
	rootA := filepath.Join(t.TempDir(), "a")
	rootB := filepath.Join(t.TempDir(), "b")

	inA := filepath.Join(rootA, "in.jpg")
	inB := filepath.Join(rootB, "in.jpg")
	seedProcessedImage(t, inv, inA, 100, 100, true)
	seedProcessedImage(t, inv, inB, 100, 100, true)

	groups, err := Load(context.Background(), inv, rootA)
	if err != nil {
		t.Fatal(err)
	}

	want := []Group{{Width: 100, Height: 100, Files: []string{inA}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

func TestLoadExcludesUnprocessedFiles(t *testing.T) {
	inv := newTestStore(t)
	root := t.TempDir()

	processed := filepath.Join(root, "processed.jpg")
	unprocessed := filepath.Join(root, "unprocessed.jpg")

	seedProcessedImage(t, inv, processed, 200, 300, true)

	ctx := context.Background()
	if err := os.WriteFile(unprocessed, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inv.AddFile(ctx, store.File{
		Path:      unprocessed,
		Filename:  filepath.Base(unprocessed),
		Extension: filepath.Ext(unprocessed),
		Filesize:  4,
	}); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(ctx, inv, "")
	if err != nil {
		t.Fatal(err)
	}

	want := []Group{{Width: 200, Height: 300, Files: []string{processed}}}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

func TestSortGroupsLargestFirst(t *testing.T) {
	groups := []Group{
		{Width: 400, Height: 600},
		{Width: 1800, Height: 1200},
		{Width: 800, Height: 600},
	}

	SortGroups(groups, true)

	want := []Group{
		{Width: 1800, Height: 1200},
		{Width: 800, Height: 600},
		{Width: 400, Height: 600},
	}

	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("SortGroups() = %v, want %v", groups, want)
	}
}
