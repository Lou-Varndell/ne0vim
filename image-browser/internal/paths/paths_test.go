package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "bare tilde", input: "~", want: home},
		{name: "tilde slash path", input: "~/Images/vacation", want: filepath.Join(home, "Images/vacation")},
		{name: "unrelated absolute path", input: "/var/tmp", want: "/var/tmp"},
		{name: "relative path is unchanged", input: "relative/path", want: "relative/path"},
		{name: "embedded tilde is not expanded", input: "/a/~/b", want: "/a/~/b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Expand(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAbsExpandsTildeAndResolvesRelative(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	got, err := Abs("~/Images")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "Images")
	if got != want {
		t.Fatalf("Abs(~/Images) = %q, want %q", got, want)
	}

	if _, err := Abs("relative"); err != nil {
		t.Fatalf("Abs(relative) returned an error: %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, "Images")
	if got := DefaultRoot(); got != want {
		t.Fatalf("DefaultRoot() = %q, want %q", got, want)
	}
}

func TestRelOrPath(t *testing.T) {
	root := string(os.PathSeparator) + filepath.Join("root", "images")

	tests := []struct {
		name string
		path string
		root string
		want string
	}{
		{
			name: "path below root becomes relative",
			path: filepath.Join(root, "sub", "file.jpg"),
			root: root,
			want: filepath.Join("sub", "file.jpg"),
		},
		{
			name: "path equal to root becomes dot",
			path: root,
			root: root,
			want: ".",
		},
		{
			name: "path outside root is unchanged",
			path: string(os.PathSeparator) + filepath.Join("other", "file.jpg"),
			root: root,
			want: string(os.PathSeparator) + filepath.Join("other", "file.jpg"),
		},
		{
			name: "sibling directory with shared prefix is unchanged",
			path: string(os.PathSeparator) + filepath.Join("root", "images-archive", "file.jpg"),
			root: root,
			want: string(os.PathSeparator) + filepath.Join("root", "images-archive", "file.jpg"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RelOrPath(tt.path, tt.root); got != tt.want {
				t.Fatalf("RelOrPath(%q, %q) = %q, want %q", tt.path, tt.root, got, tt.want)
			}
		})
	}
}
