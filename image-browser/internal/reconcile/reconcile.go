// Package reconcile implements the image-browser reconcile command: it
// compares the files the inventory database has recorded under a root
// directory against what's actually there on disk, and brings the
// database back in line without losing history (missing_since, hash,
// status/error) or re-touching files that haven't changed.
//
// Unlike scan, which only ever adds files, reconcile also detects files
// that have disappeared, reappeared, or changed, and it is read-only by
// default — pass -apply to write anything. It never deletes a files row;
// that harder-line cleanup belongs to invdb.PruneMissing. A directory
// reconcile can't read (permission denied, unmounted volume, ...) is
// excluded from the missing-files determination entirely rather than
// having every file under it reported missing — see walkFilesystem and
// underAny.
package reconcile

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	invlib "inv-lib"

	"image-browser/internal/images"
	"image-browser/internal/manifest"
	"image-browser/internal/paths"
)

// Run executes the reconcile command.
func Run(args []string) error {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "root directory to reconcile against the database (required)")
	apply := fs.Bool("apply", false, "write the detected changes to the database (default: report only)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser reconcile -root <dir> [-apply]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Compares the database's records under -root against what's actually on")
		fmt.Fprintln(os.Stderr, "disk and reports (or, with -apply, applies) new, changed, and missing")
		fmt.Fprintln(os.Stderr, "files, and prunes stale entries from any manifest.json files under -root.")
		fmt.Fprintln(os.Stderr, "Never deletes a files row and never touches records outside -root.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("reconcile: unexpected positional arguments")
	}
	if *root == "" {
		return errors.New("reconcile: -root is required")
	}

	rootPath, err := paths.Abs(*root)
	if err != nil {
		return fmt.Errorf("reconcile: resolving root: %w", err)
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("reconcile: not a directory: %s", rootPath)
	}

	dbPath, err := invlib.DefaultPath()
	if err != nil {
		return fmt.Errorf("reconcile: resolve inv.db path: %w", err)
	}
	ctx := context.Background()
	inv, err := invlib.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("reconcile: open inv.db: %w", err)
	}
	defer inv.Close()

	rep, err := reconcileRoot(ctx, inv, rootPath, *apply)
	if err != nil {
		return err
	}

	rep.print(*apply)
	return nil
}

// report tallies what a reconcile pass found and, when apply is set, did.
type report struct {
	examined, unchanged, changed, newFiles, missing, reappeared, scanErrors int
	manifestsChecked, manifestEntriesRemoved                                int
}

// reconcileRoot runs both phases of reconciliation: a filesystem walk
// (walkFilesystem) that never touches the database, followed by a diff
// against the database's own records under rootPath and, if apply is set,
// the writes that bring the database back in line.
func reconcileRoot(ctx context.Context, inv *invlib.DB, rootPath string, apply bool) (*report, error) {
	fsEntries, erroredDirs, err := walkFilesystem(rootPath)
	if err != nil {
		return nil, fmt.Errorf("reconcile: walking %s: %w", rootPath, err)
	}

	// Trailing separator so a record under a sibling directory that merely
	// shares rootPath as a string prefix (e.g. rootPath "/img/root" vs a
	// record at "/img/root2/x.jpg") never matches.
	records, err := inv.ListFileRecords(ctx, invlib.FileQuery{PathPrefix: rootPath + string(filepath.Separator)})
	if err != nil {
		return nil, fmt.Errorf("reconcile: listing database records under %s: %w", rootPath, err)
	}
	dbByPath := make(map[string]*invlib.File, len(records))
	for _, rec := range records {
		dbByPath[rec.File.Path] = rec.File
	}

	rep := &report{}
	now := time.Now().UTC()

	for path, entry := range fsEntries {
		rep.examined++

		dbFile, known := dbByPath[path]
		if !known {
			if err := recordNewFile(ctx, inv, path, entry, now, apply); err != nil {
				fmt.Fprintf(os.Stderr, "reconcile: new file %s: %v\n", path, err)
				continue
			}
			rep.newFiles++
			continue
		}

		if dbFile.MissingSince != nil {
			rep.reappeared++
		}
		changedContent := hasChanged(dbFile.Filesize, dbFile.ModifiedAt, entry.Size, entry.ModTime)
		if changedContent {
			rep.changed++
		} else {
			rep.unchanged++
		}

		if !apply {
			continue
		}
		if err := applyPresentFile(ctx, inv, dbFile, path, entry, now, changedContent); err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: updating %s: %v\n", path, err)
		}
	}

	for path, dbFile := range dbByPath {
		if _, onDisk := fsEntries[path]; onDisk {
			continue
		}
		if underAny(path, erroredDirs) {
			rep.scanErrors++
			continue
		}
		rep.examined++
		rep.missing++

		// Already recorded missing by an earlier run: leave missing_since
		// exactly as it was rather than resetting the clock on how long
		// it's been gone.
		if dbFile.MissingSince != nil || !apply {
			continue
		}
		dbFile.MissingSince = &now
		if err := inv.UpdateFile(ctx, dbFile); err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: marking missing %s: %v\n", path, err)
		}
	}

	manifestsChecked, entriesRemoved, err := reconcileManifests(rootPath, apply)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reconcile: manifest cleanup: %v\n", err)
	}
	rep.manifestsChecked = manifestsChecked
	rep.manifestEntriesRemoved = entriesRemoved

	return rep, nil
}

// recordNewFile inserts a files row (and, for a recognized image, an
// images row) for a file found on disk with no matching database record —
// the same treatment scan.Run gives a file it discovers for the first
// time. A no-op in report-only mode.
func recordNewFile(ctx context.Context, inv *invlib.DB, path string, entry fsEntry, now time.Time, apply bool) error {
	if !apply {
		return nil
	}

	hash, err := images.Hash(path)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}

	modifiedAt := entry.ModTime
	verifiedAt := now
	file := &invlib.File{
		Path:           path,
		Filename:       entry.Name,
		Extension:      &entry.Ext,
		Filesize:       entry.Size,
		ModifiedAt:     &modifiedAt,
		LastVerifiedAt: &verifiedAt,
	}
	if hash != "" {
		file.Hash = &hash
	}

	if _, err := inv.EnsureFile(ctx, file); err != nil {
		return fmt.Errorf("insert: %w", err)
	}

	if !images.IsImage(path) {
		return nil
	}
	width, height, format, space, err := images.Decode(path)
	if err != nil {
		// Matched an image extension but didn't decode (renamed or
		// corrupt) — the files row above still stands; a later `process`
		// run will retry.
		return nil
	}
	img := &invlib.Image{FileID: file.ID, Width: &width, Height: &height}
	if format != "" {
		img.Format = &format
	}
	if space != "" {
		img.ColorSpace = &space
	}
	if _, err := inv.InsertImageIfAbsent(ctx, img); err != nil {
		return fmt.Errorf("insert image: %w", err)
	}
	return nil
}

// applyPresentFile updates dbFile's record for a file reconcile found on
// disk matching an existing database entry: last_verified_at is refreshed
// and any missing_since is cleared unconditionally, and when changedContent
// is set, filesize/modified_at/hash are refreshed and the stale
// images/videos/audio/media_probe rows for it are cleared — "mark for
// reprocessing" from the design doc — along with any status/error a
// previous `process` run left behind describing the old content, so the
// next `process` run (or ffprobe effort) starts clean instead of inheriting
// a decode outcome that belonged to different bytes.
func applyPresentFile(ctx context.Context, inv *invlib.DB, dbFile *invlib.File, path string, entry fsEntry, now time.Time, changedContent bool) error {
	dbFile.LastVerifiedAt = &now
	dbFile.MissingSince = nil

	if changedContent {
		hash, err := images.Hash(path)
		if err != nil {
			return fmt.Errorf("hash: %w", err)
		}

		modifiedAt := entry.ModTime
		dbFile.Filesize = entry.Size
		dbFile.ModifiedAt = &modifiedAt
		dbFile.Status = nil
		dbFile.Error = nil
		if hash != "" {
			dbFile.Hash = &hash
		}

		for _, clear := range []func(context.Context, int64) error{
			inv.DeleteImage, inv.DeleteVideo, inv.DeleteAudio, inv.DeleteMediaProbe,
		} {
			if err := clear(ctx, dbFile.ID); err != nil {
				return fmt.Errorf("clear stale media metadata: %w", err)
			}
		}
	}

	if err := inv.UpdateFile(ctx, dbFile); err != nil {
		return fmt.Errorf("update: %w", err)
	}
	return inv.TouchScanned(ctx, dbFile.ID)
}

// reconcileManifests prunes stale entries — ones no longer backed by a
// file on disk, or pointing outside their manifest's own directory tree —
// from every *manifest.json file under root, using the same enforcement
// `import` already relies on (manifest.File.PruneInvalid). Report-only
// mode still counts how many entries would be dropped, without writing.
func reconcileManifests(root string, apply bool) (checked, removed int, err error) {
	manifestFiles, err := manifest.FindFiles(root)
	if err != nil {
		return 0, 0, fmt.Errorf("finding manifest files: %w", err)
	}

	for _, path := range manifestFiles {
		mf, err := manifest.LoadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: reading %s: %v\n", path, err)
			continue
		}
		checked++

		dropped := mf.PruneInvalid()
		if dropped == 0 {
			continue
		}
		removed += dropped

		if !apply {
			continue
		}
		if err := mf.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: writing %s: %v\n", path, err)
		}
	}
	return checked, removed, nil
}

// fsEntry holds the cheap filesystem attributes walkFilesystem collects
// for one matched file.
type fsEntry struct {
	Size    int64
	ModTime time.Time
	Name    string
	Ext     string
}

// walkFilesystem recursively collects every regular file under root,
// keyed by absolute path, along with the directories it couldn't read
// (permission denied, broken symlink, unmounted volume, ...). A directory
// WalkDir can't read is skipped — not treated as empty — and recorded in
// erroredDirs so the caller excludes every path under it from the
// missing-files determination entirely; the alternative (silently skipping
// it) would make every database record under an inaccessible directory
// look deleted. An error reading root itself is returned outright, since
// that indicates a bad starting point rather than an incidental
// obstruction partway through the walk.
func walkFilesystem(root string) (map[string]fsEntry, []string, error) {
	entries := make(map[string]fsEntry)
	var erroredDirs []string

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if d != nil && d.IsDir() {
				erroredDirs = append(erroredDirs, path)
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "reconcile: stat %s: %v\n", path, err)
			return nil
		}

		entries[path] = fsEntry{
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Name:    d.Name(),
			Ext:     filepath.Ext(path),
		}
		return nil
	})
	return entries, erroredDirs, err
}

// underAny reports whether path is one of dirs or lives somewhere under
// one of them.
func underAny(path string, dirs []string) bool {
	for _, dir := range dirs {
		if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (r *report) print(apply bool) {
	fmt.Println("Reconciliation summary")
	fmt.Println("----------------------")
	fmt.Printf("Files examined:        %d\n", r.examined)
	fmt.Printf("Unchanged:             %d\n", r.unchanged)
	fmt.Printf("Changed:               %d\n", r.changed)
	fmt.Printf("New files:             %d\n", r.newFiles)
	fmt.Printf("Missing files:         %d\n", r.missing)
	fmt.Printf("Reappeared files:      %d\n", r.reappeared)
	fmt.Printf("Scan errors:           %d\n", r.scanErrors)
	fmt.Printf("Manifests checked:     %d\n", r.manifestsChecked)
	fmt.Printf("Manifest entries removed: %d\n", r.manifestEntriesRemoved)
	fmt.Println()
	if apply {
		fmt.Println("Changes applied: yes")
	} else {
		fmt.Println("Changes applied: no (pass -apply to write these changes)")
	}
}
