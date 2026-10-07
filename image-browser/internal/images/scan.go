package images

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item is a single image file found by Scan.
type Item struct {
	Path string
	Name string
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

// Scan returns the image files directly inside dir, sorted case-insensitively
// by name, without recursing into subdirectories.
func Scan(ctx context.Context, dir string) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read directory: %w", err)
	}

	items := make([]Item, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if IsImage(path) {
			items = append(items, Item{Path: path, Name: e.Name()})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
	return items, nil
}
