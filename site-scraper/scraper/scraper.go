// Package scraper downloads images from a mix of website and direct-image
// URLs, deduplicates them, and lands the survivors in a destination
// directory alongside a JSON manifest.
package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/time/rate"

	"main/internal/httpx"
	"main/internal/manifest"
)

// Downloader downloads and deduplicates images. Construct one with
// NewDownloader.
type Downloader struct {
	tempDir     string
	destDir     string
	concurrency int
	client      *http.Client
	limiter     *rate.Limiter
	logger      *slog.Logger
}

// Option configures a Downloader.
type Option func(*Downloader)

// WithTempDir sets the scratch directory downloads land in before
// dedup/move. Required.
func WithTempDir(path string) Option { return func(d *Downloader) { d.tempDir = path } }

// WithDestDir sets the directory deduplicated survivors are moved into,
// alongside manifest.json. Required.
func WithDestDir(path string) Option { return func(d *Downloader) { d.destDir = path } }

// WithConcurrency bounds how many classify/discover/download calls run at
// once. Defaults to httpx.Workers.
func WithConcurrency(n int) Option { return func(d *Downloader) { d.concurrency = n } }

// WithHTTPClient overrides the default SSRF-hardened client from
// httpx.NewClient.
func WithHTTPClient(c *http.Client) Option { return func(d *Downloader) { d.client = c } }

// WithRateLimiter overrides the default rate limiter (concurrency
// requests/sec, burst concurrency).
func WithRateLimiter(l *rate.Limiter) Option { return func(d *Downloader) { d.limiter = l } }

// WithLogger overrides the default discard logger, e.g. to surface
// httpx's retry/rate-limit warnings.
func WithLogger(l *slog.Logger) Option { return func(d *Downloader) { d.logger = l } }

// NewDownloader builds a Downloader from opts. WithTempDir and WithDestDir
// are required; both directories are created if missing.
func NewDownloader(opts ...Option) (*Downloader, error) {
	d := &Downloader{concurrency: httpx.Workers}
	for _, opt := range opts {
		opt(d)
	}

	if d.tempDir == "" {
		return nil, fmt.Errorf("scraper: WithTempDir is required")
	}
	if d.destDir == "" {
		return nil, fmt.Errorf("scraper: WithDestDir is required")
	}
	if d.concurrency <= 0 {
		return nil, fmt.Errorf("scraper: concurrency must be positive, got %d", d.concurrency)
	}

	// should create the temporary and destination directories if they don't exist.
	// tmpdir, err := os.MkdirTemp(".", "S-S_")
	if err := os.MkdirAll(d.tempDir, 0o755); err != nil {
		return nil, fmt.Errorf("scraper: create temp dir: %w", err)
	}
	if err := os.MkdirAll(d.destDir, 0o755); err != nil {
		return nil, fmt.Errorf("scraper: create dest dir: %w", err)
	}

	if d.client == nil {
		d.client = httpx.NewClient()
	}
	if d.limiter == nil {
		d.limiter = rate.NewLimiter(rate.Limit(d.concurrency), d.concurrency)
	}
	if d.logger == nil {
		d.logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	return d, nil
}

type classifyResult struct {
	input   string
	isImage bool
	err     error
}

type discoverResult struct {
	page string
	urls []string
	err  error
}

type downloadResult struct {
	work work
	file downloadedFile
	err  error
}

// Run downloads every image discoverable from urls (a mix of page URLs and
// direct image URLs), deduplicates them, moves survivors into destDir, and
// merges the outcome into <destDir>/manifest.json.
func (d *Downloader) Run(ctx context.Context, urls []string) (*manifest.Manifest, error) {
	now := time.Now()
	var entries []manifest.Entry

	// Phase 1: classify every input as a page or a direct image.
	classifyResults := runBounded(ctx, d.concurrency, urls, func(ctx context.Context, u string) classifyResult {
		isImage, err := classify(ctx, d.client, d.limiter, d.logger, u)
		return classifyResult{input: u, isImage: isImage, err: err}
	})

	var pages []string
	var work_ []work
	for _, r := range classifyResults {
		switch {
		case r.err != nil:
			entries = append(entries, failedEntry(r.input, r.input, now, r.err))
		case r.isImage:
			work_ = append(work_, work{site: r.input, sourceURL: r.input})
		default:
			pages = append(pages, r.input)
		}
	}

	// Phase 2: discover every page's images before downloading anything.
	discoverResults := runBounded(ctx, d.concurrency, pages, func(ctx context.Context, page string) discoverResult {
		urls, err := discoverImageURLs(ctx, d.client, d.limiter, d.logger, page)
		return discoverResult{page: page, urls: urls, err: err}
	})

	for _, r := range discoverResults {
		if r.err != nil {
			entries = append(entries, failedEntry(r.page, r.page, now, r.err))
			continue
		}
		for _, u := range r.urls {
			work_ = append(work_, work{site: r.page, sourceURL: u})
		}
	}

	// Phase 3: download everything discovered into tempDir.
	downloadResults := runBounded(ctx, d.concurrency, work_, func(ctx context.Context, w work) downloadResult {
		f, err := downloadToTemp(ctx, d.client, d.limiter, d.logger, d.tempDir, w)
		return downloadResult{work: w, file: f, err: err}
	})

	var downloaded []downloadedFile
	for _, r := range downloadResults {
		if r.err != nil {
			entries = append(entries, failedEntry(r.work.site, r.work.sourceURL, now, r.err))
			continue
		}
		downloaded = append(downloaded, r.file)
	}

	// Phase 4: deduplicate within this run's tempDir contents.
	survivors, dupes, err := dedupe(downloaded)
	if err != nil {
		return nil, fmt.Errorf("scraper: dedupe: %w", err)
	}

	for _, dup := range dupes {
		entries = append(entries, manifest.Entry{
			Site:         dup.file.work.site,
			SourceURL:    dup.file.work.sourceURL,
			Hash:         dup.ofHash,
			Status:       "duplicate-of:" + dup.ofHash,
			DiscoveredAt: now,
		})
		// Left in tempDir on purpose for post-run inspection.
	}

	// Phases 5-6: decode dimensions and move each survivor to destDir.
	for _, s := range survivors {
		width, height, _ := decodeDimensions(s.path)

		finalName, err := moveToDestination(d.destDir, s.path, s.name)
		if err != nil {
			entries = append(entries, failedEntry(s.work.site, s.work.sourceURL, now, err))
			continue
		}

		entries = append(entries, manifest.Entry{
			Site:         s.work.site,
			SourceURL:    s.work.sourceURL,
			File:         finalName,
			Original:     s.name,
			Hash:         s.hash,
			Status:       "kept",
			DiscoveredAt: now,
			Width:        width,
			Height:       height,
		})
	}

	// Phase 7 (tempDir cleanup) falls out of moveToDestination's os.Rename;
	// phase 8 is the manifest merge below.
	return manifest.Merge(filepath.Join(d.destDir, "manifest.json"), entries)
}

func failedEntry(site, sourceURL string, at time.Time, err error) manifest.Entry {
	return manifest.Entry{
		Site:         site,
		SourceURL:    sourceURL,
		Status:       "download-failed:" + err.Error(),
		Error:        err.Error(),
		DiscoveredAt: at,
	}
}
