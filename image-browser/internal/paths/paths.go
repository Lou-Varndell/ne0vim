package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Abs expands a leading "~" and resolves the result to an absolute path.
func Abs(path string) (string, error) {
	expanded, err := Expand(path)
	if err != nil {
		return "", err
	}

	return filepath.Abs(expanded)
}

// Expand resolves a leading "~" or "~/" to the current user's home
// directory. It does not support "~user/..." forms.
func Expand(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand path %q: %w", path, err)
		}

		if path == "~" {
			return home, nil
		}

		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}

	return path, nil
}

// DefaultRoot returns the default image root.
func DefaultRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	return filepath.Join(home, "Images")
}

// RelOrPath returns path relative to root when path is below root; otherwise
// it returns path unchanged.
func RelOrPath(path, root string) string {
	if rel, err := filepath.Rel(root, path); err == nil &&
		rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return rel
	}

	return path
}
