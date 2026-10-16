package images

import (
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

// Cache stores JPEG thumbnails on disk, inside a single directory tree kept
// away from any directory Find or Scan might walk, so generated thumbnails
// are never mistaken for images to browse, move, or organize. Each
// thumbnail is named after the BLAKE3 hash of its source image's own
// content (see Hash), sharded into a two-hex-character subdirectory the
// way git shards its object store, which keeps any one directory from
// accumulating too many entries.
//
// Because the thumbnail's identity is its content hash rather than its
// source path, moving or renaming the source image needs no special
// handling here: the same thumbnail is simply found again under the same
// hash, wherever the source now lives.
type Cache struct {
	// Dir is the root directory thumbnails are stored under.
	Dir string
	// Size is the longest-edge pixel size thumbnails are generated at.
	Size uint
}

// DefaultCacheDir returns the directory thumbnails are cached in by
// default: a dedicated "image-browser/thumbnails" subdirectory of the
// OS's per-user cache directory (e.g. ~/Library/Caches on macOS,
// $XDG_CACHE_HOME or ~/.cache on Linux).
func DefaultCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(base, "image-browser", "thumbnails"), nil
}

// NewCache creates a thumbnail cache rooted at dir, sized for thumbnails up
// to size pixels on their longest edge. The cache directory tree is
// created lazily by Generate, so NewCache itself never touches disk.
func NewCache(dir string, size uint) *Cache {
	return &Cache{Dir: dir, Size: size}
}

// thumbnailPath returns where the thumbnail for a source image with
// content hash hash is (or would be) stored.
func (c *Cache) thumbnailPath(hash string) string {
	return filepath.Join(c.Dir, hash[:2], hash+".jpg")
}

// Generate returns the path to a cached thumbnail for path, generating one
// first if it doesn't already exist. Because the thumbnail's name is
// path's own content hash, a file already present at that name is always
// current for that content — there is nothing to re-check or invalidate.
func (c *Cache) Generate(path string) (string, error) {
	hash, err := Hash(path)
	if err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}

	dst := c.thumbnailPath(hash)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
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

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", dir, err)
	}

	// A temp file unique per call (rather than a name derived from dst)
	// means two concurrent Generate calls for the same path never share —
	// and truncate — each other's file; the rename below is what makes the
	// final write atomic.
	out, err := os.CreateTemp(dir, hash+".*.tmp")
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
