package scraper

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp writes content to dir/name and returns a downloadedFile
// describing it, as if it had just been downloaded.
func writeTemp(t *testing.T, dir, name string, content []byte) downloadedFile {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return downloadedFile{
		work: work{site: "https://example.com", sourceURL: "https://example.com/" + name},
		name: name,
		path: path,
		size: int64(len(content)),
	}
}

func TestDedupeDropsIdenticalFilesSameName(t *testing.T) {
	dir := t.TempDir()

	// Two distinct on-disk files (as download.go's collision-safe temp
	// naming would produce), sharing the same logical name and content.
	files := []downloadedFile{
		writeTemp(t, dir, "a.jpg", []byte("same-bytes")),
		{work: work{sourceURL: "https://example.com/a-2.jpg"}, name: "a.jpg", path: writeSibling(t, dir, "a-2.jpg", []byte("same-bytes")), size: int64(len("same-bytes"))},
	}

	survivors, dupes, err := dedupe(files)
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}

	if len(survivors) != 1 {
		t.Fatalf("got %d survivors, want 1: %+v", len(survivors), survivors)
	}
	if len(dupes) != 1 {
		t.Fatalf("got %d dupes, want 1: %+v", len(dupes), dupes)
	}
	if survivors[0].hash != dupes[0].ofHash {
		t.Errorf("dupe.ofHash %q != survivor hash %q", dupes[0].ofHash, survivors[0].hash)
	}
}

func writeSibling(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestDedupeKeepsSameNameDifferentContent(t *testing.T) {
	dir := t.TempDir()
	files := []downloadedFile{
		writeTemp(t, dir, "a.jpg", []byte("content-one")),
		{work: work{sourceURL: "https://example.com/a-2.jpg"}, name: "a.jpg", path: writeSibling(t, dir, "a-2.jpg", []byte("content-two-different")), size: int64(len("content-two-different"))},
	}

	survivors, dupes, err := dedupe(files)
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}

	if len(survivors) != 2 {
		t.Fatalf("got %d survivors, want 2 (same name, different content, different size): %+v", len(survivors), survivors)
	}
	if len(dupes) != 0 {
		t.Fatalf("got %d dupes, want 0: %+v", len(dupes), dupes)
	}
}

func TestDedupeMissesCrossNameDuplicates(t *testing.T) {
	// Documents the accepted limitation: identical content under different
	// logical names is NOT deduplicated.
	dir := t.TempDir()
	files := []downloadedFile{
		writeTemp(t, dir, "a.jpg", []byte("identical-bytes")),
		writeTemp(t, dir, "b.jpg", []byte("identical-bytes")),
	}

	survivors, dupes, err := dedupe(files)
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}

	if len(survivors) != 2 {
		t.Fatalf("got %d survivors, want 2 (dedup only compares within a shared name): %+v", len(survivors), survivors)
	}
	if len(dupes) != 0 {
		t.Fatalf("got %d dupes, want 0: %+v", len(dupes), dupes)
	}
}

func TestDedupeSameNameSameSizeDifferentContent(t *testing.T) {
	// A size collision without a content match must not be flagged as a
	// duplicate: the hash stage is what actually confirms duplication.
	dir := t.TempDir()
	files := []downloadedFile{
		writeTemp(t, dir, "a.jpg", []byte("AAAAAAAAAA")),
		{work: work{sourceURL: "https://example.com/a-2.jpg"}, name: "a.jpg", path: writeSibling(t, dir, "a-2.jpg", []byte("BBBBBBBBBB")), size: 10},
	}

	survivors, dupes, err := dedupe(files)
	if err != nil {
		t.Fatalf("dedupe: %v", err)
	}

	if len(survivors) != 2 {
		t.Fatalf("got %d survivors, want 2: %+v", len(survivors), survivors)
	}
	if len(dupes) != 0 {
		t.Fatalf("got %d dupes, want 0: %+v", len(dupes), dupes)
	}
}
