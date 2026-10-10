package invlib

import (
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPath returns the shared inventory database path used by every
// consumer that doesn't need a directory-scoped inv.db of its own:
// ~/.config/inventory-manager/db/inv.db. The directory is created if it
// doesn't already exist.
//
// Tests that exercise a command end-to-end should not write into that real,
// shared file — set $INVENTORY_MANAGER_DB (e.g. to a path under t.TempDir())
// to override it instead; DefaultPath creates that path's directory the
// same way it does for the real default.
func DefaultPath() (string, error) {
	path := os.Getenv("INVENTORY_MANAGER_DB")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("invlib: get home dir: %w", err)
		}
		path = filepath.Join(home, ".config", "inventory-manager", "db", "inv.db")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("invlib: create db dir: %w", err)
	}

	return path, nil
}
