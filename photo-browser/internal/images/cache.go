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

type Cache struct {
	Dir  string
	Size uint
}

func NewCache(size uint) (*Cache, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, "image-browser", fmt.Sprintf("%d", size))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Cache{Dir: dir, Size: size}, nil
}

func (c *Cache) key(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]) + ".jpg"
}

func (c *Cache) Path(path string) string {
	return filepath.Join(c.Dir, c.key(path))
}

func (c *Cache) Generate(path string) (string, error) {
	dst := c.Path(path)

	if info, err := os.Stat(dst); err == nil {
		if src, err := os.Stat(path); err == nil && info.ModTime().After(src.ModTime()) {
			return dst, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return "", err
	}

	thumb := resize.Thumbnail(c.Size, c.Size, src, resize.Lanczos3)

	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}

	err = jpeg.Encode(out, thumb, &jpeg.Options{Quality: 88})
	closeErr := out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", closeErr
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return dst, nil
}
