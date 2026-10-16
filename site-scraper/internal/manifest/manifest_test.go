package manifest

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMergeCreatesFileWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")

	got, err := Merge(path, []Entry{
		{Site: "https://example.com", SourceURL: "https://example.com/a.jpg", Status: "kept"},
	})
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}

	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(got.Entries))
	}
	if got.Entries[0].SourceURL != "https://example.com/a.jpg" {
		t.Errorf("got SourceURL %q", got.Entries[0].SourceURL)
	}
}

func TestMergeUpsertsBySourceURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")

	if _, err := Merge(path, []Entry{
		{SourceURL: "https://example.com/a.jpg", Status: "kept", File: "a.jpg"},
		{SourceURL: "https://example.com/b.jpg", Status: "kept", File: "b.jpg"},
	}); err != nil {
		t.Fatalf("first Merge: %v", err)
	}

	got, err := Merge(path, []Entry{
		{SourceURL: "https://example.com/a.jpg", Status: "download-failed:timeout"},
	})
	if err != nil {
		t.Fatalf("second Merge: %v", err)
	}

	if len(got.Entries) != 2 {
		t.Fatalf("got %d entries after upsert, want 2 (no duplication, no loss): %+v", len(got.Entries), got.Entries)
	}

	byURL := make(map[string]Entry, len(got.Entries))
	for _, e := range got.Entries {
		byURL[e.SourceURL] = e
	}

	if byURL["https://example.com/a.jpg"].Status != "download-failed:timeout" {
		t.Errorf("a.jpg entry not replaced: %+v", byURL["https://example.com/a.jpg"])
	}
	if byURL["https://example.com/b.jpg"].Status != "kept" {
		t.Errorf("b.jpg entry lost or altered: %+v", byURL["https://example.com/b.jpg"])
	}
}

func TestMergePersistsAcrossCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")

	if _, err := Merge(path, []Entry{
		{SourceURL: "https://example.com/a.jpg", Status: "kept", DiscoveredAt: time.Now()},
	}); err != nil {
		t.Fatalf("first Merge: %v", err)
	}

	// A fresh Merge call must read what the previous call wrote to disk,
	// not rely on any in-memory state.
	got, err := Merge(path, nil)
	if err != nil {
		t.Fatalf("second Merge: %v", err)
	}

	if len(got.Entries) != 1 {
		t.Fatalf("got %d entries, want 1 persisted from disk", len(got.Entries))
	}
}
