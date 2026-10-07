// Package ingest implements the image-browser import command: a read-only
// pass over *manifest.json files — written by a separate site-scraper tool,
// never by this program — that upserts their site, source_url,
// original_file, discovered_at, status, and error provenance into the
// inventory database (see internal/store). It is the only command that
// reads manifest.json, and it never writes back to it; move, size, process,
// and view all operate on the database alone.
package ingest

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"image-browser/internal/db"
	"image-browser/internal/manifest"
	"image-browser/internal/paths"
	"image-browser/internal/store"
)

// skipReason classifies why a manifest entry was not imported.
type skipReason int

const (
	skipNone skipReason = iota
	// skipInvalid marks an entry with no File path to resolve.
	skipInvalid
	// skipOutOfTree marks an entry whose File resolves outside the
	// manifest's own directory tree — a stale pointer left by an older
	// tool version (see manifest.InTree).
	skipOutOfTree
	// skipMissing marks an entry whose File no longer exists on disk.
	skipMissing
)

// Run executes the import command.
func Run(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "manifest file, or directory to search recursively for manifest files (default: ~/Images)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser import [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Reads *manifest.json files (written by site-scraper) and records their")
		fmt.Fprintln(os.Stderr, "site/source_url/original_file/discovered_at/status/error provenance in")
		fmt.Fprintln(os.Stderr, "the inventory database. Never writes back to manifest.json.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("import: unexpected positional arguments")
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = paths.DefaultRoot()
	}
	rootPath, err := paths.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("import: resolving root: %w", err)
	}

	manifestFiles, err := manifest.FindFiles(rootPath)
	if err != nil {
		return fmt.Errorf("import: finding manifest files under %s: %w", rootPath, err)
	}
	if len(manifestFiles) == 0 {
		fmt.Printf("No manifest files found under %s\n", rootPath)
		return nil
	}

	dbPath, err := db.DefaultPath()
	if err != nil {
		return fmt.Errorf("import: resolve inv.db path: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("import: open inv.db: %w", err)
	}
	defer database.Close()
	inv := store.New(database)

	ctx := context.Background()

	var scanned, imported, skippedInvalid, skippedOutOfTree, skippedMissing int

	for _, manifestPath := range manifestFiles {
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: reading %s: %v\n", manifestPath, err)
			continue
		}

		entries, err := manifest.Decode(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: parsing %s: %v\n", manifestPath, err)
			continue
		}
		scanned++

		manifestDir := filepath.Dir(manifestPath)
		for _, entry := range entries {
			switch importEntry(ctx, inv, entry, manifestDir) {
			case skipInvalid:
				skippedInvalid++
			case skipOutOfTree:
				skippedOutOfTree++
			case skipMissing:
				skippedMissing++
			default:
				imported++
			}
		}
	}

	fmt.Printf("Manifests scanned: %d\n", scanned)
	fmt.Printf("Entries imported: %d\n", imported)
	fmt.Printf("Skipped (no file path): %d\n", skippedInvalid)
	fmt.Printf("Skipped (outside manifest's directory): %d\n", skippedOutOfTree)
	fmt.Printf("Skipped (file missing on disk): %d\n", skippedMissing)
	return nil
}

// importEntry resolves entry's File against manifestDir and, if it still
// names a real, in-tree file, upserts its provenance into the database.
// Width/height are only recorded (via AddImage, which never overwrites an
// existing images row) when the entry itself reports a processed status —
// an unprocessed or failed entry has nothing worth recording there, and a
// later `process` run will fill it in from the file itself.
func importEntry(ctx context.Context, inv *store.Store, entry manifest.Entry, manifestDir string) skipReason {
	if entry.File == "" {
		return skipInvalid
	}
	if !manifest.InTree(entry.File, manifestDir) {
		return skipOutOfTree
	}

	path := entry.File
	if !filepath.IsAbs(path) {
		path = filepath.Join(manifestDir, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return skipMissing
	}

	id, err := inv.UpsertFile(ctx, store.File{
		Path:         path,
		Filename:     filepath.Base(path),
		Extension:    filepath.Ext(path),
		Filesize:     info.Size(),
		BLAKE3:       sql.NullString{String: entry.Hash, Valid: entry.Hash != ""},
		ModifiedAt:   sql.NullTime{Time: info.ModTime(), Valid: true},
		Site:         sql.NullString{String: entry.Site, Valid: entry.Site != ""},
		SourceURL:    sql.NullString{String: entry.SourceURL, Valid: entry.SourceURL != ""},
		OriginalFile: sql.NullString{String: entry.Original, Valid: entry.Original != ""},
		DiscoveredAt: sql.NullTime{Time: entry.DiscoveredAt, Valid: !entry.DiscoveredAt.IsZero()},
		Status:       sql.NullString{String: string(entry.Status), Valid: entry.Status != ""},
		Error:        sql.NullString{String: entry.Error, Valid: entry.Error != ""},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "import: %s: %v\n", path, err)
		return skipInvalid
	}

	if entry.Status == manifest.StatusProcessed && entry.Width > 0 && entry.Height > 0 {
		if _, err := inv.AddImage(ctx, store.Image{FileID: id, Width: entry.Width, Height: entry.Height}); err != nil {
			fmt.Fprintf(os.Stderr, "import: add image %s: %v\n", path, err)
		}
	}

	return skipNone
}
