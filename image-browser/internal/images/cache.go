package images

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// Cache stores JPEG thumbnails on disk, keyed by the hash of the source
// image's path, so repeated views of the same directory can skip re-decoding
// and re-resizing full-size images.
type Cache struct {
	Dir  string
	Size uint
}

// NewCache creates (or reuses) an on-disk thumbnail cache under the user's
// cache directory, sized for thumbnails up to size pixels on their longest
// edge.
func NewCache(size uint) (*Cache, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user cache dir: %w", err)
	}
	dir := filepath.Join(base, "image-browser", fmt.Sprintf("%d", size))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return &Cache{Dir: dir, Size: size}, nil
}

func (c *Cache) key(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]) + ".jpg"
}

// Path returns the on-disk cache path a thumbnail for path would be (or is)
// stored at, whether or not it has been generated yet.
func (c *Cache) Path(path string) string {
	return filepath.Join(c.Dir, c.key(path))
}

// Generate returns the path to a cached thumbnail for path, generating (or
// regenerating a stale) one first if needed.
func (c *Cache) Generate(path string) (string, error) {
	dst := c.Path(path)

	if info, err := os.Stat(dst); err == nil {
		if src, err := os.Stat(path); err == nil && !info.ModTime().Before(src.ModTime()) {
			return dst, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode %q: %w", path, err)
	}

	thumb := resize.Thumbnail(c.Size, c.Size, src, resize.Lanczos3)

	// A temp file unique per call (rather than a name derived from dst)
	// means two concurrent Generate calls for the same path never share —
	// and truncate — each other's file; the rename below is what makes the
	// final write atomic.
	out, err := os.CreateTemp(c.Dir, c.key(path)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmp := out.Name()

	err = jpeg.Encode(out, thumb, &jpeg.Options{Quality: 88})
	closeErr := out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("encode %q: %w", path, err)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename temp file: %w", err)
	}
	return dst, nil
}
