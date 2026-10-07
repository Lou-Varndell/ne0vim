// Package process implements the image-browser process command: a
// database-native backfill that fills in width, height, format, and
// color-space for every files row a scan recorded but never successfully
// decoded as an image (e.g. a file matched by extension but was actually
// corrupt or renamed at scan time).
package process

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"image-browser/internal/db"
	"image-browser/internal/paths"
	"image-browser/internal/store"
)

const defaultWorkers = 8

type job struct {
	id   int64
	file string
}

type result struct {
	id     int64
	file   string
	width  int
	height int
	format string
	space  string
	err    error
}

var imageExtensions = map[string]bool{
	".gif":  true,
	".jpeg": true,
	".jpg":  true,
	".png":  true,
	".webp": true,
}

func isImageFile(path string) bool {
	return imageExtensions[strings.ToLower(filepath.Ext(path))]
}

// underRoot reports whether path is root itself or lives somewhere under
// it.
func underRoot(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

// Run executes the process command.
func Run(args []string) error {
	fs := flag.NewFlagSet("process", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "only process files under this directory (default: every unprocessed file in the database)")
	workers := fs.Int("workers", defaultWorkers, "number of concurrent image-decode workers")

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

	var rootPath string
	if *root != "" {
		var err error
		rootPath, err = paths.Abs(*root)
		if err != nil {
			return fmt.Errorf("process: resolving root: %w", err)
		}
	}

	dbPath, err := db.DefaultPath()
	if err != nil {
		return fmt.Errorf("process: resolve inv.db path: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("process: open inv.db: %w", err)
	}
	defer database.Close()
	inv := store.New(database)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return processImages(ctx, inv, rootPath, *workers)
}

func processImages(ctx context.Context, inv *store.Store, rootPath string, maxWorkers int) error {
	if maxWorkers < 1 {
		return errors.New("maxWorkers must be greater than zero")
	}

	start := time.Now()

	candidates, err := inv.UnprocessedImages(ctx)
	if err != nil {
		return fmt.Errorf("listing unprocessed files: %w", err)
	}

	var files []store.File
	var skippedNonImage int
	for _, f := range candidates {
		if rootPath != "" && !underRoot(f.Path, rootPath) {
			continue
		}
		if !isImageFile(f.Path) {
			skippedNonImage++
			continue
		}
		files = append(files, f)
	}

	fmt.Printf("Files without an images row: %d\n", len(candidates))
	fmt.Printf("Non-image files skipped: %d\n", skippedNonImage)
	fmt.Printf("Images to process: %d\n", len(files))
	fmt.Printf("Workers: %d\n", maxWorkers)

	if len(files) == 0 {
		fmt.Println("Nothing to process.")
		return nil
	}

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
		for _, f := range files {
			select {
			case jobs <- job{id: f.ID, file: f.Path}:
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

	for res := range results {
		processed++

		if res.err != nil {
			failed++
			if err := inv.SetFileStatus(ctx, res.id, "failed", res.err.Error()); err != nil {
				log.Printf("record failure in inv.db: %s: %v", res.file, err)
			}
			log.Printf("skipping %s: %v", res.file, res.err)
			continue
		}

		successful++
		if _, err := inv.AddImage(ctx, store.Image{
			FileID:     res.id,
			Width:      res.width,
			Height:     res.height,
			Format:     res.format,
			ColorSpace: res.space,
		}); err != nil {
			log.Printf("record image in inv.db: %s: %v", res.file, err)
		}
		if err := inv.SetFileStatus(ctx, res.id, "processed", ""); err != nil {
			log.Printf("record status in inv.db: %s: %v", res.file, err)
		}
	}

	fmt.Printf("\nProcessing completed in %v\n", time.Since(start))
	fmt.Printf("Files processed: %d\n", processed)
	fmt.Printf("Images processed successfully: %d\n", successful)
	fmt.Printf("Files failed: %d\n", failed)
	return nil
}
