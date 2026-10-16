package invlib

import (
	"context"
	"path/filepath"
	"testing"
)

func TestQueryFileIDsByIDColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	a := &File{Path: "/library/a.jpg", Filename: "a.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, a); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	b := &File{Path: "/library/b.jpg", Filename: "b.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, b); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	ids, err := db.QueryFileIDs(ctx, "SELECT id FROM files WHERE path LIKE '%a.jpg'")
	if err != nil {
		t.Fatalf("QueryFileIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("QueryFileIDs = %v, want [%d]", ids, a.ID)
	}
}

func TestQueryFileIDsByFileIDColumnDedupesJoins(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	a := &File{Path: "/library/a.jpg", Filename: "a.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, a); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if err := db.SetFileTags(ctx, a.ID, []string{"beach", "sunset"}); err != nil {
		t.Fatalf("SetFileTags: %v", err)
	}

	// A join against file_tags returns one row per tag, i.e. two rows for
	// this one file — QueryFileIDs must collapse that to a single ID.
	ids, err := db.QueryFileIDs(ctx, `
		SELECT f.id AS file_id FROM files f
		JOIN file_tags ft ON ft.file_id = f.id
	`)
	if err != nil {
		t.Fatalf("QueryFileIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != a.ID {
		t.Fatalf("QueryFileIDs = %v, want exactly one id [%d] despite the join's duplicate rows", ids, a.ID)
	}
}

func TestQueryFileIDsEmptyResultIsNonNil(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	ids, err := db.QueryFileIDs(ctx, "SELECT id FROM files WHERE 1 = 0")
	if err != nil {
		t.Fatalf("QueryFileIDs: %v", err)
	}
	if ids == nil {
		t.Fatal("QueryFileIDs with zero matches returned nil, want a non-nil empty slice (see FileQuery.FileIDs nil-vs-empty semantics)")
	}
	if len(ids) != 0 {
		t.Fatalf("QueryFileIDs = %v, want empty", ids)
	}
}

func TestQueryFileIDsRequiresIDColumn(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	a := &File{Path: "/library/a.jpg", Filename: "a.jpg", Filesize: 1}
	if err := db.CreateFile(ctx, a); err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	if _, err := db.QueryFileIDs(ctx, "SELECT path FROM files"); err == nil {
		t.Fatal("expected an error when the query has no file_id or id column")
	}
}

func TestQueryFileIDsSurfacesSQLError(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if _, err := db.QueryFileIDs(ctx, "SELECT id FROM not_a_real_table"); err == nil {
		t.Fatal("expected an error for a query against a nonexistent table")
	}
}

func TestOpenReadOnlyRejectsWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "inv.db")

	// A normal Open first, so the schema exists before we connect
	// read-only — OpenReadOnly deliberately never migrates.
	rw, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { rw.Close() })

	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	t.Cleanup(func() { ro.Close() })

	if _, err := ro.ExecContext(ctx, "DELETE FROM files"); err == nil {
		t.Fatal("expected a write through a read-only connection to be rejected")
	}

	// Reads must still work.
	if _, err := ro.QueryFileIDs(ctx, "SELECT id FROM files"); err != nil {
		t.Fatalf("QueryFileIDs through read-only connection: %v", err)
	}
}

func TestOpenReadOnlyDoesNotCreateMissingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "does-not-exist.db")

	if _, err := OpenReadOnly(ctx, path); err == nil {
		t.Fatal("expected an error opening a nonexistent database read-only, not silent creation")
	}
}
