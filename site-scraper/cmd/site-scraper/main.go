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
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"main/internal/httpx"
	"main/internal/manifest"
	"main/internal/textfile"
	"main/scraper"
)

// queryRequest is the JSON shape -query accepts, e.g.
// {"apiURL":"https://example.com/search","query":"cats"}.
type queryRequest struct {
	APIURL string `json:"apiURL"`
	Query  string `json:"query"`
}

// main delegates to run so every deferred cleanup still executes; os.Exit
// itself would skip them.
func main() {
	os.Exit(run())
}

// writeLines writes lines to filename, one per line. A nil or empty lines
// writes an empty file rather than a single blank line.
func writeLines(filename string, lines []string) error {
	if len(lines) == 0 {
		return os.WriteFile(filename, nil, 0644)
	}
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
		query     = flag.String("query", "", `optional JSON {"apiURL":"...","query":"..."} to additionally fetch image URLs from a search API before downloading (default: disabled)`)
		querySave = flag.String("query-save", "", "optional path to additionally save the URLs fetched via -query, one per line (default: not saved)")
	)
	flag.Usage = usage
	flag.Parse()

	if *inputFile == "" || *destDir == "" || *timeout < 0 {
		flag.Usage()
		return 2
	}

	// logger is shared across -query fetching, the downloader itself, and
	// this function's own cleanup, so every warning this run produces goes
	// to the same place in a consistent format.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	urls, err := textfile.ReadLines(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *inputFile, err)
		return 1
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
		// os.RemoveAll already treats a missing path as success (returns
		// nil), so any error here is one worth surfacing.
		if err := os.RemoveAll(*tempDir); err != nil {
			logger.Warn("cleanup tmp dir", "path", *tempDir, "err", err)
		}
	}()

	// ctx is built before the -query fetch below so that phase is bound by
	// -timeout and Ctrl-C/SIGTERM the same as the main download run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	if *query != "" {
		var parsedQuery queryRequest
		if err := json.Unmarshal([]byte(*query), &parsedQuery); err != nil {
			fmt.Fprintf(os.Stderr, "parse -query: %v\n", err)
			return 1
		}
		if parsedQuery.APIURL == "" {
			fmt.Fprintln(os.Stderr, "parse -query: apiURL is required")
			return 1
		}

		// Reuses the same SSRF-hardened client/rate-limit shape as the
		// scraper package (see internal/httpx) rather than an unguarded
		// http.Get, since -query's apiURL is user-supplied input.
		client := httpx.NewClient()
		limiter := rate.NewLimiter(rate.Limit(httpx.Workers), httpx.Workers)

		fetched, err := fetchAllPics(ctx, client, limiter, logger, parsedQuery.APIURL, parsedQuery.Query)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch all pics: %v\n", err)
			return 1
		}

		fetchedURLs := make([]string, 0, len(fetched))
		for _, pic := range fetched {
			fetchedURLs = append(fetchedURLs, pic.GURL)
		}
		urls = append(urls, fetchedURLs...)

		if *querySave != "" {
			if err := writeLines(*querySave, fetchedURLs); err != nil {
				fmt.Fprintf(os.Stderr, "write -query-save: %v\n", err)
				return 1
			}
		}
	}

	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "no URLs to process: -input was empty and -query yielded none")
		return 2
	}

	d, err := scraper.NewDownloader(
		scraper.WithTempDir(*tempDir),
		scraper.WithDestDir(*destDir),
		scraper.WithLogger(logger),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configure downloader: %v\n", err)
		return 1
	}

	mf, err := d.Run(ctx, urls)
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
	for _, e := range mf.Entries {
		switch {
		case e.Status == manifest.StatusKept:
			kept++
		case manifest.IsDuplicate(e.Status):
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
	if err := enc.Encode(mf); err != nil {
		fmt.Fprintf(os.Stderr, "encode manifest: %v\n", err)
		return 1
	}

	return 0
}

// usage prints flag.Usage output; it's installed as flag.Usage in run.
func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s -input urls.txt -temp-dir /tmp/site-scraper -dest-dir ./images

Downloads, deduplicates, and moves images referenced by the URLs in the
input file (a mix of page URLs and direct image URLs, one per line).

Options:
`, filepath.Base(os.Args[0]))

	flag.PrintDefaults()
}
