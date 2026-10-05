// Package process implements the image-manifest dimension processor.
package process

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"image-browser/internal/db"
	"image-browser/internal/manifest"
	"image-browser/internal/paths"
	"image-browser/internal/store"
)

const (
	// defaultInput is the current directory rather than a specific
	// filename: FindFiles walks a directory recursively for every
	// *manifest.json it contains, so "." finds a lone image-manifest.json
	// sitting in the working directory just as well as it finds many
	// manifest files scattered through subdirectories below it.
	defaultInput           = "."
	defaultWorkers         = 8
	defaultCheckpointEvery = 5000
)

type job struct {
	file string
}

type result struct {
	file    string
	width   int
	height  int
	hash    string
	size    int64
	modTime time.Time
	err     error
}

var errNonImage = errors.New("non-image file")

var imageExtensions = map[string]bool{
	".gif":  true,
	".jpeg": true,
	".jpg":  true,
	".png":  true,
	".webp": true,
}

// Run executes the process command.
func Run(args []string) error {
	fs := flag.NewFlagSet("process", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	input := fs.String("input", defaultInput, "input manifest file, or directory to search recursively for manifest files (default: current directory)")
	output := fs.String("output", "", `output JSON file (default: "processed-<input>" alongside each input); only valid when -input resolves to a single manifest file`)
	workers := fs.Int("workers", defaultWorkers, "number of concurrent image-decode workers")
	checkpointEvery := fs.Int("checkpoint-every", defaultCheckpointEvery, "write a checkpoint after this many unique files (0 disables checkpointing)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser process [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("process: unexpected positional arguments")
	}
	if *workers < 1 {
		return errors.New("process: --workers must be greater than zero")
	}
	if *checkpointEvery < 0 {
		return errors.New("process: --checkpoint-every must not be negative")
	}

	inputPath, err := paths.Abs(*input)
	if err != nil {
		return err
	}

	manifestFiles, err := manifest.FindFiles(inputPath)
	if err != nil {
		return fmt.Errorf("process: finding manifest files under %s: %w", inputPath, err)
	}
	if len(manifestFiles) == 0 {
		fmt.Printf("No manifest files found under %s\n", inputPath)
		return nil
	}
	if *output != "" && len(manifestFiles) > 1 {
		return fmt.Errorf("process: -output cannot be used with %d manifest files found under %s; omit -output to write each result alongside its input", len(manifestFiles), inputPath)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for i, inputFile := range manifestFiles {
		outputFile := *output
		if outputFile == "" {
			outputFile = defaultOutput(inputFile)
		} else {
			outputFile, err = paths.Abs(outputFile)
			if err != nil {
				return err
			}
		}

		if len(manifestFiles) > 1 {
			fmt.Printf("\n[%d/%d] Processing %s\n", i+1, len(manifestFiles), inputFile)
		}
		if err := processImages(ctx, inputFile, outputFile, *workers, *checkpointEvery); err != nil {
			return fmt.Errorf("%s: %w", inputFile, err)
		}
	}

	return nil
}

func defaultOutput(inputFile string) string {
	dir := filepath.Dir(inputFile)
	name := filepath.Base(inputFile)
	return filepath.Join(dir, "processed-"+name)
}

func totalImageReferences(data []manifest.Entry) int {
	count := 0
	for _, item := range data {
		if item.Status != manifest.StatusNonImage &&
			item.Status != manifest.StatusInvalid &&
			item.File != "" {
			count++
		}
	}
	return count
}

// removeStatus returns entries with every record carrying status dropped,
// reusing entries' backing array.
func removeStatus(entries []manifest.Entry, status manifest.Status) []manifest.Entry {
	kept := entries[:0]
	for _, e := range entries {
		if e.Status == status {
			continue
		}
		kept = append(kept, e)
	}
	return kept
}

// removeOutOfTree returns entries with every record whose File lies
// outside baseDir's own directory tree dropped (see manifest.InTree),
// reusing entries' backing array. A manifest only ever describes images in
// its own directory or a subdirectory of it; an entry failing that check
// is stale, left behind by an older version of move or view's Move All
// that rewrote a moved entry's File to point outside the manifest's
// directory instead of removing it.
func removeOutOfTree(entries []manifest.Entry, baseDir string) ([]manifest.Entry, int) {
	kept := entries[:0]
	removed := 0
	for _, e := range entries {
		if e.File != "" && !manifest.InTree(e.File, baseDir) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	return kept, removed
}

func processImages(ctx context.Context, inputFile, outputFile string, maxWorkers, checkpointEvery int) error {
	if maxWorkers < 1 {
		return errors.New("maxWorkers must be greater than zero")
	}
	if checkpointEvery < 0 {
		return errors.New("checkpointEvery must not be negative")
	}

	start := time.Now()
	jsonData, err := os.ReadFile(inputFile)
	if err != nil {
		return fmt.Errorf("reading JSON: %w", err)
	}

	data, err := manifest.Decode(jsonData)
	if err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	manifestDir := filepath.Dir(inputFile)
	if normalized := normalizeFilePaths(data, manifestDir); normalized > 0 {
		fmt.Printf("Normalized %d relative file paths to absolute\n", normalized)
	}

	// inv.db is shared across every image-browser/site-scraper command at
	// one central location rather than one per manifest — see
	// db.DefaultPath.
	dbPath, err := db.DefaultPath()
	if err != nil {
		return fmt.Errorf("resolve inv.db path: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open inv.db: %w", err)
	}
	defer database.Close()
	inv := store.New(database)

	var removedOutOfTree int
	data, removedOutOfTree = removeOutOfTree(data, manifestDir)
	if removedOutOfTree > 0 {
		fmt.Printf("Removed %d records whose file lives outside this manifest's directory\n", removedOutOfTree)
	}

	data, markedMissing, markedNonImage, markedEmpty := classifyFiles(data, maxWorkers)
	if markedMissing > 0 {
		data = removeStatus(data, manifest.StatusMissing)
		fmt.Printf("Removed %d records whose file no longer exists\n", markedMissing)
	}
	if markedEmpty > 0 {
		// Unlike a non-image record (whose File still names a real,
		// in-tree file worth keeping for audit purposes), an entry with no
		// File at all describes nothing: it can't be displayed, moved, or
		// re-validated later, and left in place it's exactly the kind of
		// entry move -preview has no file to resolve for (see move.go's
		// previewMove) — so it's dropped here, the same way a confirmed-
		// missing file is.
		data = removeStatus(data, manifest.StatusInvalid)
		fmt.Printf("Removed %d records with an empty file path\n", markedEmpty)
	}
	fmt.Printf("Marked %d records as non-image\n", markedNonImage)

	fileIndexes := make(map[string][]int, len(data))
	for i, item := range data {
		if item.Status == manifest.StatusNonImage ||
			item.Status == manifest.StatusInvalid {
			continue
		}
		fileIndexes[item.File] = append(fileIndexes[item.File], i)
	}

	totalRecords := len(data)
	totalFiles := len(fileIndexes)
	fmt.Printf("JSON records: %d\n", totalRecords)
	fmt.Printf("Unique image files to process: %d\n", totalFiles)
	fmt.Printf("Duplicate image references: %d\n", totalImageReferences(data)-totalFiles)
	fmt.Printf("Workers: %d\n", maxWorkers)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan job, maxWorkers*2)
	results := make(chan result, maxWorkers*2)
	var workers sync.WaitGroup

	for range maxWorkers {
		workers.Add(1)
		go worker(ctx, jobs, results, &workers)
	}

	go func() {
		defer close(jobs)
		for filePath := range fileIndexes {
			select {
			case jobs <- job{file: filePath}:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	processed, successful, failed := 0, 0, 0
	lastCheckpoint := 0

	for result := range results {
		processed++
		if result.err != nil {
			failed++
			for _, index := range fileIndexes[result.file] {
				data[index].Status = manifest.StatusFailed
			}
			if !errors.Is(result.err, errNonImage) {
				log.Printf("skipping %s: %v", result.file, result.err)
			}
		} else {
			successful++
			for _, index := range fileIndexes[result.file] {
				data[index].Width = result.width
				data[index].Height = result.height
				data[index].Status = manifest.StatusProcessed
			}

			if _, _, err := inv.AddFile(ctx, store.File{
				Path:       result.file,
				Filename:   filepath.Base(result.file),
				Extension:  filepath.Ext(result.file),
				Filesize:   result.size,
				BLAKE3:     sql.NullString{String: result.hash, Valid: result.hash != ""},
				ModifiedAt: sql.NullTime{Time: result.modTime, Valid: !result.modTime.IsZero()},
			}); err != nil {
				// inv.db is a secondary record of what the manifest JSON
				// already captures above; a failure to write it shouldn't
				// fail an otherwise-successful process run.
				log.Printf("record file in inv.db: %s: %v", result.file, err)
			}
		}

		if checkpointEvery > 0 && processed-lastCheckpoint >= checkpointEvery {
			lastCheckpoint = processed
			fmt.Printf("Checkpoint: %d/%d unique files\n", processed, totalFiles)
			if err := writeJSON(outputFile, data); err != nil {
				cancel()
				for range results {
				}
				return fmt.Errorf("writing checkpoint: %w", err)
			}
		}
	}

	if err := writeJSON(outputFile, data); err != nil {
		return fmt.Errorf("writing final JSON: %w", err)
	}

	fmt.Printf("\nProcessing completed in %v\n", time.Since(start))
	fmt.Printf("Unique files processed: %d\n", processed)
	fmt.Printf("Images processed successfully: %d\n", successful)
	fmt.Printf("Files skipped/failed: %d\n", failed)
	return nil
}
