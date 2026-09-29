package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

type Entry struct {
	Site         string    `json:"site"`
	SourceURL    string    `json:"source_url"`
	File         string    `json:"file,omitempty"`
	Hash         string    `json:"hash,omitempty"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Original     string    `json:"original_file,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`

	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// Manifest is the on-disk (and returned) record of every file a Run
// encountered, keyed implicitly by Entry.SourceURL.
type Manifest struct {
	Entries []Entry `json:"entries"`
}

// Merge loads the manifest at path (a missing file is treated as an empty
// manifest, not an error), upserts newEntries into it by SourceURL — an
// entry from a prior run is replaced rather than duplicated — atomically
// rewrites path, and returns the merged result.
func Merge(path string, newEntries []Entry) (*Manifest, error) {
	byURL, err := load(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: load %s: %w", path, err)
	}

	for _, e := range newEntries {
		byURL[e.SourceURL] = e
	}

	merged := make([]Entry, 0, len(byURL))
	for _, e := range byURL {
		merged = append(merged, e)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].SourceURL < merged[j].SourceURL })

	if err := write(path, merged); err != nil {
		return nil, fmt.Errorf("manifest: write %s: %w", path, err)
	}

	return &Manifest{Entries: merged}, nil
}

func load(path string) (map[string]Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Entry{}, nil
	}
	if err != nil {
		return nil, err
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	byURL := make(map[string]Entry, len(m.Entries))
	for _, e := range m.Entries {
		byURL[e.SourceURL] = e
	}
	return byURL, nil
}

// write rewrites path atomically (write to a temp file, then rename) so a
// crash or concurrent read never observes a partially-written manifest.
func write(path string, entries []Entry) error {
	data, err := json.MarshalIndent(Manifest{Entries: entries}, "", "  ")
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}
