package images

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanListsOnlyImagesDirectlyInDir(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("b.png")
	writeFile("a.JPG")
	writeFile("notes.txt")

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	items, err := Scan(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(items) != 2 {
		t.Fatalf("Scan() returned %d items, want 2: %+v", len(items), items)
	}
	if items[0].Name != "a.JPG" || items[1].Name != "b.png" {
		t.Fatalf("Scan() order = [%s, %s], want case-insensitive [a.JPG, b.png]", items[0].Name, items[1].Name)
	}
	if items[0].Path != filepath.Join(dir, "a.JPG") {
		t.Fatalf("Scan() path = %q, want %q", items[0].Path, filepath.Join(dir, "a.JPG"))
	}
}

func TestCountImagesCountsOnlyDirectImages(t *testing.T) {
	dir := t.TempDir()

	writeFile := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("a.jpg")
	writeFile("b.PNG")
	writeFile("notes.txt")

	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	count, err := CountImages(dir)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("CountImages() = %d, want 2", count)
	}
}

func TestCountImagesReturnsErrorForMissingDir(t *testing.T) {
	if _, err := CountImages(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("CountImages() error = nil, want error for missing directory")
	}
}

func TestScanReturnsErrorForMissingDir(t *testing.T) {
	if _, err := Scan(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Scan() error = nil, want error for missing directory")
	}
}

func TestScanRespectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Scan(ctx, t.TempDir()); err == nil {
		t.Fatal("Scan() error = nil, want context.Canceled")
	}
}
