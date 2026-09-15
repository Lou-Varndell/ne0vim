// Package process implements the image-manifest dimension processor.
package process

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"image-browser/internal/manifest"
	"image-browser/internal/paths"
)

const (
	defaultInput           = "image-manifest.json"
	defaultWorkers         = 8
	defaultCheckpointEvery = 5000
)

type job struct {
	file string
}

type result struct {
	file   string
	width  int
	height int
	err    error
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

	input := fs.String("input", defaultInput, "input JSON file")
	output := fs.String("output", "", `output JSON file (default: "processed-<input>" alongside the input)`)
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

	inputFile, err := paths.Abs(*input)
	if err != nil {
		return err
	}

	outputFile := *output
	if outputFile == "" {
		outputFile = defaultOutput(inputFile)
	} else {
		outputFile, err = paths.Abs(outputFile)
		if err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return processImages(ctx, inputFile, outputFile, *workers, *checkpointEvery)
}

func defaultOutput(inputFile string) string {
	dir := filepath.Dir(inputFile)
	name := filepath.Base(inputFile)
	return filepath.Join(dir, "processed-"+name)
}

func totalImageReferences(data []manifest.Entry) int {
	count := 0
	for _, item := range data {
		if item.Status != manifest.StatusMissing &&
			item.Status != manifest.StatusNonImage &&
			item.Status != manifest.StatusInvalid &&
			item.File != "" {
			count++
		}
	}
	return count
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

	var data []manifest.Entry
	if err := json.Unmarshal(jsonData, &data); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	data, markedMissing, markedNonImage, markedEmpty := classifyFiles(data, maxWorkers)
	fmt.Printf("Marked %d records as missing\n", markedMissing)
	fmt.Printf("Marked %d records as non-image\n", markedNonImage)
	fmt.Printf("Marked %d records with an empty file path as invalid\n", markedEmpty)

	fileIndexes := make(map[string][]int, len(data))
	for i, item := range data {
		if item.Status == manifest.StatusMissing ||
			item.Status == manifest.StatusNonImage ||
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
