package images

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item is a single image file found by ScanRecursive.
type Item struct {
	Path string
}

var extensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".gif": true, ".webp": true, ".bmp": true,
	".tif": true, ".tiff": true,
}

// IsImage reports whether path has a file extension this app can decode.
func IsImage(path string) bool {
	return extensions[strings.ToLower(filepath.Ext(path))]
}

// CountImages returns the number of image files directly inside dir,
// without recursing into subdirectories.
func CountImages(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("read directory: %w", err)
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && IsImage(e.Name()) {
			count++
		}
	}
	return count, nil
}

// ScanRecursive returns every image file under dir, at any depth, sorted
// case-insensitively by full path. It is used to import a directory tree
// into the inventory database in one pass.
func ScanRecursive(ctx context.Context, dir string) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var items []Item
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if d.IsDir() {
			return nil
		}
		if IsImage(path) {
			items = append(items, Item{Path: path})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk directory: %w", err)
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Path) < strings.ToLower(items[j].Path)
	})
	return items, nil
}
