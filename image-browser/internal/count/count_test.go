package count

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCountImages(t *testing.T) {
	root := t.TempDir()

	files := []string{
		"one.jpg", "two.JPG", "three.png", "four.webp",
		"not-an-image.txt", "nested/five.jpeg", "nested/six.HEIC",
	}

	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, _, err := inspectDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := 6; got != want {
		t.Fatalf("inspectDirectory() image count = %d, want %d", got, want)
	}
}

func TestInspectDirectorySkipsUnreadableSubdir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission restriction is not portable to windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}

	root := t.TempDir()

	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked, "hidden.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o755) })

	if err := os.WriteFile(filepath.Join(root, "visible.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	gotCount, gotSubdirs, err := inspectDirectory(root)
	if err != nil {
		t.Fatalf("inspectDirectory returned an error instead of skipping the unreadable subdir: %v", err)
	}
	if gotCount != 1 {
		t.Fatalf("inspectDirectory() image count = %d, want 1", gotCount)
	}
	if !gotSubdirs {
		t.Fatal("inspectDirectory() hasSubdirs = false, want true")
	}
}

func TestParseImageCountFilter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMin int
		wantMax int
		wantNil bool
		wantErr bool
	}{
		{name: "empty", input: "", wantNil: true},
		{name: "exact", input: "30", wantMin: 30, wantMax: 30},
		{name: "range", input: "10,30", wantMin: 10, wantMax: 30},
		{name: "equal range", input: "10,10", wantMin: 10, wantMax: 10},
		{name: "zero", input: "0", wantMin: 0, wantMax: 0},
		{name: "spaces", input: " 10, 30 ", wantMin: 10, wantMax: 30},
		{name: "too many values", input: "10,20,30", wantErr: true},
		{name: "missing minimum", input: ",30", wantErr: true},
		{name: "missing maximum", input: "10,", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
		{name: "minimum greater than maximum", input: "30,10", wantErr: true},
		{name: "not a number", input: "abc", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseImageCountFilter(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Fatal("parseImageCountFilter() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("parseImageCountFilter() error = %v", err)
			}

			if tt.wantNil {
				if got != nil {
					t.Fatalf("parseImageCountFilter() = %#v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("parseImageCountFilter() = nil, want filter")
			}

			if got.min != tt.wantMin || got.max != tt.wantMax {
				t.Fatalf("parseImageCountFilter() = [%d,%d], want [%d,%d]",
					got.min, got.max, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestImageCountFilterMatches(t *testing.T) {
	tests := []struct {
		name   string
		filter imageCountFilter
		count  int
		want   bool
	}{
		{name: "exact match", filter: imageCountFilter{min: 30, max: 30}, count: 30, want: true},
		{name: "below exact", filter: imageCountFilter{min: 30, max: 30}, count: 29, want: false},
		{name: "above exact", filter: imageCountFilter{min: 30, max: 30}, count: 31, want: false},
		{name: "range lower bound", filter: imageCountFilter{min: 10, max: 30}, count: 10, want: true},
		{name: "range upper bound", filter: imageCountFilter{min: 10, max: 30}, count: 30, want: true},
		{name: "range middle", filter: imageCountFilter{min: 10, max: 30}, count: 20, want: true},
		{name: "range below", filter: imageCountFilter{min: 10, max: 30}, count: 9, want: false},
		{name: "range above", filter: imageCountFilter{min: 10, max: 30}, count: 31, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.matches(tt.count); got != tt.want {
				t.Fatalf("matches(%d) = %v, want %v", tt.count, got, tt.want)
			}
		})
	}
}

func TestInspectDirectory(t *testing.T) {
	root := t.TempDir()

	files := []string{
		"one.jpg",
		"two.png",
		"nested/three.webp",
		"nested/deeper/four.jpeg",
		"not-an-image.txt",
	}

	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gotCount, gotSubdirs, err := inspectDirectory(root)
	if err != nil {
		t.Fatal(err)
	}

	if want := 4; gotCount != want {
		t.Fatalf("inspectDirectory() image count = %d, want %d", gotCount, want)
	}

	if !gotSubdirs {
		t.Fatal("inspectDirectory() hasSubdirs = false, want true")
	}
}

func TestInspectDirectoryWithoutSubdirectories(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{"one.jpg", "two.png"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	gotCount, gotSubdirs, err := inspectDirectory(root)
	if err != nil {
		t.Fatal(err)
	}

	if want := 2; gotCount != want {
		t.Fatalf("inspectDirectory() image count = %d, want %d", gotCount, want)
	}

	if gotSubdirs {
		t.Fatal("inspectDirectory() hasSubdirs = true, want false")
	}
}
