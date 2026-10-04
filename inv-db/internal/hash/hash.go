// Package hash computes content hashes for files during ingestion.
package hash

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/zeebo/blake3"
)

// BLAKE3 returns the hex-encoded BLAKE3 digest of the file at path.
func BLAKE3(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hash: open %q: %w", path, err)
	}
	defer f.Close()

	h := blake3.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash: read %q: %w", path, err)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
