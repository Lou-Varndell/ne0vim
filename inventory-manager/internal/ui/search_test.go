package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	invlib "inv-lib"
)

// openTestDBAt is openTestDB plus the path it opened, for tests (like the
// resolveSQLFilter ones below) that need to open a second, independent
// connection to the exact same file.
func openTestDBAt(t *testing.T) (*invlib.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inv.db")
	db, err := invlib.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestParseOptionalInt(t *testing.T) {
	if n, err := parseOptionalInt("Width min", ""); err != nil || n != 0 {
		t.Fatalf("parseOptionalInt(blank) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := parseOptionalInt("Width min", "  "); err != nil || n != 0 {
		t.Fatalf("parseOptionalInt(whitespace) = (%d, %v), want (0, nil)", n, err)
	}
	if n, err := parseOptionalInt("Width min", "1920"); err != nil || n != 1920 {
		t.Fatalf("parseOptionalInt(1920) = (%d, %v), want (1920, nil)", n, err)
	}
	if _, err := parseOptionalInt("Width min", "wide"); err == nil {
		t.Fatal("parseOptionalInt(\"wide\") = nil error, want an error")
	}
}

func TestParseOptionalDate(t *testing.T) {
	if d, err := parseOptionalDate("Discovered from", ""); err != nil || d != nil {
		t.Fatalf("parseOptionalDate(blank) = (%v, %v), want (nil, nil)", d, err)
	}
	d, err := parseOptionalDate("Discovered from", "2026-06-15")
	if err != nil {
		t.Fatalf("parseOptionalDate(2026-06-15): %v", err)
	}
	want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	if d == nil || !d.Equal(want) {
		t.Fatalf("parseOptionalDate(2026-06-15) = %v, want %v", d, want)
	}
	if _, err := parseOptionalDate("Discovered from", "06/15/2026"); err == nil {
		t.Fatal("parseOptionalDate with the wrong format = nil error, want an error")
	}
}

func TestResolveStructuredFilterTrimsAndParses(t *testing.T) {
	filter, err := resolveStructuredFilter(structuredInput{
		Tag:            " beach ",
		Site:           " example.com ",
		WidthMin:       "100",
		WidthMax:       "200",
		DiscoveredFrom: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("resolveStructuredFilter: %v", err)
	}
	if filter.Tag != "beach" || filter.Site != "example.com" {
		t.Fatalf("resolveStructuredFilter did not trim: %+v", filter)
	}
	if filter.WidthMin != 100 || filter.WidthMax != 200 {
		t.Fatalf("resolveStructuredFilter width range = %d/%d, want 100/200", filter.WidthMin, filter.WidthMax)
	}
	if filter.DiscoveredFrom == nil {
		t.Fatal("resolveStructuredFilter did not set DiscoveredFrom")
	}
	if filter.FileIDs != nil {
		t.Fatalf("resolveStructuredFilter set FileIDs = %v, want nil (structured mode must never restrict by ID)", filter.FileIDs)
	}
}

func TestResolveStructuredFilterPropagatesParseError(t *testing.T) {
	if _, err := resolveStructuredFilter(structuredInput{WidthMin: "not-a-number"}); err == nil {
		t.Fatal("resolveStructuredFilter with an invalid Width min = nil error, want an error")
	}
	if _, err := resolveStructuredFilter(structuredInput{DiscoveredFrom: "not-a-date"}); err == nil {
		t.Fatal("resolveStructuredFilter with an invalid Discovered from = nil error, want an error")
	}
}

func TestResolveSQLFilterMatchesByIDColumn(t *testing.T) {
	ctx := context.Background()
	db, path := openTestDBAt(t)

	a := createTestFile(t, db, "/photos/a.jpg")
	createTestFile(t, db, "/photos/b.jpg")

	filter, err := resolveSQLFilter(path, "SELECT id FROM files WHERE path LIKE '%a.jpg'")
	if err != nil {
		t.Fatalf("resolveSQLFilter: %v", err)
	}
	if len(filter.FileIDs) != 1 || filter.FileIDs[0] != a.ID {
		t.Fatalf("resolveSQLFilter FileIDs = %v, want [%d]", filter.FileIDs, a.ID)
	}

	// The resulting Filter must actually restrict searchRecords, proving
	// the whole SQL-box-to-grid path works end to end, not just that
	// QueryFileIDs itself returns the right IDs.
	records, total, err := searchRecords(ctx, db, filter, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if total != 1 || len(records) != 1 || records[0].Path != "/photos/a.jpg" {
		t.Fatalf("searchRecords(resolveSQLFilter result) = %v (total %d), want only /photos/a.jpg", records, total)
	}
}

func TestResolveSQLFilterZeroMatchesMeansNoResults(t *testing.T) {
	ctx := context.Background()
	db, path := openTestDBAt(t)
	createTestFile(t, db, "/photos/a.jpg")

	filter, err := resolveSQLFilter(path, "SELECT id FROM files WHERE 1 = 0")
	if err != nil {
		t.Fatalf("resolveSQLFilter: %v", err)
	}
	if filter.FileIDs == nil {
		t.Fatal("resolveSQLFilter with zero matches returned nil FileIDs, want non-nil empty (see Filter.FileIDs nil-vs-empty semantics)")
	}

	// Proving the nil-vs-empty distinction actually matters: if this
	// Filter were mistaken for an unrestricted one, every file (including
	// the one just created) would come back instead of none.
	records, _, err := searchRecords(ctx, db, filter, 0, 0)
	if err != nil {
		t.Fatalf("searchRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("searchRecords(zero-match SQL filter) = %v, want none", records)
	}
}

func TestResolveSQLFilterSurfacesError(t *testing.T) {
	_, path := openTestDBAt(t)

	if _, err := resolveSQLFilter(path, "SELECT path FROM files"); err == nil {
		t.Fatal("resolveSQLFilter with no id/file_id column = nil error, want an error")
	}
	if _, err := resolveSQLFilter(path, "DELETE FROM files"); err == nil {
		t.Fatal("resolveSQLFilter with a write statement = nil error, want it rejected by the read-only connection")
	}
}
