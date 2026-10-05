package manifest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is the processing state of a manifest Entry.
type Status string

const (
	// StatusMissing marks an entry whose File no longer exists on disk.
	StatusMissing Status = "missing"
	// StatusNonImage marks an entry whose File extension is not a
	// recognized image type.
	StatusNonImage Status = "non-image"
	// StatusInvalid marks an entry with no File path to process.
	StatusInvalid Status = "invalid"
	// StatusProcessed marks an entry whose image dimensions have been
	// read successfully.
	StatusProcessed Status = "processed"
	// StatusFailed marks an entry whose image could not be decoded.
	StatusFailed Status = "failed"
)

// Entry is one record in an image manifest: a single discovered image and
// its discovery and processing metadata.
type Entry struct {
	Site         string    `json:"site"`
	SourceURL    string    `json:"source_url"`
	File         string    `json:"file,omitempty"`
	Hash         string    `json:"hash,omitempty"`
	Status       Status    `json:"status"`
	Error        string    `json:"error,omitempty"`
	Original     string    `json:"original_file,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`

	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// Wrapper is the shape written by tools that wrap their manifest entries in
// a named "entries" array rather than a bare top-level array — the
// convention site-scraper's own internal/manifest package always writes
// (see Manifest in that package). It's exported so other tools in this
// pipeline, such as process, can write it directly to standardize their own
// output on the same shape.
type Wrapper struct {
	Entries []Entry `json:"entries"`
}

// Decode reads manifest entries from raw JSON, accepting any of the shapes
// a manifest file may use: a bare array of entries, a single entry object
// (the bare manifest.json convention), or an object with an "entries"
// array (written by external tools such as a scraper's own manifest
// output). The shapes are tried in that order; the first that successfully
// unmarshals wins.
func Decode(raw []byte) ([]Entry, error) {
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries, nil
	}

	var w Wrapper
	if err := json.Unmarshal(raw, &w); err == nil && w.Entries != nil {
		return w.Entries, nil
	}

	var single Entry
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, err
	}
	return []Entry{single}, nil
}

// InTree reports whether file — resolved against baseDir if it's a bare
// relative path, the convention used throughout this package (see
// resolveMove in sync.go) — names a path at or under baseDir.
//
// A manifest only ever describes images in its own directory or a
// subdirectory of it: the only way an image crosses that boundary is a
// move, and every move creates or merges a correct entry into the
// destination's own manifest (see internal/move and view's Move All), so
// an entry failing this check is always stale, left behind by an older
// version of this program that rewrote File to point outside the
// manifest's own tree instead of dropping the entry.
func InTree(file, baseDir string) bool {
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	rel, err := filepath.Rel(baseDir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// PruneInvalid removes every entry that no longer belongs in a manifest
// rooted at baseDir: its File can no longer be found on disk, or it names
// a file outside baseDir's own directory tree (see InTree). An entry with
// no File at all is left in place: that's an invalid entry, a distinct
// failure mode callers already track separately (see
// manifest.StatusInvalid).
//
// It returns the surviving entries, reusing entries' backing array, and a
// count of how many were dropped. Every command that loads a manifest
// file — process, size, move, and view's Move All sync — calls this so a
// stale reference never lingers once the file it names is gone or has
// moved elsewhere.
func PruneInvalid(entries []Entry, baseDir string) ([]Entry, int) {
	kept := entries[:0]
	removed := 0
	for _, e := range entries {
		if e.File != "" {
			path := e.File
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				removed++
				continue
			}
			if !InTree(e.File, baseDir) {
				removed++
				continue
			}
		}
		kept = append(kept, e)
	}
	return kept, removed
}
