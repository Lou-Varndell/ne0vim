package images

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHash(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.jpg")
	b := filepath.Join(dir, "b.jpg")

	if err := os.WriteFile(a, []byte("same content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("same content"), 0o600); err != nil {
		t.Fatal(err)
	}

	hashA, err := Hash(a)
	if err != nil {
		t.Fatalf("Hash(a): %v", err)
	}
	hashB, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b): %v", err)
	}

	if hashA != hashB {
		t.Errorf("identical content hashed differently: %q != %q", hashA, hashB)
	}

	if err := os.WriteFile(b, []byte("different content"), 0o600); err != nil {
		t.Fatal(err)
	}
	hashB2, err := Hash(b)
	if err != nil {
		t.Fatalf("Hash(b) after rewrite: %v", err)
	}
	if hashA == hashB2 {
		t.Errorf("different content hashed identically: %q", hashA)
	}
}

func TestHashMissingFile(t *testing.T) {
	if _, err := Hash(filepath.Join(t.TempDir(), "missing.jpg")); err == nil {
		t.Error("expected an error for a missing file, got nil")
	}
}
