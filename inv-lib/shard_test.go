package invlib

import (
	"path/filepath"
	"testing"
)

// fill calls Dir/Advance n times against s and returns the shard directory
// each of those n placements landed in.
func fill(t *testing.T, s *Sharder, n int) []string {
	t.Helper()
	dirs := make([]string, n)
	for i := range n {
		dir, err := s.Dir()
		if err != nil {
			t.Fatalf("Dir: %v", err)
		}
		s.Advance()
		dirs[i] = dir
	}
	return dirs
}

func TestSharderFillsThenDoublesCapacity(t *testing.T) {
	root := t.TempDir()

	s, err := NewSharder(root, 2)
	if err != nil {
		t.Fatalf("NewSharder: %v", err)
	}

	shard0 := filepath.Join(root, shardName(0))
	shard1 := filepath.Join(root, shardName(1))
	shard2 := filepath.Join(root, shardName(2))

	// Shard 0's capacity is the base capacity (2): both placements land
	// there.
	for i, dir := range fill(t, s, 2) {
		if dir != shard0 {
			t.Errorf("placement %d went to %s, want shard 0 (%s)", i, dir, shard0)
		}
	}

	// Shard 1 doubles the base capacity (4): all four placements land
	// there, not just two.
	for i, dir := range fill(t, s, 4) {
		if dir != shard1 {
			t.Errorf("placement %d went to %s, want shard 1 (%s)", i, dir, shard1)
		}
	}

	// Shard 1 is now full; the next placement starts shard 2.
	dir, err := s.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if dir != shard2 {
		t.Errorf("got %s, want shard 2 (%s)", dir, shard2)
	}
}

func TestNewSharderResumesExistingShards(t *testing.T) {
	root := t.TempDir()

	s1, err := NewSharder(root, 2)
	if err != nil {
		t.Fatalf("NewSharder: %v", err)
	}
	fill(t, s1, 1) // one placement into shard 0, which holds up to 2

	// A fresh Sharder instance over the same root must resume from shard
	// 0's partial count (1) rather than starting a new shard.
	s2, err := NewSharder(root, 2)
	if err != nil {
		t.Fatalf("NewSharder (resume): %v", err)
	}
	dir, err := s2.Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	want := filepath.Join(root, shardName(0))
	if dir != want {
		t.Errorf("got %s, want resumed shard 0 (%s)", dir, want)
	}
}

func TestIsSharded(t *testing.T) {
	root := "/library/images"

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"direct child of a shard dir", filepath.Join(root, shardName(0), "a.jpg"), true},
		{"direct child of a later shard dir", filepath.Join(root, shardName(12), "a.jpg"), true},
		{"loose directly under root", filepath.Join(root, "a.jpg"), false},
		{"non-numeric subdirectory", filepath.Join(root, "misc", "a.jpg"), false},
		{"nested two levels under a shard dir", filepath.Join(root, shardName(0), "sub", "a.jpg"), false},
		{"outside root entirely", "/elsewhere/a.jpg", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSharded(root, tt.path); got != tt.want {
				t.Errorf("IsSharded(%q, %q) = %v, want %v", root, tt.path, got, tt.want)
			}
		})
	}
}
