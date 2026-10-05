package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
)

// SyncMoves keeps *manifest.json files consistent after a set of images is
// moved on disk.
//
// moves maps each image's original absolute path to its new absolute path.
// For every source directory involved, every file matching *manifest.json
// there is scanned; an entry whose File resolves (relative to the
// manifest's own directory, since entries typically store a bare filename)
// to one of the moved images is removed from that manifest — a manifest
// only ever describes images in its own directory or a subdirectory of it
// (see InTree), and the image has just moved out of that tree entirely.
// Manifests with no matching entries, and no entries invalidated for any
// other reason (see PruneInvalid), are left untouched.
//
// Each matched entry is then mirrored into a *manifest.json in the image's
// destination directory (see MergeIntoDirectoryManifest) so the manifest
// that travels with the image keeps its full metadata, not just the
// source-side pointer to where the image went.
func SyncMoves(moves map[string]string) error {
	dirs := make(map[string]struct{}, len(moves))
	for src := range moves {
		dirs[filepath.Dir(src)] = struct{}{}
	}

	movedEntries := make(map[string]Entry)

	var errs []error
	for dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*manifest.json"))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, manifestPath := range matches {
			found, err := syncManifestFile(manifestPath, moves)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", manifestPath, err))
				continue
			}
			maps.Copy(movedEntries, found)
		}
	}

	if err := mirrorToDestinations(movedEntries); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// syncManifestFile rewrites a single manifest, whatever shape it's stored
// in (see decodeManifestFile), and writes it back in that same shape. It
// returns the entries that matched a move, keyed by the image's new
// absolute path, with File set to that new path for the caller to mirror
// into the destination's own manifest — the source manifest itself never
// gets that path written into it; the matched entry is dropped instead
// (see the loop below), since the file it names no longer lives anywhere
// under this manifest's directory.
//
// Every other entry is also dropped if it fails PruneInvalid: either its
// File no longer resolves to a file on disk, or (the common cause in
// practice) it's a leftover from an older version of this program that
// rewrote a moved entry's File to point outside the manifest's directory
// instead of removing it — exactly the stale pointer this function no
// longer creates, but may still need to clean up from a manifest written
// before this change.
func syncManifestFile(manifestPath string, moves map[string]string) (map[string]Entry, error) {
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(manifestPath)

	entries, shape, err := decodeManifestFile(raw)
	if err != nil {
		return nil, fmt.Errorf("unrecognized manifest shape: %w", err)
	}

	found := make(map[string]Entry)
	remaining := entries[:0]
	for _, e := range entries {
		if newPath, ok := resolveMove(dir, e.File, moves); ok {
			e.File = newPath
			found[newPath] = e
			continue
		}
		remaining = append(remaining, e)
	}

	pruned, removedInvalid := PruneInvalid(remaining, dir)
	if len(found) == 0 && removedInvalid == 0 {
		return found, nil
	}
	if err := encodeManifestFile(manifestPath, pruned, shape); err != nil {
		return nil, err
	}
	return found, nil
}

// resolveMove reports the moved destination for a manifest entry's File
// field, if any.
//
// The lookup key is always manifestDir joined with File's base name, never
// File verbatim — even when File is already absolute. SyncMoves only ever
// looks at a manifest inside a directory that actually had a move happen
// (see the dirs set built in SyncMoves), so an entry naming a file in that
// same directory is identified by filename alone; manifestDir supplies the
// rest. This also makes the match immune to File having been written as an
// absolute path computed a different way than this process computes
// moves' keys — e.g. through a symlinked ancestor directory the writer
// resolved and this process didn't (or vice versa) — since both sides are
// rebuilt from the same manifestDir/moves basis rather than compared as
// opaque strings.
func resolveMove(manifestDir, file string, moves map[string]string) (string, bool) {
	if file == "" {
		return "", false
	}
	candidate := filepath.Join(manifestDir, filepath.Base(file))
	newPath, ok := moves[candidate]
	return newPath, ok
}

// mirrorToDestinations creates or updates a *manifest.json in each
// directory that received moved images, adding one entry per moved image
// whose source manifest entry syncManifestFile found. Each entry's File is
// always set to the full destination path, regardless of whether the
// source entry stored File as a bare filename or an absolute path: a bare
// filename only resolves correctly relative to the manifest's own
// directory, which is a convention worth keeping on the source side (an
// unrelated manifest co-located with other images), but the destination
// copy should unambiguously name the file it describes either way.
func mirrorToDestinations(movedEntries map[string]Entry) error {
	byDir := make(map[string][]Entry)
	for destPath, entry := range movedEntries {
		entry.File = destPath
		byDir[filepath.Dir(destPath)] = append(byDir[filepath.Dir(destPath)], entry)
	}

	var errs []error
	for dir, entries := range byDir {
		if err := MergeIntoDirectoryManifest(dir, entries); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))
		}
	}
	return errors.Join(errs...)
}

// MergeIntoDirectoryManifest merges newEntries into the single existing
// *manifest.json in dir, matching on File to update rather than duplicate
// an entry for a file that's already listed. If dir has no manifest yet, a
// new "image-manifest.json" array is created. More than one existing
// *manifest.json in dir is ambiguous and reported as an error rather than
// guessing which one to update.
func MergeIntoDirectoryManifest(dir string, newEntries []Entry) error {
	matches, err := filepath.Glob(filepath.Join(dir, "*manifest.json"))
	if err != nil {
		return err
	}
	if len(matches) > 1 {
		return fmt.Errorf("ambiguous destination manifest: %d files match *manifest.json", len(matches))
	}
	if len(matches) == 0 {
		return writeIndented(filepath.Join(dir, "image-manifest.json"), newEntries)
	}

	manifestPath := matches[0]
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}

	existing, shape, err := decodeManifestFile(raw)
	if err != nil {
		return fmt.Errorf("unrecognized manifest shape: %w", err)
	}

	// Matched by base filename, not the raw File string: an entry already
	// in dir may have been written in either convention (bare filename or
	// absolute path, see mirrorToDestinations), and a re-sync should
	// replace that entry regardless of which convention produced it rather
	// than appending a duplicate alongside it under a different File
	// spelling for the same on-disk file.
	indexByFile := make(map[string]int, len(existing))
	for i, e := range existing {
		indexByFile[filepath.Base(e.File)] = i
	}
	for _, e := range newEntries {
		if i, ok := indexByFile[filepath.Base(e.File)]; ok {
			existing[i] = e
			continue
		}
		indexByFile[filepath.Base(e.File)] = len(existing)
		existing = append(existing, e)
	}

	// A bare single-object manifest can only stay that shape as long as it
	// still holds exactly one entry; once it grows, it becomes an array.
	if shape == shapeObject && len(existing) != 1 {
		shape = shapeArray
	}
	return encodeManifestFile(manifestPath, existing, shape)
}

// manifestShape identifies which of the recognized on-disk manifest shapes
// a file was read in, so syncManifestFile and mirrorToDestinationManifest
// can write it back the same way rather than silently changing conventions
// a different tool (e.g. site-scraper, which always uses shapeWrapper)
// expects.
type manifestShape int

const (
	// shapeArray is a bare top-level JSON array of entries — the
	// image-manifest.json convention used by process/size.
	shapeArray manifestShape = iota
	// shapeObject is a single entry as a bare JSON object — the bare
	// manifest.json convention for a directory tracking exactly one entry.
	shapeObject
	// shapeWrapper is an object with an "entries" array, e.g.
	// {"entries": [...]} — the shape site-scraper always writes.
	shapeWrapper
)

// decodeManifestFile parses raw as whichever of the three recognized
// manifest shapes it matches — bare array, {"entries": [...]} wrapper, or a
// single bare object — trying each in that order, and returns the
// flattened entries along with which shape it was.
func decodeManifestFile(raw []byte) ([]Entry, manifestShape, error) {
	var entries []Entry
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries, shapeArray, nil
	}

	var w Wrapper
	if err := json.Unmarshal(raw, &w); err == nil && w.Entries != nil {
		return w.Entries, shapeWrapper, nil
	}

	var single Entry
	if err := json.Unmarshal(raw, &single); err != nil {
		return nil, 0, err
	}
	return []Entry{single}, shapeObject, nil
}

// encodeManifestFile writes entries back out in the given shape.
func encodeManifestFile(path string, entries []Entry, shape manifestShape) error {
	switch shape {
	case shapeWrapper:
		return writeIndented(path, Wrapper{Entries: entries})
	case shapeObject:
		if len(entries) == 1 {
			return writeIndented(path, entries[0])
		}
		return writeIndented(path, entries)
	default:
		return writeIndented(path, entries)
	}
}

func writeIndented(path string, data any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
