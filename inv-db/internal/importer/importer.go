// Package importer walks a directory tree for manifest.json files and
// decodes their entries for loading into the store.
package importer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const manifestFilename = "manifest.json"

type Entry struct {
	Site         string
	SourceURL    string
	File         string
	Hash         string
	Status       string
	Error        string
	Original     string
	DiscoveredAt time.Time
	Width        int
	Height       int
}

// decode reads manifest entries from raw JSON, accepting any of the
// shapes a manifest.json may use: an object with an "entries" array (both
// known manifest packages write this), a bare array of entries, or a
// single entry object. The shapes are tried in that order; the first that
// successfully unmarshals wins.
func decode(raw []byte) ([]Entry, error) {
	var wrapper struct {
		Entries []Entry `json:"entries"`
	}

	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Entries != nil {
		return wrapper.Entries, nil
	}

	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries, nil
	}

	var entry Entry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, err
	}

	return []Entry{entry}, nil
}

// Find walks root and returns the path of every manifest.json under it.
func Find(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		} //var patterns = []string{"*.go", "*.jpg"}
		if !d.IsDir() && d.Name() == manifestFilename {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("importer: walk %s: %w", root, err)
	}
	return paths, nil
}

// Load reads and decodes the manifest.json at path into store.Entry values.
func Load(path string) ([]Entry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("importer: read %s: %w", path, err)
	}

	entries, err := decode(raw)
	if err != nil {
		return nil, fmt.Errorf("importer: decode %s: %w", path, err)
	}

	return entries, nil
}
