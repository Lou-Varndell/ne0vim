package invlib

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultShardCapacity is the number of entries the first shard directory
// under a root holds before Sharder starts a new one, used whenever a
// caller doesn't configure its own capacity.
const DefaultShardCapacity = 4096

// shardNameWidth is how many digits Sharder zero-pads a shard directory's
// index to. Wide enough that sorting shard directory names lexically always
// agrees with sorting them numerically (a prerequisite for loadExisting
// finding the highest-indexed one via a plain string sort), and that
// exhausting it would require more shard directories than any real library
// will ever have. The exact naming convention is not part of Sharder's
// contract -- callers should only rely on shard directories being direct
// children of root, discoverable by IsSharded.
const shardNameWidth = 10

// maxCapacityShift bounds how many times capacityForShard doubles
// baseCapacity, so a pathologically long-lived root (or a deliberately
// small baseCapacity) can't shift a baseCapacity left far enough to
// overflow into a negative int. baseCapacity<<40 alone is already far
// beyond any directory size worth having, so nothing realistic hits this
// cap -- shard capacity simply stops growing past it.
const maxCapacityShift = 40

// capacityForShard returns how many entries shard index idx (0-based) may
// hold: idx 0 holds baseCapacity, idx 1 holds 2*baseCapacity, idx 2 holds
// 4*baseCapacity, and so on -- each shard directory doubles the capacity of
// the one before it. A library that keeps growing ends up with fewer,
// larger directories over time rather than an ever-lengthening flat list of
// same-size ones.
func capacityForShard(baseCapacity int, idx int64) int {
	shift := min(idx, maxCapacityShift)
	return baseCapacity << uint(shift)
}

// shardName formats idx as a shard directory's name.
func shardName(idx int64) string {
	return fmt.Sprintf("%0*d", shardNameWidth, idx)
}

// parseShardName reports whether name is a valid shard directory name (per
// shardName's convention) and, if so, the index it encodes.
func parseShardName(name string) (int64, bool) {
	idx, err := strconv.ParseInt(name, 10, 64)
	if err != nil || idx < 0 {
		return 0, false
	}
	return idx, true
}

// Sharder distributes files across numbered subdirectories of a root
// directory, filling each one to its capacity (see capacityForShard) before
// starting the next, so no single directory ends up holding an unbounded
// number of entries.
//
// A Sharder is not safe for concurrent use; callers sharding from multiple
// goroutines must serialize their Dir/Advance calls.
type Sharder struct {
	root         string
	baseCapacity int

	hasCurrent bool  // false until the first shard directory is known
	maxIdx     int64 // highest shard index seen so far under root
	count      int   // entries already placed in the shard at maxIdx
}

// NewSharder prepares a Sharder that places files under root, creating root
// if it doesn't already exist and discovering any shard directories
// already there, so a later call resumes filling the most recent
// partially-full one instead of starting over. capacity <= 0 uses
// DefaultShardCapacity.
func NewSharder(root string, capacity int) (*Sharder, error) {
	if capacity <= 0 {
		capacity = DefaultShardCapacity
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("invlib: create shard root %s: %w", root, err)
	}

	s := &Sharder{root: root, baseCapacity: capacity}
	if err := s.loadExisting(); err != nil {
		return nil, err
	}
	return s, nil
}

// loadExisting scans root for subdirectories matching the shard naming
// convention and, if any exist, resumes from the highest-indexed one,
// counting its current entries so the very next Dir call's capacity check
// is accurate.
func (s *Sharder) loadExisting() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return fmt.Errorf("invlib: read shard root %s: %w", s.root, err)
	}

	found := false
	var maxIdx int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		idx, ok := parseShardName(e.Name())
		if !ok {
			continue
		}
		if !found || idx > maxIdx {
			maxIdx = idx
			found = true
		}
	}
	if !found {
		return nil
	}

	count, err := countEntries(filepath.Join(s.root, shardName(maxIdx)))
	if err != nil {
		return fmt.Errorf("invlib: count shard dir %s: %w", shardName(maxIdx), err)
	}

	s.hasCurrent = true
	s.maxIdx = maxIdx
	s.count = count
	return nil
}

func countEntries(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// Dir returns the shard directory a caller should place its next file
// into, creating a new one (one past the highest index seen so far) if the
// active shard has reached its capacity or none has been allocated yet.
// Call Advance once the file has actually been placed there -- Dir itself
// doesn't assume success, so a failed move can retry against the same
// directory.
func (s *Sharder) Dir() (string, error) {
	if !s.hasCurrent || s.count >= capacityForShard(s.baseCapacity, s.maxIdx) {
		idx := int64(0)
		if s.hasCurrent {
			idx = s.maxIdx + 1
		}
		if err := os.MkdirAll(filepath.Join(s.root, shardName(idx)), 0o755); err != nil {
			return "", fmt.Errorf("invlib: create shard dir %s: %w", shardName(idx), err)
		}
		s.hasCurrent = true
		s.maxIdx = idx
		s.count = 0
	}
	return filepath.Join(s.root, shardName(s.maxIdx)), nil
}

// Advance records that one more file was successfully placed in the
// directory most recently returned by Dir.
func (s *Sharder) Advance() {
	s.count++
}

// IsSharded reports whether path already lives directly inside one of
// root's shard directories -- i.e. whether some Sharder for root has
// already placed it there, so a caller consolidating files into shards
// doesn't need to move it again.
func IsSharded(root, path string) bool {
	parent := filepath.Dir(path)
	if filepath.Dir(parent) != filepath.Clean(root) {
		return false
	}
	_, ok := parseShardName(filepath.Base(parent))
	return ok
}
