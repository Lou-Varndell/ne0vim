// Command site-scraper crawls a set of sites (or a gallery API) and
// downloads the images they reference.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"main/internal/applog"
	"main/internal/httpx"
	"main/internal/textfile"
)

const maxLogBytes = 10 << 20

func main() {
	staging := time.Now().Format("2006/01/02/150405")

	var inputFile = flag.String("input", "", "input file containing URLs")

	flag.Usage = usage
	flag.Parse()

	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve home directory: %v\n", err)
		os.Exit(1)
	}

	appDir := filepath.Join(home, ".local", "share", "site-scraper")

	if err := os.MkdirAll(appDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "create application directory: %v\n", err)
		os.Exit(1)
	}

	logPath := filepath.Join(appDir, "logs.jsonl")

	logger, logFile, err := applog.Setup(logPath, maxLogBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "set up logger: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var sites []string

	switch {
	case *inputFile != "":
		sites, err = textfile.ReadLines(*inputFile)
		if err != nil {
			logger.Error(
				"reading",
				"inputFile", *inputFile,
				"err", err,
			)
			os.Exit(1)
		}

	default:
		flag.Usage()
		os.Exit(2)
	}

	for _, rawURL := range sites {
		client := &http.Client{}
		req, err := httpx.NewRequest(ctx, http.MethodGet, rawURL)
		if err != nil {
			err = fmt.Errorf("create request: %w", err)
			return
		}

		resp, err := httpx.Do(ctx, client, logger, req)
		if err != nil {
			err = fmt.Errorf("fetch %s: %w", rawURL, err)
			logger.Error("failed to fetch page", "url", rawURL, "error", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			logger.Error("bad status for page", "url", rawURL, "status", resp.Status)
			return
		}
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  %s [options]

Downloads and processes galleries.

Options:
`, filepath.Base(os.Args[0]))

	flag.PrintDefaults()

	fmt.Fprintln(os.Stderr, `
Examples:
  myscraper -site https://example.com/gallery
  myscraper -api https://example.com/api/gallery
  myscraper -input urls.txt`)
}
