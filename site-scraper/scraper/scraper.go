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
	"sync"
	"time"

	"golang.org/x/time/rate"

	invlib "inv-lib"

	"main/internal/httpx"
	"main/internal/manifest"
)

// Downloader downloads and deduplicates images. Construct one with
// NewDownloader.
type Downloader struct {
	tempDir          string
	destDir          string
	concurrency      int
	client           *http.Client
	limiter          *rate.Limiter
	logger           *slog.Logger
	classifyProgress func(completed, total int)
	discoverProgress func(completed, total int)
	downloadProgress func(completed, total int)
	entryProgress    func(e manifest.Entry)
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

// WithClassifyProgress registers an optional callback after each input URL is classified.
// Calls are serialized and completed increases monotonically.
func WithClassifyProgress(fn func(completed, total int)) Option {
	return func(d *Downloader) { d.classifyProgress = fn }
}

// WithDiscoverProgress registers an optional callback after each page discovery attempt.
// Calls are serialized and completed increases monotonically.
func WithDiscoverProgress(fn func(completed, total int)) Option {
	return func(d *Downloader) { d.discoverProgress = fn }
}

// WithDownloadProgress registers an optional callback invoked after each
// download attempt completes, whether it succeeds or fails. completed is
// the number of finished attempts so far; total is the number of downloads
// discovered for this run. Calls are serialized and completed increases
// monotonically. The callback runs from the download worker path, so it
// should return quickly.
func WithDownloadProgress(fn func(completed, total int)) Option {
	return func(d *Downloader) { d.downloadProgress = fn }
}

// WithEntryProgress registers an optional callback invoked once for every
// manifest entry Run finalizes - kept, duplicate, or failed - at the moment
// it's decided, rather than only once at the end via Run's returned
// manifest. This is what lets a caller stream failures live instead of
// waiting for the whole run to finish. Calls are serialized; e is safe to
// retain.
func WithEntryProgress(fn func(e manifest.Entry)) Option {
	return func(d *Downloader) { d.entryProgress = fn }
}

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

	// Resolved once here so every downstream use (moveToDestination, the
	// manifest path, and Entry.File/Entry.Original) is absolute regardless
	// of whether the caller passed a relative WithDestDir.
	destDir, err := filepath.Abs(d.destDir)
	if err != nil {
		return nil, fmt.Errorf("scraper: resolve dest dir: %w", err)
	}
	d.destDir = destDir

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

	// Every survivor this run moves gets tagged "site-scraper" plus this
	// run's staging timestamp, so inv.db tooling (e.g. image-browser) can
	// isolate "everything this run landed" without parsing manifest.json.
	// Shares now rather than re-deriving its own timestamp so the tag
	// matches DiscoveredAt exactly for every entry below.
	stagingTag := now.Format("2006-01-02-150405")

	// inv.db is shared across every site-scraper/image-browser/
	// inventory-manager command at one central location rather than one
	// per destDir — see invlib.DefaultPath. manifest.json remains the
	// per-run, per-destDir record; each survivor moved below is
	// additionally recorded into inv.db to keep the two in sync.
	dbPath, err := invlib.DefaultPath()
	if err != nil {
		return nil, fmt.Errorf("scraper: resolve inv.db path: %w", err)
	}
	inv, err := invlib.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("scraper: open inv.db: %w", err)
	}
	defer inv.Close()

	// Phase 1: classify every input as a page or a direct image.
	var classifyMu sync.Mutex
	classifyCompleted := 0
	classifyResults := runBounded(ctx, d.concurrency, urls, func(ctx context.Context, u string) classifyResult {
		isImage, err := classify(ctx, d.client, d.limiter, d.logger, u)

		if d.classifyProgress != nil || d.entryProgress != nil {
			classifyMu.Lock()
			if d.classifyProgress != nil {
				classifyCompleted++
				d.classifyProgress(classifyCompleted, len(urls))
			}
			// A failure here is terminal (see the switch below), so it's
			// reported live now rather than waiting for the sequential
			// loop that builds entries from classifyResults.
			if err != nil && d.entryProgress != nil {
				d.entryProgress(failedEntry(u, u, now, err))
			}
			classifyMu.Unlock()
		}

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
	var discoverMu sync.Mutex
	discoverCompleted := 0
	discoverResults := runBounded(ctx, d.concurrency, pages, func(ctx context.Context, page string) discoverResult {
		urls, err := discoverImageURLs(ctx, d.client, d.limiter, d.logger, page)

		if d.discoverProgress != nil || d.entryProgress != nil {
			discoverMu.Lock()
			if d.discoverProgress != nil {
				discoverCompleted++
				d.discoverProgress(discoverCompleted, len(pages))
			}
			if err != nil && d.entryProgress != nil {
				d.entryProgress(failedEntry(page, page, now, err))
			}
			discoverMu.Unlock()
		}

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
	var progressMu sync.Mutex
	completed := 0
	downloadResults := runBounded(ctx, d.concurrency, work_, func(ctx context.Context, w work) downloadResult {
		f, err := downloadToTemp(ctx, d.client, d.limiter, d.logger, d.tempDir, w)

		if d.downloadProgress != nil || d.entryProgress != nil {
			progressMu.Lock()
			if d.downloadProgress != nil {
				completed++
				d.downloadProgress(completed, len(work_))
			}
			if err != nil && d.entryProgress != nil {
				d.entryProgress(failedEntry(w.site, w.sourceURL, now, err))
			}
			progressMu.Unlock()
		}

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
		e := manifest.Entry{
			Site:         dup.file.work.site,
			SourceURL:    dup.file.work.sourceURL,
			Hash:         dup.ofHash,
			Status:       manifest.DuplicateStatus(dup.ofHash),
			DiscoveredAt: now,
		}
		entries = append(entries, e)
		if d.entryProgress != nil {
			d.entryProgress(e)
		}
		// Left in tempDir on purpose for post-run inspection.
	}

	// Phases 5-6: decode dimensions and move each survivor to destDir.
	for _, s := range survivors {
		width, height, format, dimensionsOK := decodeDimensions(s.path)

		finalName, err := moveToDestination(d.destDir, s.path, s.name)
		if err != nil {
			e := failedEntry(s.work.site, s.work.sourceURL, now, err)
			entries = append(entries, e)
			if d.entryProgress != nil {
				d.entryProgress(e)
			}
			continue
		}

		finalPath := filepath.Join(d.destDir, finalName)

		var originID *int64
		origin := &invlib.Origin{Type: "web"}
		if s.work.site != "" {
			origin.Site = &s.work.site
		}
		if s.work.sourceURL != "" {
			origin.URL = &s.work.sourceURL
			origin.Identifier = &s.work.sourceURL
		}
		originalFile := filepath.Join(d.destDir, s.name)
		origin.OriginalFile = &originalFile
		origin.DiscoveredAt = &now

		if _, err := inv.EnsureOrigin(ctx, origin); err != nil {
			d.logger.Warn("record origin in inv.db", "source_url", s.work.sourceURL, "err", err)
		} else {
			originID = &origin.ID
		}

		extension := filepath.Ext(finalName)
		file := &invlib.File{
			Path:      finalPath,
			Filename:  finalName,
			Extension: &extension,
			Filesize:  s.size,
			OriginID:  originID,
		}
		if s.hash != "" {
			file.Hash = &s.hash
		}

		if _, err := inv.EnsureFile(ctx, file); err != nil {
			// inv.db is a secondary record of what manifest.json already
			// captures below; a failure to write it shouldn't fail an
			// otherwise-successful download.
			d.logger.Warn("record file in inv.db", "path", finalPath, "err", err)
		} else {
			if dimensionsOK {
				img := &invlib.Image{FileID: file.ID, Width: &width, Height: &height}
				if format != "" {
					img.Format = &format
				}
				if _, err := inv.InsertImageIfAbsent(ctx, img); err != nil {
					d.logger.Warn("record image dimensions in inv.db", "path", finalPath, "err", err)
				}
			}
			d.tagFile(ctx, inv, file.ID, finalPath, "site-scraper", stagingTag)
		}

		e := manifest.Entry{
			Site:         s.work.site,
			SourceURL:    s.work.sourceURL,
			File:         finalPath,
			Original:     filepath.Join(d.destDir, s.name),
			Hash:         s.hash,
			Status:       manifest.StatusKept,
			DiscoveredAt: now,
			Width:        width,
			Height:       height,
		}
		entries = append(entries, e)
		if d.entryProgress != nil {
			d.entryProgress(e)
		}
	}

	// Phase 7 (tempDir cleanup) falls out of moveToDestination's os.Rename;
	// phase 8 is the manifest merge below.
	return manifest.Merge(filepath.Join(d.destDir, "manifest.json"), entries)
}

// tagFile attaches each name in names to fileID, creating any tag that
// doesn't already exist (via GetOrCreateTag) and otherwise reusing it.
// Additive and idempotent like the rest of this run's inv.db writes: a
// failure to tag is logged and does not fail an otherwise-successful
// download.
func (d *Downloader) tagFile(ctx context.Context, inv *invlib.DB, fileID int64, path string, names ...string) {
	for _, name := range names {
		tag, err := inv.GetOrCreateTag(ctx, name)
		if err != nil {
			d.logger.Warn("get or create tag in inv.db", "tag", name, "path", path, "err", err)
			continue
		}
		if err := inv.TagFile(ctx, fileID, tag.ID); err != nil {
			d.logger.Warn("tag file in inv.db", "tag", name, "path", path, "err", err)
		}
	}
}

func failedEntry(site, sourceURL string, at time.Time, err error) manifest.Entry {
	return manifest.Entry{
		Site:         site,
		SourceURL:    sourceURL,
		Status:       manifest.FailedStatus(err),
		Error:        err.Error(),
		DiscoveredAt: at,
	}
}
