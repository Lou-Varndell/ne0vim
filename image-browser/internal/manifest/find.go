package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// FilePattern is the glob pattern identifying a manifest file, matching the
// convention used elsewhere (see sync.go): any file ending in
// "manifest.json", e.g. manifest.json or image-manifest.json.
const FilePattern = "*manifest.json"

// IsFileName reports whether path's base name matches the manifest file
// naming convention (manifest.json, image-manifest.json, ...).
func IsFileName(path string) bool {
	matched, _ := filepath.Match(FilePattern, filepath.Base(path))
	return matched
}

// FindFiles resolves root to the manifest file(s) it names: root itself if
// it is a single manifest file, or every file matching FilePattern found by
// recursively walking root if it is a directory. A subdirectory that can't
// be read (permission denied, broken symlink, etc.) is skipped rather than
// aborting the whole walk, matching the convention images.Find uses.
func FindFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		if !IsFileName(root) {
			return nil, fmt.Errorf("%s does not look like a manifest file (expected a name matching %q)", root, FilePattern)
		}
		return []string{root}, nil
	}

	var found []string
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if IsFileName(path) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(found)
	return found, nil
}
