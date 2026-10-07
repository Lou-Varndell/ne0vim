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
	"strings"
	"syscall"
	"time"

	"main/internal/textfile"
	"main/scraper"
)

type Pics struct {
	apiURL string
	query  string
}

func main() {
	os.Exit(run())
}

func writeLines(filename string, lines []string) error {
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(filename, []byte(content), 0644)
}

// run does the work of main and returns the process exit code, so every
// return path still runs its deferred cleanup (unlike os.Exit).
func run() int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve home directory: %v\n", err)
		return 1
	}
	staging := time.Now().Format("2006/01/02/150405")

	var (
		inputFile = flag.String("input", "", "input file containing website/image URLs, one per line (required)")
		tempDir   = flag.String("temp-dir", "", "scratch directory for in-progress downloads (default: a fresh OS temp dir, removed unless duplicates were found or the run failed)")
		destDir   = flag.String("dest-dir", filepath.Join(home, "Images", staging), "directory to move deduplicated images into")
		timeout   = flag.Duration("timeout", 0, "overall run deadline; must be >= 0, 0 disables the timeout")
		query     = flag.String("query", "none", "search query for fetching by query (optional)")
	)
	flag.Usage = usage
	flag.Parse()

	if *inputFile == "" || *destDir == "" || *timeout < 0 {
		flag.Usage()
		return 2
	}

	usedDefaultTempDir := *tempDir == ""
	if usedDefaultTempDir {
		created, err := os.MkdirTemp("", "S-S_")
		if err != nil {
			fmt.Fprintf(os.Stderr, "mktemp: %v\n", err)
			return 1
		}
		*tempDir = created
	}

	// keepTempDir stays true for a caller-supplied -temp-dir (not ours to
	// remove) and flips true for our own default dir once it holds anything
	// worth inspecting: a failed run, or duplicates the scraper deliberately
	// leaves behind for review.
	keepTempDir := !usedDefaultTempDir
	defer func() {
		if keepTempDir {
			return
		}
		if err := os.RemoveAll(*tempDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("cleanup tmp dir", "path", *tempDir, "err", err)
		}
	}()

	urls, err := textfile.ReadLines(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *inputFile, err)
		return 1
	}

	if *query != "none" {
		var ppURLS []string
		var picsURL Pics

		err := json.Unmarshal([]byte(*query), &picsURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal query: %v\n", err)
			return 1
		}

		pics, err := fetchAllPics(picsURL.apiURL, picsURL.query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch all pics: %v\n", err)
			return 1
		}
		for _, pic := range pics {
			ppURLS = append(ppURLS, pic.GURL)
		}
		urls = append(urls, ppURLS...)
		if err = writeLines("/Users/boomer/dev/go/site-scraper/input.txt", ppURLS); err != nil {
			fmt.Fprintf(os.Stderr, "write lines: %v\n", err)
			return 1
		}
	}

	d, err := scraper.NewDownloader(
		scraper.WithTempDir(*tempDir),
		scraper.WithDestDir(*destDir),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure downloader: %v\n", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	manifest, err := d.Run(ctx, urls)
	if err != nil {
		keepTempDir = true
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			fmt.Fprintf(os.Stderr, "run: exceeded -timeout of %s\n", *timeout)
		case errors.Is(err, context.Canceled):
			fmt.Fprintln(os.Stderr, "run: canceled")
		default:
			fmt.Fprintf(os.Stderr, "run: %v\n", err)
		}
		return 1
	}

	kept, failed, duplicate := 0, 0, 0
	for _, e := range manifest.Entries {
		switch {
		case e.Status == "kept":
			kept++
		case strings.HasPrefix(e.Status, "duplicate-of:"):
			duplicate++
		default:
			failed++
		}
	}

	fmt.Fprintf(os.Stderr, "kept=%d duplicate=%d failed=%d manifest=%s\n",
		kept, duplicate, failed, filepath.Join(*destDir, "manifest.json"))
	if duplicate > 0 {
		keepTempDir = true
		fmt.Fprintf(os.Stderr, "duplicate raw files retained for inspection in %s\n", *tempDir)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		fmt.Fprintf(os.Stderr, "encode manifest: %v\n", err)
		return 1
	}

	return 0
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
