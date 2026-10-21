package invlib

import (
	"encoding/hex"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// hashChunkSize is the buffer size used to stream file contents into
// HashFile.
const hashChunkSize = 1 << 20

// HashFile returns the hex-encoded BLAKE3 digest of the file at path. It is
// the single hashing implementation shared by every inv-lib consumer that
// needs to compute or compare a files.hash value — site-scraper's
// dedup pass and image-browser's move/organize duplicate check both call
// this instead of maintaining their own BLAKE3 plumbing.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, hashChunkSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
