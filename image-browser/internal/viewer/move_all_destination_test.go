package viewer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMoveAllDestinationAcceptsNonexistentPath(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "not-yet-created", "nested")

	resolved, err := resolveMoveAllDestination(dest)
	if err != nil {
		t.Fatalf("resolveMoveAllDestination(%q) returned error: %v", dest, err)
	}
	if resolved != dest {
		t.Fatalf("resolveMoveAllDestination(%q) = %q, want %q", dest, resolved, dest)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("resolveMoveAllDestination must not create the destination; stat err = %v", err)
	}
}

func TestResolveMoveAllDestinationAcceptsExistingDirectory(t *testing.T) {
	dir := t.TempDir()

	resolved, err := resolveMoveAllDestination(dir)
	if err != nil {
		t.Fatalf("resolveMoveAllDestination(%q) returned error: %v", dir, err)
	}
	if resolved != dir {
		t.Fatalf("resolveMoveAllDestination(%q) = %q, want %q", dir, resolved, dir)
	}
}

func TestResolveMoveAllDestinationRejectsExistingFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveMoveAllDestination(file); err == nil {
		t.Fatalf("resolveMoveAllDestination(%q) = nil error, want a rejection", file)
	}
}

func TestResolveMoveAllDestinationTrimsAndRejectsEmpty(t *testing.T) {
	dir := t.TempDir()

	resolved, err := resolveMoveAllDestination("  " + dir + "  ")
	if err != nil {
		t.Fatalf("resolveMoveAllDestination with surrounding whitespace returned error: %v", err)
	}
	if resolved != dir {
		t.Fatalf("resolveMoveAllDestination with surrounding whitespace = %q, want %q", resolved, dir)
	}

	if _, err := resolveMoveAllDestination("   "); err == nil {
		t.Fatal("resolveMoveAllDestination(\"   \") = nil error, want a rejection")
	}
}

func TestResolveMoveAllDestinationExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := resolveMoveAllDestination("~/move-all-destination-test-dir")
	if err != nil {
		t.Fatalf("resolveMoveAllDestination(~/...) returned error: %v", err)
	}
	want := filepath.Join(home, "move-all-destination-test-dir")
	if resolved != want {
		t.Fatalf("resolveMoveAllDestination(~/...) = %q, want %q", resolved, want)
	}
}

func TestNearestExistingDirWalksUpToExistingAncestor(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "missing", "also-missing")

	if got := nearestExistingDir(nested); got != dir {
		t.Fatalf("nearestExistingDir(%q) = %q, want %q", nested, got, dir)
	}
}

func TestNearestExistingDirReturnsPathItselfWhenItExists(t *testing.T) {
	dir := t.TempDir()

	if got := nearestExistingDir(dir); got != dir {
		t.Fatalf("nearestExistingDir(%q) = %q, want %q", dir, got, dir)
	}
}
