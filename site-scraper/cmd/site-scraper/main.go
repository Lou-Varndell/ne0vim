// Command site-scraper is a thin example consumer of the scraper library:
// it reads a list of website/image URLs from a file and downloads,
// deduplicates, and lands the images in a destination directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"main/internal/textfile"
	"main/scraper"
)

func main() {
	// Generate a staging directory name based on the current timestamp.
	staging := time.Now().Format("2006/01/02/150405")

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve home directory: %v\n", err)
		os.Exit(1)
	}
	tmpdir, err := os.MkdirTemp("", "S-S_")
	if err != nil {
		fmt.Fprintf(os.Stderr, "mktemp: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if err := os.RemoveAll(tmpdir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("cleanup tmp dir", "path", tmpdir, "err", err)
		}
	}()
	// Use the home directory as a base for the staging directory.

	var (
		inputFile = flag.String("input", "", "input file containing website/image URLs, one per line")
		tempDir   = flag.String("temp-dir", tmpdir, "scratch directory for in-progress downloads (required)")
		destDir   = flag.String("dest-dir", filepath.Join(home, "Images", staging), "directory to move deduplicated images into (required)")
	)
	// os.MkdirTemp()
	flag.Usage = usage
	flag.Parse()

	if *inputFile == "" || *tempDir == "" || *destDir == "" {
		flag.Usage()
		os.Exit(2)
	}

	urls, err := textfile.ReadLines(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *inputFile, err)
		os.Exit(1)
	}

	d, err := scraper.NewDownloader(
		scraper.WithTempDir(*tempDir),
		scraper.WithDestDir(*destDir),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure downloader: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	manifest, err := d.Run(ctx, urls)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run: %v\n", err)
		os.Exit(1)
	}

	kept, failed, duplicate := 0, 0, 0
	for _, e := range manifest.Entries {
		switch {
		case e.Status == "kept":
			kept++
		case len(e.Status) >= len("duplicate-of:") && e.Status[:len("duplicate-of:")] == "duplicate-of:":
			duplicate++
		default:
			failed++
		}
	}

	fmt.Printf("kept=%d duplicate=%d failed=%d manifest=%s\n",
		kept, duplicate, failed, filepath.Join(*destDir, "manifest.json"))

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(manifest)
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s -input urls.txt -temp-dir /tmp/site-scraper -dest-dir ./images

Downloads, deduplicates, and moves images referenced by the URLs in the
input file (a mix of page URLs and direct image URLs, one per line).

Options:
`, filepath.Base(os.Args[0]))

	flag.PrintDefaults()
}
