package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"media-site/internal/catalogmodel"
	"media-site/internal/db"
)

// newTestStore mirrors internal/catalog's helper of the same name: a fresh
// shared-schema inventory database backed by a temp-dir SQLite file.
func newTestStore(t *testing.T) *Store {
	t.Helper()

	conn, err := db.Open(filepath.Join(t.TempDir(), "inv.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	s := New(conn)
	t.Cleanup(func() { s.Close() })
	return s
}

// seedFile inserts a file+image row at path with the given dimensions,
// returning its id so a test can attach tags/origin/status to it.
func seedFile(t *testing.T, s *Store, path string, width, height int) int64 {
	t.Helper()

	ctx := context.Background()
	id, _, err := s.AddFile(ctx, File{Path: path, Filename: filepath.Base(path), Filesize: 1})
	if err != nil {
		t.Fatalf("add file %s: %v", path, err)
	}
	if _, err := s.AddImage(ctx, Image{FileID: id, Width: width, Height: height, Format: "png"}); err != nil {
		t.Fatalf("add image %s: %v", path, err)
	}
	return id
}

// seedTag attaches name to fileID, creating the tag if it doesn't exist —
// these aggregation/filter queries are the only thing in this package that
// touches tags, so there's no AddTag on Store to reuse here.
func seedTag(t *testing.T, s *Store, fileID int64, name string) {
	t.Helper()

	if _, err := s.db.Exec(`INSERT OR IGNORE INTO tags (name) VALUES (?)`, name); err != nil {
		t.Fatalf("insert tag %s: %v", name, err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO file_tags (file_id, tag_id)
		SELECT ?, id FROM tags WHERE name = ?
	`, fileID, name); err != nil {
		t.Fatalf("tag file %d with %s: %v", fileID, name, err)
	}
}

// seedOrigin gives fileID an origin with the given site and discovered_at
// (RFC3339 or any SQLite-comparable DATETIME string, "" to leave it unset).
func seedOrigin(t *testing.T, s *Store, fileID int64, site, discoveredAt string) {
	t.Helper()

	result, err := s.db.Exec(`
		INSERT INTO origins (type, site, identifier, discovered_at)
		VALUES ('test', ?, ?, ?)
	`, site, fmt.Sprintf("id-%d", fileID), nullIfEmpty(discoveredAt))
	if err != nil {
		t.Fatalf("insert origin for file %d: %v", fileID, err)
	}
	originID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("get origin id: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE files SET origin_id = ? WHERE id = ?`, originID, fileID); err != nil {
		t.Fatalf("attach origin to file %d: %v", fileID, err)
	}
}

// seedStatus sets fileID's status column.
func seedStatus(t *testing.T, s *Store, fileID int64, status string) {
	t.Helper()

	if _, err := s.db.Exec(`UPDATE files SET status = ? WHERE id = ?`, status, fileID); err != nil {
		t.Fatalf("set status for file %d: %v", fileID, err)
	}
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestTagCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := seedFile(t, s, "/root/a.png", 10, 10)
	b := seedFile(t, s, "/root/b.png", 10, 10)
	c := seedFile(t, s, "/root/c.png", 10, 10)

	seedTag(t, s, a, "portrait")
	seedTag(t, s, b, "portrait")
	seedTag(t, s, c, "landscape")

	counts, err := s.TagCounts(ctx, "/root/")
	if err != nil {
		t.Fatalf("TagCounts: %v", err)
	}

	want := map[string]int{"portrait": 2, "landscape": 1}
	if len(counts) != len(want) {
		t.Fatalf("counts = %+v, want %v", counts, want)
	}
	for _, c := range counts {
		if want[c.Name] != c.Count {
			t.Errorf("tag %q count = %d, want %d", c.Name, c.Count, want[c.Name])
		}
	}
}

// TestListFilteredImagePaths_MultipleTagsAreANDed verifies that selecting
// several tags narrows to files carrying all of them, not just one — see
// filterClause's doc comment for why that's the chosen default.
func TestListFilteredImagePaths_MultipleTagsAreANDed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	both := seedFile(t, s, "/root/both.png", 10, 10)
	onlyA := seedFile(t, s, "/root/only-a.png", 10, 10)
	onlyB := seedFile(t, s, "/root/only-b.png", 10, 10)

	seedTag(t, s, both, "a")
	seedTag(t, s, both, "b")
	seedTag(t, s, onlyA, "a")
	seedTag(t, s, onlyB, "b")

	paths, err := s.ListFilteredImagePaths(ctx, "/root/", catalogmodel.FileFilter{Tags: []string{"a", "b"}}, 0, 10)
	if err != nil {
		t.Fatalf("ListFilteredImagePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/root/both.png" {
		t.Errorf("paths = %v, want just /root/both.png", paths)
	}
}

// TestListFilteredImagePaths_RecursesIntoSubdirectories verifies a filter
// searches the whole subtree, not just prefix's direct children — the
// behavior that makes the flyout's "4 levels into a 6 level hierarchy"
// scoping work.
func TestListFilteredImagePaths_RecursesIntoSubdirectories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	top := seedFile(t, s, "/root/top.png", 10, 10)
	nested := seedFile(t, s, "/root/sub/deep/nested.png", 10, 10)
	seedTag(t, s, top, "keep")
	seedTag(t, s, nested, "keep")

	paths, err := s.ListFilteredImagePaths(ctx, "/root/", catalogmodel.FileFilter{Tags: []string{"keep"}}, 0, 10)
	if err != nil {
		t.Fatalf("ListFilteredImagePaths: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("paths = %v, want both top.png and sub/deep/nested.png", paths)
	}
}

func TestSiteCountsAndStatusCounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := seedFile(t, s, "/root/a.png", 10, 10)
	b := seedFile(t, s, "/root/b.png", 10, 10)

	seedOrigin(t, s, a, "example.com", "")
	seedOrigin(t, s, b, "example.com", "")
	seedStatus(t, s, a, "processed")

	sites, err := s.SiteCounts(ctx, "/root/")
	if err != nil {
		t.Fatalf("SiteCounts: %v", err)
	}
	if len(sites) != 1 || sites[0].Site != "example.com" || sites[0].Count != 2 {
		t.Errorf("sites = %+v, want example.com with count 2", sites)
	}

	statuses, err := s.StatusCounts(ctx, "/root/")
	if err != nil {
		t.Fatalf("StatusCounts: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != "processed" || statuses[0].Count != 1 {
		t.Errorf("statuses = %+v, want processed with count 1", statuses)
	}
}

func TestDimensionRangeAndDiscoveredRange(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a := seedFile(t, s, "/root/a.png", 100, 50)
	b := seedFile(t, s, "/root/b.png", 200, 150)

	seedOrigin(t, s, a, "example.com", "2024-01-01 00:00:00")
	seedOrigin(t, s, b, "example.com", "2024-06-15 00:00:00")

	dims, err := s.DimensionRange(ctx, "/root/")
	if err != nil {
		t.Fatalf("DimensionRange: %v", err)
	}
	want := catalogmodel.DimensionRange{WidthMin: 100, WidthMax: 200, HeightMin: 50, HeightMax: 150}
	if dims != want {
		t.Errorf("dims = %+v, want %+v", dims, want)
	}

	discovered, err := s.DiscoveredRange(ctx, "/root/")
	if err != nil {
		t.Fatalf("DiscoveredRange: %v", err)
	}
	want2 := catalogmodel.DateRange{From: "2024-01-01", To: "2024-06-15"}
	if discovered != want2 {
		t.Errorf("discovered = %+v, want %+v", discovered, want2)
	}
}
