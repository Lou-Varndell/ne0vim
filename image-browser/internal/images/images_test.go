package images

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	dest := filepath.Join(dir, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, duplicate, err := ResolveDestination(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate || filepath.Base(path) != "source.jpg" {
		t.Fatalf("unexpected destination: %q duplicate=%v", path, duplicate)
	}
}

func TestResolveDestinationRenamesFilenameCollisionWithDifferentContent(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	dest := filepath.Join(dir, "dest")

	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "source.jpg"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, duplicate, err := ResolveDestination(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("ResolveDestination() reported duplicate for different content")
	}
	if got, want := filepath.Base(path), "source-2.jpg"; got != want {
		t.Fatalf("ResolveDestination() path = %q, want %q", got, want)
	}
}

func TestResolveDestinationSkipsSameHashWithSameFilename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	dest := filepath.Join(dir, "dest")

	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("same image content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "source.jpg"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	path, duplicate, err := ResolveDestination(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate {
		t.Fatalf("ResolveDestination() duplicate = false, want true; path=%q", path)
	}
	if path != "" {
		t.Fatalf("ResolveDestination() path = %q for duplicate, want empty", path)
	}
}

func TestResolveDestinationSkipsSameHashWithDifferentFilename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	dest := filepath.Join(dir, "dest")

	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	content := []byte("same image content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "already-there.jpg"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	path, duplicate, err := ResolveDestination(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate {
		t.Fatalf("ResolveDestination() duplicate = false, want true; path=%q", path)
	}
	if path != "" {
		t.Fatalf("ResolveDestination() path = %q for duplicate, want empty", path)
	}
}

func TestResolveDestinationIgnoresDifferentSizedFiles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	dest := filepath.Join(dir, "dest")

	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(src, []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "different.jpg"), []byte("different size"), 0o644); err != nil {
		t.Fatal(err)
	}

	path, duplicate, err := ResolveDestination(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate {
		t.Fatal("ResolveDestination() reported duplicate for different-sized content")
	}
	if filepath.Base(path) != "source.jpg" {
		t.Fatalf("ResolveDestination() path = %q, want source.jpg", filepath.Base(path))
	}
}

func TestFindSkipsUnreadableSubdir(t *testing.T) {
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

	ok := filepath.Join(root, "ok")
	if err := os.Mkdir(ok, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ok, "visible.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	matches, err := Find(root, "")
	if err != nil {
		t.Fatalf("Find returned an error instead of skipping the unreadable subdir: %v", err)
	}
	if len(matches) != 1 || filepath.Base(matches[0]) != "visible.jpg" {
		t.Fatalf("expected only visible.jpg, got %v", matches)
	}
}

func TestMove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.jpg")
	destDir := filepath.Join(dir, "dest")
	dest := filepath.Join(destDir, "source.jpg")

	if err := os.Mkdir(destDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Move(src, dest); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source still exists or unexpected error: %v", err)
	}
	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "image" {
		t.Fatalf("destination content = %q, want image", content)
	}
}
