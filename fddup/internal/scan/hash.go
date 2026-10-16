package scan

import (
	"io"
	"os"
	"sync"

	"github.com/zeebo/blake3"
)

const (
	// HeadSize is the prefix length read in stage 2. 64 KB is a sweet spot
	// on HDDs: one seek + filesystem readahead pulls ~128 KB anyway, so 64 KB
	// is essentially free relative to 4 KB but discriminates dramatically
	// better — many file formats have shared headers in the first few KB
	// but diverge by the 16 KB mark.
	HeadSize = 64 * 1024

	// BufferSize is the pooled buffer used by io.CopyBuffer in hashFull.
	// Cuts syscall count ~30x vs io.Copy's default 32 KB on multi-GB files.
	BufferSize = 1 * 1024 * 1024
)

// bufPool keeps allocations O(workers) instead of O(files-hashed).
// The element type is *[]byte (rather than []byte) so re-Putting the
// same slice doesn't allocate a new pool entry.
var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, BufferSize)
		return &b
	},
}

// hashHead returns BLAKE3 of the first HeadSize bytes (or whole file if smaller).
// Used by stage 2 to disqualify size-collisions cheaply.
func hashHead(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()

	h := blake3.New()
	// io.EOF is expected for files smaller than HeadSize and is not an error.
	if _, err := io.CopyN(h, f, HeadSize); err != nil && err != io.EOF {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// hashFull returns BLAKE3 of the entire file using a pooled 1 MB buffer.
func hashFull(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()

	h := blake3.New()
	bufPtr := bufPool.Get().(*[]byte)
	defer bufPool.Put(bufPtr)

	if _, err := io.CopyBuffer(h, f, *bufPtr); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// pickKeeper returns the path that should survive a duplicate cluster.
// Shortest path wins (with lexicographic tiebreak for determinism) — this
// heuristic favors the "original" location over deeper "copy of X" paths.
func pickKeeper(paths []string) string {
	keeper := paths[0]
	for _, p := range paths[1:] {
		if len(p) < len(keeper) || (len(p) == len(keeper) && p < keeper) {
			keeper = p
		}
	}
	return keeper
}
