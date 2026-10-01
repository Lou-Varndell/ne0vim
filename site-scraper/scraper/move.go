package scraper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// resolveDestName returns a filename in destDir that doesn't collide with
// an existing file (from this run or a prior one), appending -2, -3, ...
// before the extension as needed.
func resolveDestName(destDir, wantName string) string {
	ext := filepath.Ext(wantName)
	stem := strings.TrimSuffix(wantName, ext)

	name := wantName
	for i := 2; fileExists(filepath.Join(destDir, name)); i++ {
		name = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	return name
}

// moveToDestination moves src into destDir under a collision-free name
// derived from wantName, returning the final on-disk filename. A
// successful move leaves nothing behind at src, which is how tempDir
// cleanup falls out for free: only files that are never moved (failed
// downloads, dropped duplicates) remain there after Run returns.
func moveToDestination(destDir, src, wantName string) (string, error) {
	finalName := resolveDestName(destDir, wantName)
	dst := filepath.Join(destDir, finalName)

	if err := os.Rename(src, dst); err != nil {
		// tempDir and destDir on different filesystems/mounts: os.Rename
		// can't do this atomically, so fall back to copy-then-remove.
		if !errors.Is(err, syscall.EXDEV) {
			return "", fmt.Errorf("move %s to %s: %w", src, dst, err)
		}
		if err := copyThenRemove(src, dst); err != nil {
			return "", fmt.Errorf("move %s to %s: %w", src, dst, err)
		}
	}

	return finalName, nil
}

func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}

	return os.Remove(src)
}
