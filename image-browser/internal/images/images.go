package images

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"lukechampine.com/blake3"
)

const HashChunkSize = 1 << 20

var Extensions = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {},
	".bmp": {}, ".tiff": {}, ".webp": {},
}

// Find walks root looking for image files whose path contains pattern
// (case-insensitive). A subdirectory that can't be read (permission denied,
// broken symlink, etc.) is skipped rather than aborting the whole walk; an
// error reading root itself is still returned, since that indicates the
// caller passed a bad starting point rather than an incidental obstruction.
func Find(root, pattern string) ([]string, error) {
	pattern = strings.ToLower(pattern)
	var matches []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := Extensions[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}
		if strings.Contains(strings.ToLower(path), pattern) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(matches)
	return matches, nil
}

func Hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New(32, nil)
	if _, err := io.CopyBuffer(h, f, make([]byte, HashChunkSize)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func SameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	ah, err := Hash(a)
	if err != nil {
		return false, err
	}
	bh, err := Hash(b)
	if err != nil {
		return false, err
	}
	return ah == bh, nil
}

// ResolveDestination decides where src should be moved to inside destDir.
//
// Duplicate detection is content-based, not filename-based. It first compares
// file sizes and only hashes destination files whose size matches src. If a
// matching hash exists anywhere directly inside destDir, the source is a
// duplicate and the returned duplicate flag is true.
//
// If no duplicate exists, the original filename is used when available. If
// that filename is already taken, a numbered name such as foo-2.jpg is used.
// A candidate path is atomically claimed before it is returned so the caller's
// later move/copy is protected from a concurrent organizer using the same
// destination.
func ResolveDestination(src, destDir string) (string, bool, error) {
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return "", false, err
	}

	duplicate, err := hasDuplicateBySizeAndHash(src, sourceInfo.Size(), destDir)
	if err != nil {
		return "", false, err
	}
	if duplicate {
		return "", true, nil
	}

	dest := filepath.Join(destDir, filepath.Base(src))
	claimed, err := claim(dest)
	if err != nil {
		return "", false, err
	}
	if claimed {
		return dest, false, nil
	}

	ext := filepath.Ext(dest)
	stem := strings.TrimSuffix(filepath.Base(dest), ext)
	for n := 2; ; n++ {
		candidate := filepath.Join(destDir, fmt.Sprintf("%s-%d%s", stem, n, ext))
		claimed, err := claim(candidate)
		if err != nil {
			return "", false, err
		}
		if claimed {
			return candidate, false, nil
		}
	}
}

func hasDuplicateBySizeAndHash(src string, sourceSize int64, destDir string) (bool, error) {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return false, err
	}

	var sourceHash string
	hashedSource := false

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}

		path := filepath.Join(destDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return false, err
		}

		if info.Size() != sourceSize {
			continue
		}

		if !hashedSource {
			sourceHash, err = Hash(src)
			if err != nil {
				return false, err
			}
			hashedSource = true
		}

		destHash, err := Hash(path)
		if err != nil {
			return false, err
		}

		if sourceHash == destHash {
			return true, nil
		}
	}

	return false, nil
}

// Move relocates src to dest, falling back to a copy-then-remove when the
// two paths are on different filesystems. The destination should normally be
// obtained from ResolveDestination so it has already been claimed.
func Move(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("renaming %s to %s: %w", src, dest, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	out, err := os.OpenFile(
		dest,
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		info.Mode(),
	)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return fmt.Errorf("copying %s to %s: %w", src, dest, err)
	}

	if err := out.Close(); err != nil {
		os.Remove(dest)
		return fmt.Errorf("closing %s: %w", dest, err)
	}

	if err := os.Remove(src); err != nil {
		return fmt.Errorf("removing %s after copy: %w", src, err)
	}
	return nil
}

// claim atomically creates an empty file at path if nothing exists there
// yet, reporting whether it did so.
func claim(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	return true, nil
}
