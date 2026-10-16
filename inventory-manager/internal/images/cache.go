package images

import (
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

var ErrAnimatedWebP = fmt.Errorf("animated WebP is not supported")

// Cache stores JPEG thumbnails on disk, keyed by the BLAKE3 content hash
// of the source image (see Hash) rather than its path, so repeated views
// of the same image skip re-decoding and re-resizing full-size images —
// and a moved or renamed source image still finds its existing thumbnail.
// This is the same hash image-browser keys its own thumbnail cache by and
// invlib stores in files.hash, so the two tools' caches agree on identity
// for any image they both see.
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
	dir := filepath.Join(base, "inventory-manager", fmt.Sprintf("%d", size))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create cache dir %q: %w", dir, err)
	}
	return &Cache{Dir: dir, Size: size}, nil
}

// Path returns the on-disk cache path a thumbnail for path would be (or is)
// stored at, whether or not it has been generated yet. Since the cache key
// is path's own content hash (see Hash), this reads path to compute it —
// a missing or unreadable file surfaces here rather than later in Generate.
func (c *Cache) Path(path string) (string, error) {
	hash, err := Hash(path)
	if err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return filepath.Join(c.Dir, hash+".jpg"), nil
}

// Generate returns the path to a cached thumbnail for path. If a thumbnail
// already exists, it is reused as-is unless force is true (wired to the
// UI's Refresh button) — Generate never re-checks the source file's mtime on
// its own, so routine loads (opening a directory, drilling into a preview
// row, scrolling) never pay to regenerate thumbnails that are already there.
func (c *Cache) Generate(path string, force bool) (string, error) {
	dst, err := c.Path(path)
	if err != nil {
		return "", err
	}

	if !force {
		if _, err := os.Stat(dst); err == nil {
			return dst, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	// Check if the image is an animated WebP
	isAnimated, err := IsAnimatedWebP(f)
	if err != nil {
		return "", fmt.Errorf("check animated WebP: %w", err)
	}
	if isAnimated {
		return "", ErrAnimatedWebP
	}

	src, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode %q: %w", path, err)
	}

	thumb := resize.Thumbnail(c.Size, c.Size, src, resize.Lanczos3)

	// A temp file unique per call (rather than a name derived from dst)
	// means two concurrent Generate calls for the same path never share —
	// and truncate — each other's file; the rename below is what makes the
	// final write atomic.
	out, err := os.CreateTemp(c.Dir, filepath.Base(dst)+".*.tmp")
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

// isAnimatedWebP reports whether f is an animated WebP file.
// It leaves f positioned at the beginning for subsequent decoding.
func IsAnimatedWebP(f *os.File) (bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	defer f.Seek(0, io.SeekStart)

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return false, err
	}

	if string(header[0:4]) != "RIFF" ||
		string(header[8:12]) != "WEBP" {
		return false, nil
	}

	riffSize := int64(binary.LittleEndian.Uint32(header[4:8]))
	remaining := riffSize - 4 // WEBP identifier is included in RIFF size.

	for remaining >= 8 {
		chunk := make([]byte, 8)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return false, err
		}

		chunkSize := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		if chunkSize > remaining-8 {
			return false, fmt.Errorf("invalid WebP chunk size")
		}

		remaining -= 8

		if string(chunk[0:4]) == "VP8X" {
			if chunkSize < 1 {
				return false, fmt.Errorf("invalid VP8X chunk")
			}

			var flags [1]byte
			if _, err := io.ReadFull(f, flags[:]); err != nil {
				return false, err
			}

			// VP8X animation flag is bit 1 (0x02).
			return flags[0]&0x02 != 0, nil
		}

		// WebP chunks are padded to an even byte boundary.
		skip := chunkSize + chunkSize%2
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return false, err
		}
		remaining -= skip
	}

	return false, nil
}
