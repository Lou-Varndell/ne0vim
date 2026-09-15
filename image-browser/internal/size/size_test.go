package size

import (
	"encoding/json"
	"image-browser/internal/manifest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadGroupsByResolutionAndDeduplicatesFiles(t *testing.T) {
	root := t.TempDir()
	manifestFile := filepath.Join(root, "image-manifest.json")

	existingA := filepath.Join(root, "a.jpg")
	existingB := filepath.Join(root, "b.jpg")
	existingC := filepath.Join(root, "c.jpg")
	missing := filepath.Join(root, "missing.jpg")

	for _, path := range []string{existingA, existingB, existingC} {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	records := []manifest.Entry{
		{File: existingB, Status: "processed", Width: 400, Height: 600},
		{File: existingA, Status: "processed", Width: 400, Height: 600},
		{File: existingA, Status: "processed", Width: 400, Height: 600},
		{File: existingC, Status: "processed", Width: 800, Height: 600},
		{File: missing, Status: "missing", Width: 800, Height: 600},
		{File: existingC, Status: "non-image", Width: 800, Height: 600},
		{File: existingC, Status: "failed", Width: 800, Height: 600},
		{File: existingC, Status: "processed", Width: 0, Height: 600},
	}

	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(manifestFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(groups) != 2 {
		t.Fatalf("Load() returned %d groups, want 2", len(groups))
	}

	SortGroups(groups, false)

	want := []Group{
		{
			Width:  400,
			Height: 600,
			Files:  []string{existingA, existingB},
		},
		{
			Width:  800,
			Height: 600,
			Files:  []string{existingC},
		},
	}

	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

func TestLoadExcludesMissingFiles(t *testing.T) {
	root := t.TempDir()
	manifestFile := filepath.Join(root, "image-manifest.json")

	existing := filepath.Join(root, "existing.jpg")
	missing := filepath.Join(root, "missing.jpg")

	if err := os.WriteFile(existing, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}

	records := []manifest.Entry{
		{
			File:   existing,
			Status: "processed",
			Width:  100,
			Height: 100,
		},
		{
			File:   missing,
			Status: "processed",
			Width:  100,
			Height: 100,
		},
	}

	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(manifestFile)
	if err != nil {
		t.Fatal(err)
	}

	want := []Group{
		{
			Width:  100,
			Height: 100,
			Files:  []string{existing},
		},
	}

	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

func TestLoadExcludesNonProcessedRecords(t *testing.T) {
	root := t.TempDir()
	manifestFile := filepath.Join(root, "image-manifest.json")

	processed := filepath.Join(root, "processed.jpg")
	missing := filepath.Join(root, "missing.jpg")
	nonImage := filepath.Join(root, "page.php")
	failed := filepath.Join(root, "failed.jpg")

	for _, path := range []string{processed, missing, nonImage, failed} {
		if err := os.WriteFile(path, []byte("test"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	records := []manifest.Entry{
		{
			File:   processed,
			Status: "processed",
			Width:  200,
			Height: 300,
		},
		{
			File:   missing,
			Status: "missing",
			Width:  200,
			Height: 300,
		},
		{
			File:   nonImage,
			Status: "non-image",
			Width:  200,
			Height: 300,
		},
		{
			File:   failed,
			Status: "failed",
			Width:  200,
			Height: 300,
		},
	}

	data, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(manifestFile, data, 0o644); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(manifestFile)
	if err != nil {
		t.Fatal(err)
	}

	want := []Group{
		{
			Width:  200,
			Height: 300,
			Files:  []string{processed},
		},
	}

	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("Load() groups = %#v, want %#v", groups, want)
	}
}

func TestSortGroupsLargestFirst(t *testing.T) {
	groups := []Group{
		{Width: 400, Height: 600},
		{Width: 1800, Height: 1200},
		{Width: 800, Height: 600},
	}

	SortGroups(groups, true)

	want := []Group{
		{Width: 1800, Height: 1200},
		{Width: 800, Height: 600},
		{Width: 400, Height: 600},
	}

	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("SortGroups() = %v, want %v", groups, want)
	}
}

func intPtr(v int) *int {
	return &v
}
