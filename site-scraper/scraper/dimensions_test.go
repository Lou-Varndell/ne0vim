package scraper

import (
	"os"
	"testing"
)

func TestDecodeDimensionsWebP(t *testing.T) {
	// Confirms the golang.org/x/image/webp blank import actually registers
	// with image.DecodeConfig — the risk plan.md flagged.
	w, h, ok := decodeDimensions("testdata/dims.webp")
	if !ok {
		t.Fatal("decodeDimensions: ok = false for a valid WebP fixture")
	}
	if w != 6 || h != 5 {
		t.Errorf("got %dx%d, want 6x5", w, h)
	}
}

func TestDecodeDimensionsUnsupportedFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/not-an-image.txt"
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, _, ok := decodeDimensions(path)
	if ok {
		t.Error("decodeDimensions: ok = true for a non-image file, want false")
	}
}
