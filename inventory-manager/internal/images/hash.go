package images

import (
	"encoding/hex"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// hashChunkSize is the buffer size used to stream file contents into Hash.
const hashChunkSize = 1 << 20

// Hash returns the hex-encoded BLAKE3 digest of the file at path. It is used
// to key inventory records by content rather than by path or name, so the
// same image found under two different paths is recognized as one entry.
func Hash(path string) (string, error) {
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
