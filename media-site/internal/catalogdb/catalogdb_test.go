package catalogdb

import (
	"fmt"
	"path/filepath"
	"testing"
)

// newTestDB opens a fresh catalog index backed by a temp-dir SQLite file,
// closed automatically at the end of the test.
func newTestDB(t *testing.T) *DB {
	t.Helper()

	db, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestListImages_Paginates verifies offset and limit slice a directory's
// images in name order, matching the window loadImageBatch expects when
// paging through an infinite-scroll continuation.
func TestListImages_Paginates(t *testing.T) {
	db := newTestDB(t)

	// Insert out of order, with names that sort img00..img09, to confirm
	// ordering (not insertion order) determines the page boundaries.
	for i := 9; i >= 0; i-- {
		name := fmt.Sprintf("img%02d.png", i)
		if err := db.InsertImage(name, name, ""); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
	}

	first, err := db.ListImages("", 0, 4)
	if err != nil {
		t.Fatalf("ListImages first page: %v", err)
	}
	if got := namesOf(first); !equal(got, []string{"img00.png", "img01.png", "img02.png", "img03.png"}) {
		t.Errorf("first page = %v", got)
	}

	second, err := db.ListImages("", 4, 4)
	if err != nil {
		t.Fatalf("ListImages second page: %v", err)
	}
	if got := namesOf(second); !equal(got, []string{"img04.png", "img05.png", "img06.png", "img07.png"}) {
		t.Errorf("second page = %v", got)
	}

	last, err := db.ListImages("", 8, 4)
	if err != nil {
		t.Fatalf("ListImages last page: %v", err)
	}
	if got := namesOf(last); !equal(got, []string{"img08.png", "img09.png"}) {
		t.Errorf("last (partial) page = %v, want 2 remaining images", got)
	}

	beyond, err := db.ListImages("", 10, 4)
	if err != nil {
		t.Fatalf("ListImages beyond end: %v", err)
	}
	if len(beyond) != 0 {
		t.Errorf("page beyond end = %v, want empty", beyond)
	}
}

func namesOf(images []Image) []string {
	names := make([]string, len(images))
	for i, img := range images {
		names[i] = img.Name
	}
	return names
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
