package browse

import (
	"context"
	"path/filepath"
	"testing"

	invlib "inv-lib"
)

// newTestStore opens a fresh, isolated inventory database for the
// duration of t, mirroring internal/move's own test helper of the same
// name.
func newTestStore(t *testing.T) *invlib.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	database, err := invlib.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// createFileWithImage seeds a files row plus a matching images row, so the
// record satisfies loadRecordsCmd's RequireImage filter.
func createFileWithImage(t *testing.T, inv *invlib.DB, path string) {
	t.Helper()
	ctx := context.Background()

	f := &invlib.File{Path: path, Filename: filepath.Base(path)}
	if err := inv.CreateFile(ctx, f); err != nil {
		t.Fatal(err)
	}

	width, height := 100, 200
	if err := inv.UpsertImage(ctx, &invlib.Image{FileID: f.ID, Width: &width, Height: &height}); err != nil {
		t.Fatal(err)
	}
}

// TestLoadRecordsCmdScopesToRootAndRequiresImage exercises loadRecordsCmd
// against a real inv-lib database (rather than the synthetic
// recordsLoadedMsg values update_test.go feeds into the state machine
// directly) to confirm the actual FileQuery — PathPrefix plus
// RequireImage — returns what browse expects: files under root that have
// been decoded into an images row, and nothing outside root.
func TestLoadRecordsCmdScopesToRootAndRequiresImage(t *testing.T) {
	inv := newTestStore(t)
	ctx := context.Background()

	root := "/photos/root"
	createFileWithImage(t, inv, filepath.Join(root, "a.jpg"))
	createFileWithImage(t, inv, filepath.Join(root, "sub", "b.jpg"))

	// Outside root: must not appear even though it otherwise qualifies.
	createFileWithImage(t, inv, "/photos/elsewhere/c.jpg")

	// Under root but never decoded into an images row: must not appear.
	if err := inv.CreateFile(ctx, &invlib.File{Path: filepath.Join(root, "d.jpg"), Filename: "d.jpg"}); err != nil {
		t.Fatal(err)
	}

	msg := loadRecordsCmd(ctx, inv, root)()
	loaded, ok := msg.(recordsLoadedMsg)
	if !ok {
		t.Fatalf("loadRecordsCmd() returned %T, want recordsLoadedMsg", msg)
	}
	if loaded.err != nil {
		t.Fatal(loaded.err)
	}

	if len(loaded.records) != 2 {
		t.Fatalf("got %d record(s), want 2: %+v", len(loaded.records), loaded.records)
	}
	for _, r := range loaded.records {
		if r.Image == nil {
			t.Errorf("record %s has no Image, want RequireImage to have excluded it", r.File.Path)
		}
	}
}
