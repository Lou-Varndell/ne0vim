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

// decodeWrapper decodes raw as the entries-wrapper shape process now always
// writes (see writeJSON), failing the test if raw isn't in that shape.
func decodeWrapper(t *testing.T, raw []byte) []manifest.Entry {
	t.Helper()
	var w manifest.Wrapper
	if err := json.Unmarshal(raw, &w); err != nil {
		t.Fatalf("output is not a valid entries wrapper: %v", err)
	}
	return w.Entries
}

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

func TestProcessImagesPreservesManifestFieldsRemovesMissingAndMarksNonImage(t *testing.T) {
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

	got := decodeWrapper(t, output)
	// The missing.jpg record is removed entirely rather than kept with a
	// "missing" status: once process confirms a manifest entry's file no
	// longer exists on disk, the entry itself is dropped from the output.
	if len(got) != 2 {
		t.Fatalf("output records = %d, want 2 (missing record should have been removed): %#v", len(got), got)
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
	if got[1].Status != "non-image" {
		t.Errorf("non-image record status = %q, want non-image", got[1].Status)
	}
	if got[1].Hash != "php-hash" {
		t.Errorf("non-image record hash = %q, want php-hash", got[1].Hash)
	}
}

func TestProcessImagesAcceptsEntriesWrapperManifest(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	manifestPath := filepath.Join(dir, "manifest.json")
	outputPath := filepath.Join(dir, "processed-manifest.json")

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 10, 20))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	manifestJSON := `{"entries":[{"site":"s","source_url":"u","file":"` + imagePath + `","status":"kept"}]}`
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := processImages(context.Background(), manifestPath, outputPath, 2, 0); err != nil {
		t.Fatal(err)
	}

	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}

	got := decodeWrapper(t, output)
	if len(got) != 1 {
		t.Fatalf("output records = %d, want 1", len(got))
	}
	if got[0].Width != 10 || got[0].Height != 20 || got[0].Status != manifest.StatusProcessed {
		t.Errorf("record = %#v, want processed 10x20", got[0])
	}
}

func TestRunProcessesManifestFilesRecursively(t *testing.T) {
	root := t.TempDir()

	writeManifestDir := func(subdir, manifestName string, width, height int) string {
		dir := filepath.Join(root, subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		imagePath := filepath.Join(dir, "image.png")
		file, err := os.Create(imagePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}

		manifestJSON, err := json.Marshal([]manifest.Entry{{File: imagePath, Status: "discovered"}})
		if err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(dir, manifestName)
		if err := os.WriteFile(manifestPath, manifestJSON, 0o644); err != nil {
			t.Fatal(err)
		}
		return manifestPath
	}

	writeManifestDir("a", "image-manifest.json", 11, 22)
	writeManifestDir("b", "manifest.json", 33, 44)

	if err := Run([]string{"-input", root}); err != nil {
		t.Fatal(err)
	}

	readProcessed := func(path string) []manifest.Entry {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return decodeWrapper(t, data)
	}

	gotA := readProcessed(filepath.Join(root, "a", "processed-image-manifest.json"))
	if len(gotA) != 1 || gotA[0].Width != 11 || gotA[0].Height != 22 || gotA[0].Status != manifest.StatusProcessed {
		t.Fatalf("a manifest result = %#v, want processed 11x22", gotA)
	}

	gotB := readProcessed(filepath.Join(root, "b", "processed-manifest.json"))
	if len(gotB) != 1 || gotB[0].Width != 33 || gotB[0].Height != 44 || gotB[0].Status != manifest.StatusProcessed {
		t.Fatalf("b manifest result = %#v, want processed 33x44", gotB)
	}
}

func TestRunRejectsOutputFlagWithMultipleManifestFiles(t *testing.T) {
	root := t.TempDir()
	for _, subdir := range []string{"a", "b"} {
		dir := filepath.Join(root, subdir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		manifestJSON, err := json.Marshal([]manifest.Entry{})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), manifestJSON, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	err := Run([]string{"-input", root, "-output", filepath.Join(root, "out.json")})
	if err == nil {
		t.Fatal("expected an error when -output is combined with multiple discovered manifest files")
	}
}

func TestNormalizeFilePathsRewritesBareFilenamesOnly(t *testing.T) {
	manifestDir := "/Users/louisvarndell-local/Images/mlsgrid_sync/photos/laksr.v2"
	data := []manifest.Entry{
		{File: "001.jpg"},
		{File: "/already/absolute/002.jpg"},
		{File: ""},
	}

	normalized := normalizeFilePaths(data, manifestDir)
	if normalized != 1 {
		t.Fatalf("normalizeFilePaths() = %d, want 1", normalized)
	}

	want := filepath.Join(manifestDir, "001.jpg")
	if data[0].File != want {
		t.Errorf("data[0].File = %q, want %q", data[0].File, want)
	}
	if data[1].File != "/already/absolute/002.jpg" {
		t.Errorf("data[1].File = %q, want unchanged", data[1].File)
	}
	if data[2].File != "" {
		t.Errorf("data[2].File = %q, want unchanged empty string", data[2].File)
	}
}

func TestProcessImagesNormalizesBareFilenameToAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "001.jpg")
	manifestPath := filepath.Join(dir, "image-manifest.json")
	outputPath := filepath.Join(dir, "processed-image-manifest.json")

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 5, 5))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	// file is a bare filename, as `view -move-all`'s manifest sync writes
	// one when the manifest is co-located with its images.
	manifestJSON, err := json.Marshal([]manifest.Entry{{File: "001.jpg"}})
	if err != nil {
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

	got := decodeWrapper(t, output)
	if len(got) != 1 {
		t.Fatalf("output records = %d, want 1", len(got))
	}
	if got[0].File != imagePath {
		t.Fatalf("got[0].File = %q, want %q (bare filename normalized to absolute)", got[0].File, imagePath)
	}
	if got[0].Status != manifest.StatusProcessed {
		t.Fatalf("got[0].Status = %q, want %q", got[0].Status, manifest.StatusProcessed)
	}
}

// TestProcessImagesRemovesEntryOutsideManifestDirectory guards the "move"
// and view "Move All" invariant from the other side: a manifest only ever
// describes images in its own directory or a subdirectory of it, so an
// entry whose File lives elsewhere — the shape left behind by an older
// version of this program that rewrote a moved entry's File in place
// instead of removing it — must be dropped, not merely normalized or
// processed as if it belonged.
func TestProcessImagesRemovesEntryOutsideManifestDirectory(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "001.jpg")
	manifestPath := filepath.Join(dir, "image-manifest.json")
	outputPath := filepath.Join(dir, "processed-image-manifest.json")

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 5, 5))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "elsewhere.jpg")
	if err := os.WriteFile(outsidePath, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestJSON, err := json.Marshal([]manifest.Entry{
		{File: imagePath, Site: "in-tree"},
		{File: outsidePath, Site: "out-of-tree"},
	})
	if err != nil {
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

	got := decodeWrapper(t, output)
	if len(got) != 1 {
		t.Fatalf("output records = %d, want 1 (out-of-tree entry should be removed): %+v", len(got), got)
	}
	if got[0].Site != "in-tree" {
		t.Fatalf("got[0].Site = %q, want %q", got[0].Site, "in-tree")
	}
}

// TestProcessImagesRemovesEntryWithEmptyFile guards the gap that let a
// manifest with no File at all for an entry (status "invalid") survive
// process indefinitely: classifyFiles already marked it invalid, but
// nothing removed it the way a confirmed-missing file is removed — so it
// kept getting handed to downstream tools like move -preview, which has
// no file to resolve for it (see move.go's previewPaths).
func TestProcessImagesRemovesEntryWithEmptyFile(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "image.png")
	manifestPath := filepath.Join(dir, "image-manifest.json")
	outputPath := filepath.Join(dir, "processed-image-manifest.json")

	file, err := os.Create(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 9, 9))); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	manifestJSON, err := json.Marshal([]manifest.Entry{
		{File: imagePath, Site: "has-file"},
		{File: "", Site: "no-file"},
	})
	if err != nil {
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

	got := decodeWrapper(t, output)
	if len(got) != 1 {
		t.Fatalf("output records = %d, want 1 (empty-file entry should be removed): %+v", len(got), got)
	}
	if got[0].Site != "has-file" {
		t.Fatalf("got[0].Site = %q, want %q", got[0].Site, "has-file")
	}
}
