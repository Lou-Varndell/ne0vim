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

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"golang.org/x/time/rate"

	"main/internal/httpx"
	"main/internal/manifest"
	"main/internal/textfile"
	"main/scraper"
)

// shard 4,096 starting point
// shard next step 8,192 entries per directory

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

	urls, err := textfile.ReadLines(*inputFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", *inputFile, err)
		return 1
	}

	// -query is parsed up front so a malformed flag fails fast, before the
	// TUI below takes over the terminal.
	var parsedQuery queryRequest
	if *query != "" {
		if err := json.Unmarshal([]byte(*query), &parsedQuery); err != nil {
			fmt.Fprintf(os.Stderr, "parse -query: %v\n", err)
			return 1
		}
		if parsedQuery.APIURL == "" {
			fmt.Fprintln(os.Stderr, "parse -query: apiURL is required")
			return 1
		}
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
		// nil), so any error here is one worth surfacing. The TUI has
		// already restored the terminal by the time this runs, so stderr
		// is safe to write to directly.
		if err := os.RemoveAll(*tempDir); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup tmp dir %s: %v\n", *tempDir, err)
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

	// The TUI needs to read keypresses from a real controlling terminal
	// (bubbletea opens /dev/tty directly); fall back to the original plain
	// stderr logger and one-line-per-phase progress when stdin/stdout
	// aren't one, e.g. piped output, CI, or a cron job.
	interactive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())

	var queryPtr *queryRequest
	if *query != "" {
		queryPtr = &parsedQuery
	}

	var out scrapeOutcome
	if interactive {
		// runCtx adds a second, TUI-driven cancellation path on top of
		// -timeout/SIGTERM: pressing q/ctrl+c in the TUI (see tui.go)
		// cancels this context without tearing down the signal/timeout
		// context above, since bubbletea's raw terminal mode intercepts
		// ctrl+c as a keypress rather than letting it raise SIGINT.
		runCtx, cancelRun := context.WithCancel(ctx)
		defer cancelRun()

		m := newModel(cancelRun)
		p := tea.NewProgram(m, tea.WithAltScreen())

		// logger forwards every warning line into the TUI's log pane (Tab
		// to view) instead of stderr, which the alt-screen render would
		// otherwise corrupt.
		logger := slog.New(slog.NewTextHandler(teaLogWriter{program: p}, nil))

		results := make(chan scrapeOutcome, 1)
		go func() {
			o := doScrape(runCtx, urls, queryPtr, *querySave, *tempDir, *destDir, *timeout, logger,
				func(phase, completed, total int) {
					p.Send(progressMsg{phase: phase, completed: completed, total: total})
				},
				func(e manifest.Entry) {
					p.Send(entryMsg(e))
				})
			results <- o
			p.Send(doneMsg{mf: o.mf, err: o.err})
		}()

		finalModel, err := p.Run()
		if err != nil {
			fmt.Fprintf(os.Stderr, "tui: %v\n", err)
			return 1
		}

		// A force-quit (second q/ctrl+c while canceling) exits immediately
		// without waiting for the goroutine above, so there's no outcome
		// to read and nothing safe left to clean up or print.
		if fm := finalModel.(model); fm.forceQuit {
			fmt.Fprintln(os.Stderr, "exiting without waiting for the in-progress run to finish cleanly")
			return 130
		}

		out = <-results
	} else {
		logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
		out = doScrape(ctx, urls, queryPtr, *querySave, *tempDir, *destDir, *timeout, logger,
			func(phase, completed, total int) {
				if total > 0 && completed == total {
					fmt.Fprintf(os.Stderr, "%s: %d/%d\n", phaseLabels[phase], completed, total)
				}
			},
			func(e manifest.Entry) {
				// Mirrors the TUI's live Failures pane for the
				// non-interactive path: print failures/duplicates as they
				// happen rather than only in the final summary.
				if e.Status == manifest.StatusKept {
					return
				}
				detail := e.Status
				if e.Error != "" {
					detail = e.Error
				}
				fmt.Fprintf(os.Stderr, "%s: %s (%s)\n", entryKind(e), e.SourceURL, detail)
			})
	}

	keepTempDir = keepTempDir || out.keepTemp

	if out.message != "" {
		fmt.Fprintln(os.Stderr, out.message)
	}
	if out.exitCode != 0 {
		return out.exitCode
	}

	kept, failed, duplicate := 0, 0, 0
	for _, e := range out.mf.Entries {
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
		fmt.Fprintf(os.Stderr, "duplicate raw files retained for inspection in %s\n", *tempDir)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out.mf); err != nil {
		fmt.Fprintf(os.Stderr, "encode manifest: %v\n", err)
		return 1
	}

	return 0
}

// scrapeOutcome carries everything run() needs to finish up once the scrape
// is done, whether doScrape ran synchronously (non-interactive) or in the
// TUI's background goroutine.
type scrapeOutcome struct {
	mf       *manifest.Manifest
	err      error
	message  string
	exitCode int
	keepTemp bool
}

// doScrape runs the -query fetch (if query is non-nil) followed by the full
// classify/discover/download run, returning once everything is done.
// onProgress is called with a phaseClassify/phaseDiscover/phaseDownload
// phase index on every scraper.With*Progress callback; onEntry is called
// once per manifest entry (kept, duplicate, or failed) the moment Run
// finalizes it, well before Run itself returns. Either may be nil.
func doScrape(ctx context.Context, urls []string, query *queryRequest, querySave, tempDir, destDir string, timeout time.Duration, logger *slog.Logger, onProgress func(phase, completed, total int), onEntry func(manifest.Entry)) scrapeOutcome {
	if query != nil {
		// Reuses the same SSRF-hardened client/rate-limit shape as the
		// scraper package (see internal/httpx) rather than an unguarded
		// http.Get, since -query's apiURL is user-supplied input.
		client := httpx.NewClient()
		limiter := rate.NewLimiter(rate.Limit(httpx.Workers), httpx.Workers)

		fetched, err := fetchAllPics(ctx, client, limiter, logger, query.APIURL, query.Query)
		if err != nil {
			return scrapeOutcome{err: err, message: fmt.Sprintf("fetch all pics: %v", err), exitCode: 1}
		}

		fetchedURLs := make([]string, 0, len(fetched))
		for _, pic := range fetched {
			fetchedURLs = append(fetchedURLs, pic.GURL)
		}
		urls = append(urls, fetchedURLs...)

		if querySave != "" {
			if err := writeLines(querySave, fetchedURLs); err != nil {
				return scrapeOutcome{err: err, message: fmt.Sprintf("write -query-save: %v", err), exitCode: 1}
			}
		}
	}

	if len(urls) == 0 {
		err := errors.New("no URLs to process: -input was empty and -query yielded none")
		return scrapeOutcome{err: err, message: err.Error(), exitCode: 2}
	}

	// phaseProgress builds a scraper.With*Progress callback that reports
	// through onProgress tagged with phase, without onProgress itself
	// needing to know which phase it's being called for.
	phaseProgress := func(phase int) func(completed, total int) {
		return func(completed, total int) {
			if onProgress != nil {
				onProgress(phase, completed, total)
			}
		}
	}

	d, err := scraper.NewDownloader(
		scraper.WithTempDir(tempDir),
		scraper.WithDestDir(destDir),
		scraper.WithLogger(logger),
		scraper.WithClassifyProgress(phaseProgress(phaseClassify)),
		scraper.WithDiscoverProgress(phaseProgress(phaseDiscover)),
		scraper.WithDownloadProgress(phaseProgress(phaseDownload)),
		scraper.WithEntryProgress(func(e manifest.Entry) {
			if onEntry != nil {
				onEntry(e)
			}
		}),
	)
	if err != nil {
		return scrapeOutcome{err: err, message: fmt.Sprintf("configure downloader: %v", err), exitCode: 1}
	}

	mf, err := d.Run(ctx, urls)
	if err != nil {
		var message string
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			message = fmt.Sprintf("run: exceeded -timeout of %s", timeout)
		case errors.Is(err, context.Canceled):
			message = "run: canceled"
		default:
			message = fmt.Sprintf("run: %v", err)
		}
		return scrapeOutcome{err: err, message: message, exitCode: 1, keepTemp: true}
	}

	duplicate := 0
	for _, e := range mf.Entries {
		if manifest.IsDuplicate(e.Status) {
			duplicate++
		}
	}

	return scrapeOutcome{mf: mf, keepTemp: duplicate > 0}
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
