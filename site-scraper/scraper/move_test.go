package scraper

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveDestNameNoCollision(t *testing.T) {
	dir := t.TempDir()

	got := resolveDestName(dir, "a.jpg")
	if got != "a.jpg" {
		t.Errorf("got %q, want %q", got, "a.jpg")
	}
}

func TestResolveDestNameDisambiguatesCollisions(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.jpg"))
	mustWrite(t, filepath.Join(dir, "a-2.jpg"))

	got := resolveDestName(dir, "a.jpg")
	if got != "a-3.jpg" {
		t.Errorf("got %q, want %q", got, "a-3.jpg")
	}
}

func TestMoveToDestinationNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "a.jpg")
	mustWrite(t, existing)
	original, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing: %v", err)
	}

	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "incoming.jpg")
	if err := os.WriteFile(src, []byte("new content"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}

	finalName, err := moveToDestination(dir, src, "a.jpg")
	if err != nil {
		t.Fatalf("moveToDestination: %v", err)
	}
	if finalName != "a-2.jpg" {
		t.Fatalf("got finalName %q, want %q", finalName, "a-2.jpg")
	}

	stillThere, err := os.ReadFile(existing)
	if err != nil {
		t.Fatalf("read existing after move: %v", err)
	}
	if string(stillThere) != string(original) {
		t.Errorf("existing file at %s was overwritten", existing)
	}

	if fileExists(src) {
		t.Errorf("src %s should have been moved, not copied", src)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
