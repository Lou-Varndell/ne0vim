package manifest

import (
	"fmt"
	"os"
	"path/filepath"
)

// File is a manifest loaded from disk together with the on-disk shape it
// was found in (see decodeManifestFile), so Save writes it back in that
// same shape rather than silently changing conventions a different tool
// (e.g. site-scraper, which always uses the entries-wrapper shape) expects.
type File struct {
	// Path is the file this manifest was loaded from and will be written
	// back to by Save.
	Path string
	// Entries is the manifest's decoded content. Callers may mutate it
	// freely before calling Save.
	Entries []Entry

	shape manifestShape
}

// LoadFile reads path and decodes it as any of the recognized manifest
// shapes.
func LoadFile(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	entries, shape, err := decodeManifestFile(raw)
	if err != nil {
		return nil, fmt.Errorf("unrecognized manifest shape: %w", err)
	}

	return &File{Path: path, Entries: entries, shape: shape}, nil
}

// Save writes f.Entries back to f.Path in the shape f was loaded in.
func (f *File) Save() error {
	return encodeManifestFile(f.Path, f.Entries, f.shape)
}

// SavePruned writes a pruned copy of f.Entries to f.Path — the same
// enforcement PruneInvalid performs (see the package-level PruneInvalid),
// resolving a relative File against f.Path's own directory — but applied
// to a snapshot rather than mutating f.Entries itself.
//
// This matters for a caller that still holds index-based references into
// f.Entries after a move, such as move's Move All retry button, which
// re-runs over the same matched indexes every time it's clicked: dropping
// an entry from f.Entries the moment it moves would shift every later
// index out from under that caller. Writing the pruned copy to disk
// (rather than mutating f.Entries) lets the moved entry — now pointing
// outside f.Path's own directory — disappear from what the next command
// run sees, while this run's own in-memory indexes stay valid for as long
// as it keeps running.
func (f *File) SavePruned() error {
	pruned, _ := PruneInvalid(f.Entries, filepath.Dir(f.Path))
	return encodeManifestFile(f.Path, pruned, f.shape)
}

// PruneInvalid removes entries from f.Entries that no longer belong in
// this manifest (see the package-level PruneInvalid), resolving a relative
// File against f.Path's own directory. It returns how many entries were
// dropped; callers must call Save afterward to persist the change.
func (f *File) PruneInvalid() int {
	kept, removed := PruneInvalid(f.Entries, filepath.Dir(f.Path))
	f.Entries = kept
	return removed
}
