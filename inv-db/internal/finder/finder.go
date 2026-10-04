// Package finder recursively locates files matching a fixed set of glob
// patterns, splitting the search across the root directory's immediate
// subdirectories to run concurrently.
package finder

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// A single file is tested against every pattern in one place, so a walk
// never needs to repeat itself per pattern — adding patterns costs one
// cheap filepath.Match call per file, not another filesystem walk.
func matchesAny(name string, patterns []string) (bool, error) {
	for _, p := range patterns {
		ok, err := filepath.Match(p, name)
		if err != nil {
			return false, fmt.Errorf("matching pattern %q against %q: %w", p, name, err)
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// walkMatches walks dir recursively and returns every file matching
// matchesAny.
func walkMatches(dir string, patterns []string) ([]string, error) {
	var found []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		ok, err := matchesAny(d.Name(), patterns)
		if err != nil {
			return err
		}
		if ok {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %q: %w", dir, err)
	}

	return found, nil
}

// Find splits root into its immediate subdirectories (one level deep) and
// walks each one concurrently in its own goroutine, plus a single pass
// over any files directly inside root. Fan-out is bounded by directory
// count rather than pattern count: each goroutine performs exactly one
// filesystem walk and checks every pattern in-process, instead of
// re-walking the same subtree once per pattern.
func Find(root string, patterns []string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("reading %q: %w", root, err)
	}

	var subdirs []string
	var rootFiles []fs.DirEntry
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, filepath.Join(root, e.Name()))
		} else {
			rootFiles = append(rootFiles, e)
		}
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		results  []string
		firstErr error
	)

	record := func(found []string, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		results = append(results, found...)
	}

	for _, dir := range subdirs {
		wg.Go(func() {
			found, err := walkMatches(dir, patterns)
			record(found, err)
		})
	}

	wg.Go(func() {
		var found []string
		for _, e := range rootFiles {
			ok, err := matchesAny(e.Name(), patterns)
			if err != nil {
				record(nil, err)
				return
			}
			if ok {
				found = append(found, filepath.Join(root, e.Name()))
			}
		}
		record(found, nil)
	})

	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}
	return results, nil
}
