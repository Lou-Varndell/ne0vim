package process

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"image-browser/internal/manifest"
)

func TestDefaultOutput(t *testing.T) {
	got := defaultOutput("/tmp/image-manifest.json")
	want := "/tmp/processed-image-manifest.json"
	if got != want {
		t.Fatalf("defaultOutput() = %q, want %q", got, want)
	}
}

func TestIsImageFile(t *testing.T) {
	for _, path := range []string{"image.jpg", "image.JPEG", "image.png", "image.gif", "image.webp"} {
		if !isImageFile(path) {
			t.Errorf("isImageFile(%q) = false, want true", path)
		}
	}

	for _, path := range []string{"image.svg", "image.avif", "image.php", "image", ""} {
		if isImageFile(path) {
			t.Errorf("isImageFile(%q) = true, want false", path)
		}
	}
}

func TestImageDimensions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.png")

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 37, 53))
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	width, height, err := imageDimensions(path)
	if err != nil {
		t.Fatal(err)
	}
	if width != 37 || height != 53 {
		t.Fatalf("imageDimensions() = %dx%d, want 37x53", width, height)
	}
}

func TestProcessImageFileRejectsNonImage(t *testing.T) {
	width, height, err := processImageFile("example.php")
	if width != 0 || height != 0 {
		t.Fatalf("processImageFile() dimensions = %dx%d, want 0x0", width, height)
	}
	if err != errNonImage {
		t.Fatalf("processImageFile() error = %v, want %v", err, errNonImage)
	}
}

func TestClassifyFilesKeepsRecordsAndMarksStatus(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.jpg")
	if err := os.WriteFile(existing, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	data := []manifest.Entry{
		{File: existing, Status: "discovered"},
		{File: filepath.Join(dir, "missing.jpg"), Status: "discovered"},
		{File: filepath.Join(dir, "notes.php"), Status: "discovered"},
		{File: "", Status: "discovered"},
	}

	got, markedMissing, markedNonImage, markedEmpty := classifyFiles(data, 2)
	if len(got) != 4 {
		t.Fatalf("classifyFiles() returned %d records, want 4", len(got))
	}
	if markedMissing != 1 || markedNonImage != 1 || markedEmpty != 1 {
		t.Fatalf("counts = missing:%d non-image:%d empty:%d, want 1,1,1", markedMissing, markedNonImage, markedEmpty)
	}
	if got[0].Status != "discovered" {
		t.Fatalf("existing image status = %q, want discovered", got[0].Status)
	}
	if got[1].Status != "missing" {
		t.Fatalf("missing image status = %q, want missing", got[1].Status)
	}
	if got[2].Status != "non-image" {
		t.Fatalf("non-image status = %q, want non-image", got[2].Status)
	}
	if got[3].Status != "invalid" {
		t.Fatalf("empty path status = %q, want invalid", got[3].Status)
	}
}

func TestProcessImagesPreservesManifestFieldsAndMarksMissingAndNonImage(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	missingPath := filepath.Join(dir, "missing.jpg")
	otherPath := filepath.Join(dir, "notes.php")
	manifestPath := filepath.Join(dir, "image-manifest.json")
	outputPath := filepath.Join(dir, "processed-image-manifest.json")

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 17, 29))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	discovered := time.Date(2026, 8, 28, 12, 34, 56, 0, time.UTC)
	data := []manifest.Entry{
		{
			Site:         "https://example.com/page",
			SourceURL:    "https://cdn.example.com/image.png",
			File:         imagePath,
			Hash:         "abc123",
			Status:       "discovered",
			Error:        "",
			Original:     "original.png",
			DiscoveredAt: discovered,
		},
		{File: missingPath, Hash: "missing-hash", Status: "discovered", DiscoveredAt: discovered},
		{File: otherPath, Hash: "php-hash", Status: "discovered", DiscoveredAt: discovered},
	}

	manifestJSON, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := processImages(context.Background(), manifestPath, outputPath, 2, 0); err != nil {
		t.Fatal(err)
	}

	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	var got []manifest.Entry
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("output records = %d, want 3", len(got))
	}

	if got[0].Width != 17 || got[0].Height != 29 || got[0].Status != "processed" {
		t.Errorf("image record = %#v, want processed 17x29", got[0])
	}
	if got[0].Hash != "abc123" || got[0].Original != "original.png" || got[0].Site != "https://example.com/page" {
		t.Errorf("image record lost manifest fields: %#v", got[0])
	}
	if !got[0].DiscoveredAt.Equal(discovered) {
		t.Errorf("DiscoveredAt = %v, want %v", got[0].DiscoveredAt, discovered)
	}
	if got[1].Status != "missing" {
		t.Errorf("missing record status = %q, want missing", got[1].Status)
	}
	if got[1].Hash != "missing-hash" {
		t.Errorf("missing record hash = %q, want missing-hash", got[1].Hash)
	}
	if got[2].Status != "non-image" {
		t.Errorf("non-image record status = %q, want non-image", got[2].Status)
	}
	if got[2].Hash != "php-hash" {
		t.Errorf("non-image record hash = %q, want php-hash", got[2].Hash)
	}
}
