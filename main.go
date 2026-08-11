package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"
	"lukechampine.com/blake3"
)

const (
	dialTimeout = 10 * time.Second
	readTimeout = 60 * time.Second
	workers     = 8
	userAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) " +
		"AppleWebKit/537.36 (KHTML, like Gecko) " +
		"Chrome/148.0.0.0 Safari/537.36"
	maxRetries  = 4
	baseBackoff = time.Second

	maxPageBytes     = 50 << 20
	maxDownloadBytes = 200 << 20
)

var logger *slog.Logger
var staging string

type crawlResult struct {
	Site   string
	Images map[string]struct{}
	Links  map[string]struct{}
}

// galleryItem mirrors one entry returned by a -api gallery endpoint: GURL is the
// gallery/site page, TURL/TURL460 are preview thumbnails, not images to keep.
type galleryItem struct {
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	GURL     string `json:"g_url"`
	TURL     string `json:"t_url"`
	TURL460  string `json:"t_url_460"`
	TID      int    `json:"tid"`
	GID      int    `json:"gid"`
	MID      string `json:"mid"`
	H        int    `json:"h"`
	Position int    `json:"position"`
}

// apiManifestEntry pairs a fetched gallery item with its downloaded preview so a
// human can browse previews and decide which GURLs are worth scraping next.
type apiManifestEntry struct {
	GID         int    `json:"gid"`
	TID         int    `json:"tid"`
	Title       string `json:"title"`
	Desc        string `json:"desc"`
	SiteURL     string `json:"site_url"`
	PreviewPath string `json:"preview_path,omitempty"`
	PreviewErr  string `json:"preview_error,omitempty"`
}

type contentCache struct {
	mu   sync.Mutex
	seen map[[32]byte]string
}

// pageFetch holds the outcome of fetching one URL, computed at most once.
type pageFetch struct {
	once        sync.Once
	statusCode  int
	contentType string
	body        []byte
	finalURL    *url.URL
	err         error
}

// pageCache deduplicates concurrent and repeat fetches of the same URL within a
// single run, so a page is downloaded once and its body is reused by every
// caller that needs it (e.g. an href that also appears as an <img> parent link).
type pageCache struct {
	mu    sync.Mutex
	pages map[string]*pageFetch
}

type Groups map[string][]string

type imageManifestEntry struct {
	Site       string    `json:"site"`
	SourceURL  string    `json:"source_url"`
	File       string    `json:"file,omitempty"`
	Hash       string    `json:"hash,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
	Discovered time.Time `json:"discovered_at"`
}

type imageDownload struct {
	Site       string
	SourceURL  string
	Filename   string
	Discovered time.Time

	Hash   string
	Status string
	Error  string
}

func (c *contentCache) check(hash [32]byte, filename string) (duplicate bool, original string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if orig, ok := c.seen[hash]; ok {
		return true, orig
	}
	c.seen[hash] = filename
	return false, ""
}

func collectURLsFromHTML(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, cache *pageCache, site string) (*crawlResult, error) {
	result := &crawlResult{
		Site:   site,
		Images: make(map[string]struct{}),
		Links:  make(map[string]struct{}),
	}

	siteURL, err := url.Parse(strings.TrimSpace(site))
	if err != nil {
		return nil, fmt.Errorf("parse site URL: %w", err)
	}

	page, err := cache.fetch(ctx, client, limiter, logger, siteURL.String())
	if err != nil {
		return nil, err
	}
	if page.statusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %d", page.statusCode)
	}

	werr := os.WriteFile("temp.html", page.body, 0644)
	if werr != nil {
		return nil, fmt.Errorf("write temp file: %w", werr)
	}

	// Use the final URL after any redirects as the base for resolving relative refs,
	// and to scope link-following to the same host (avoid following scraped content
	// to attacker-influenced or unrelated destinations).
	base := page.finalURL

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(page.body))
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	doc.Find("img").Each(func(_ int, s *goquery.Selection) {
		if u, ok := imageURL(base, s); ok {
			result.Images[u] = struct{}{}
		}
		if a, ok := findParentAnchor(s); ok {
			if href, ok := a.Attr("href"); ok {
				if abs, ok := resolveURL(base, href); ok {
					result.Images[abs] = struct{}{}
				}
			}
		}
	})

	doc.Find("a").Each(func(_ int, s *goquery.Selection) {
		link, ok := s.Attr("href")
		if !ok {
			return
		}

		abs, ok := resolveURL(base, link)
		if !ok {
			return
		}

		linkURL, err := url.Parse(abs)
		if err != nil || !strings.EqualFold(linkURL.Hostname(), base.Hostname()) {
			return
		}

		linkPage, err := cache.fetch(ctx, client, limiter, logger, abs)
		if err != nil || linkPage.statusCode != http.StatusOK || strings.HasPrefix(linkPage.contentType, "image/") {
			return
		}

		result.Links[abs] = struct{}{}
	})

	return result, nil
}

func createClient() *http.Client {
	dialer := &net.Dialer{Timeout: dialTimeout}
	return &http.Client{
		Timeout: readTimeout,
		Transport: &http.Transport{
			DialContext:         dialer.DialContext,
			MaxIdleConns:        32,
			MaxIdleConnsPerHost: 8,
			MaxConnsPerHost:     4,
			IdleConnTimeout:     30 * time.Second,
		},
	}
}

func doRequest(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, req *http.Request) (*http.Response, error) {
	if err := limiter.Wait(ctx); err != nil {
		return nil, err
	}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		resp.Body.Close()

		var wait time.Duration

		if retry := resp.Header.Get("Retry-After"); retry != "" {
			if secs, err := strconv.Atoi(retry); err == nil {
				wait = time.Duration(secs) * time.Second
			} else if t, err := http.ParseTime(retry); err == nil {
				wait = time.Until(t)
			}
		}

		if wait <= 0 {
			wait = baseBackoff << attempt
			wait += time.Duration(rand.Int63n(int64(500 * time.Millisecond)))
		}

		logger.Warn("rate limited",
			"url", req.URL,
			"attempt", attempt+1,
			"retry_in", wait)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}

	return nil, fmt.Errorf("too many retries after 429")
}

func downloadFile(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, cache *contentCache, rawURL, filename string) (hash string, err error) {
	req, err := newRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return "", err
	}

	resp, err := doRequest(ctx, client, limiter, logger, req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return "", err
		}

		tmp, err := os.CreateTemp(filepath.Dir(filename), ".dl-*")
		if err != nil {
			return "", err
		}
		defer func() {
			if cerr := tmp.Close(); cerr != nil && err == nil {
				err = cerr
			}
			if err != nil {
				os.Remove(tmp.Name())
			}
		}()

		h := blake3.New(32, nil)
		if _, err = io.Copy(tmp, io.TeeReader(io.LimitReader(resp.Body, maxDownloadBytes), h)); err != nil {
			return "", err
		}

		var digest [32]byte
		h.Sum(digest[:0])

		hash = fmt.Sprintf("%x", digest)

		if dup, orig := cache.check(digest, filename); dup {
			logger.Info(
				"skipping duplicate content",
				"file", filename,
				"original", orig,
			)
			err = os.Remove(tmp.Name())
			return hash, err
		}

		if err := os.Rename(tmp.Name(), filename); err != nil {
			return hash, err
		}

		logger.Info("downloaded",
			"file", filename,
			"hash", hash,
		)

		return hash, nil
	}

	return "", nil
}

func (c *pageCache) fetch(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, rawURL string) (*pageFetch, error) {
	c.mu.Lock()
	entry, ok := c.pages[rawURL]
	if !ok {
		entry = &pageFetch{}
		c.pages[rawURL] = entry
	}
	c.mu.Unlock()

	entry.once.Do(func() {
		req, err := newRequest(ctx, http.MethodGet, rawURL)
		if err != nil {
			entry.err = fmt.Errorf("create request: %w", err)
			return
		}

		resp, err := doRequest(ctx, client, limiter, logger, req)
		if err != nil {
			entry.err = fmt.Errorf("fetch %s: %w", rawURL, err)
			logger.Error("failed to fetch page", "url", rawURL, "error", err)
			return
		}
		defer resp.Body.Close()

		entry.statusCode = resp.StatusCode
		entry.contentType = resp.Header.Get("Content-Type")
		entry.finalURL = resp.Request.URL

		if resp.StatusCode != http.StatusOK {
			logger.Error("bad status for page", "url", rawURL, "status", resp.Status)
			return
		}

		if strings.HasPrefix(entry.contentType, "image/") {
			return
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
		if err != nil {
			entry.err = fmt.Errorf("read body: %w", err)
			return
		}
		entry.body = body
	})

	return entry, entry.err
}

func fetchGalleryItems(ctx context.Context, client *http.Client, apiURL string) ([]galleryItem, error) {
	req, err := newRequest(ctx, http.MethodGet, apiURL)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %s", resp.Status)
	}

	var items []galleryItem
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPageBytes)).Decode(&items); err != nil {
		return nil, fmt.Errorf("decode gallery json: %w", err)
	}

	return items, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func filenameFromURL(home, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	rel := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(u.Path, "/")))

	switch {
	case rel == ".":
		rel = "index.html"
	case strings.HasSuffix(u.Path, "/"):
		rel = filepath.Join(rel, "index.html")
	}

	root := filepath.Join(home, "Images", staging, u.Hostname())
	full := filepath.Join(root, rel)

	if relCheck, err := filepath.Rel(root, full); err != nil || strings.HasPrefix(relCheck, "..") {
		return "", fmt.Errorf("refusing to write outside %s: %s", root, full)
	}

	return full, nil
}

func findParentAnchor(s *goquery.Selection) (*goquery.Selection, bool) {
	for p := s.Parent(); p.Length() > 0; p = p.Parent() {
		if goquery.NodeName(p) == "a" {
			return p, true
		}
	}
	return nil, false
}

func hashFile(filename string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := blake3.New(32, nil)

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	var digest [32]byte
	h.Sum(digest[:0])

	return fmt.Sprintf("%x", digest), nil
}

func imageURL(base *url.URL, img *goquery.Selection) (string, bool) {
	var best string

	for _, attr := range []string{"data-srcset", "srcset"} {
		if val, ok := img.Attr(attr); ok {
			if u := parseSrcset(val); u != "" {
				if resolved, ok := resolveURL(base, u); ok {
					best = resolved
					break
				}
			}
		}
	}

	if best == "" {
		for _, attr := range []string{
			"data-original",
			"data-lazy-src",
			"data-src",
			"src",
			"href",
			"srcset",
			"poster",
			"action",
		} {
			if val, ok := img.Attr(attr); ok && val != "" {
				if resolved, ok := resolveURL(base, val); ok {
					best = resolved
					break
				}
			}
		}
	}

	return best, best != ""
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil &&
		(u.Scheme == "http" || u.Scheme == "https")
}

func main() {
	staging = time.Now().Format("20060102-150405")
	var (
		useClipboard = flag.Bool("clipboard", false, "read input from the system clipboard")
		site         = flag.String("site", "", "site to scrape")
		inputFile    = flag.String("input", "", "input file containing URLs")
		api          = flag.String("api", "", "gallery API URL that returns json")
	)

	logFile, err := os.OpenFile("log.json", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		panic(err)
	}
	defer logFile.Close()

	mw := io.MultiWriter(os.Stdout, logFile)

	logger = slog.New(slog.NewJSONHandler(mw, &slog.HandlerOptions{
		AddSource: true,
	}))

	flag.Usage = usage

	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	arg := flag.Arg(0)

	var sites []string

	switch {
	case *inputFile != "":
		var err error
		sites, err = readLines(*inputFile)
		if err != nil {
			logger.Error("reading", "inputFile", *inputFile, "err", err)
			os.Exit(1)
		}

	case *api != "":
		if err := runAPI(ctx, logger, *api); err != nil {
			logger.Error(err.Error())
			os.Exit(1)
		}

		os.Exit(0)

	case *site != "":
		sites = []string{*site}

	case *useClipboard:
		lines, err := readClipboard()
		if err != nil {
			logger.Error("reading clipboard", "err", err)
			os.Exit(1)
		}
		sites = lines

	case arg == "":
		data, err := os.ReadFile("/Users/boomer/dev/go/site-scraper/links.json")
		if err != nil {
			logger.Error("error", "err", err)
			os.Exit(1)
		}

		var groups map[string][]string

		if err := json.Unmarshal(data, &groups); err != nil {
			logger.Error("error", "err", err)
			os.Exit(1)
		}

		for _, v := range groups {
			sites = append(sites, v...)
		}

	default:
		flag.Usage()
	}

	if err := runCrawl(ctx, logger, sites); err != nil {
		logger.Error(err.Error())
		os.Exit(1)
	}
}

func manifestFilename(home string) string {
	return filepath.Join(
		home,
		"Images",
		staging,
		"image-manifest.json",
	)
}

func newPageCache() *pageCache {
	return &pageCache{pages: make(map[string]*pageFetch)}
}

func newRequest(ctx context.Context, method, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", userAgent)

	return req, nil
}

func parseSrcset(srcset string) string {
	var (
		bestURL     string
		bestW       int
		bestX       float64
		fallbackURL string
	)

	for candidate := range strings.SplitSeq(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(candidate))
		if len(fields) == 0 {
			continue
		}

		rawURL := fields[0]

		if len(fields) == 1 {
			if fallbackURL == "" {
				fallbackURL = rawURL
			}
			continue
		}

		d := fields[1]

		if wStr, ok := strings.CutSuffix(d, "w"); ok {
			w, err := strconv.Atoi(wStr)
			if err == nil && w > bestW {
				bestW = w
				bestURL = rawURL
			}
			continue
		}

		if xStr, ok := strings.CutSuffix(d, "x"); ok {
			x, err := strconv.ParseFloat(xStr, 64)
			if err == nil && x > bestX {
				bestX = x
				bestURL = rawURL
			}
		}
	}

	if bestURL == "" {
		bestURL = fallbackURL
	}

	return bestURL
}

// previewFilename builds a traversal-safe path for a gallery item's preview image.
// GID/TID are API-controlled integers, and the extension is allowlisted, so the
// result never depends on attacker-influenced path segments.
func previewFilename(home string, item galleryItem, rawURL string) string {
	ext := ".jpg"
	if u, err := url.Parse(rawURL); err == nil {
		switch e := strings.ToLower(filepath.Ext(u.Path)); e {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif":
			ext = e
		}
	}

	return filepath.Join(home, "Images", staging, "api-previews", fmt.Sprintf("%d_%d%s", item.GID, item.TID, ext))
}

func readClipboard() ([]string, error) {
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return nil, fmt.Errorf("clipboard read failed: %w", err)
	}

	var lines []string

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

func readLines(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string

	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

func resolveURL(base *url.URL, ref string) (string, bool) {
	u, err := base.Parse(ref)
	if err != nil {
		return "", false
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return "", false
	}

	return u.String(), true
}

// runAPI fetches gallery metadata from a JSON API, downloads each item's preview
// thumbnail, and writes a manifest pairing previews with their gallery site URL —
// letting a human review previews offline before choosing which GURLs to scrape
// with -site. This is a first pass meant to be reiterated on.
func runAPI(ctx context.Context, logger *slog.Logger, apiURL string) error {
	client := createClient()
	limiter := rate.NewLimiter(rate.Every(500*time.Millisecond), 1)

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	items, err := fetchGalleryItems(ctx, client, apiURL)
	if err != nil {
		return fmt.Errorf("fetch gallery items: %w", err)
	}
	logger.Info("fetched gallery items", "count", len(items))

	cache := &contentCache{seen: make(map[[32]byte]string)}
	manifest := make([]apiManifestEntry, len(items))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, item := range items {
		entry := apiManifestEntry{
			GID:     item.GID,
			TID:     item.TID,
			Title:   item.Title,
			Desc:    item.Desc,
			SiteURL: item.GURL,
		}

		previewURL := item.TURL460
		if !isHTTPURL(previewURL) {
			previewURL = item.TURL
		}

		if !isHTTPURL(previewURL) {
			manifest[i] = entry
			continue
		}

		filename := previewFilename(home, item, previewURL)

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			entry.PreviewErr = ctx.Err().Error()
			manifest[i] = entry
			continue
		}

		wg.Go(func() {
			defer func() { <-sem }()

			if !fileExists(filename) {
				if hash, err := downloadFile(ctx, client, limiter, logger, cache, previewURL, filename); err != nil {
					logger.Error("preview download failed", "url", previewURL, "hash", hash, "error", err)
					entry.PreviewErr = err.Error()
					manifest[i] = entry
					return
				}
			}

			entry.PreviewPath = filename
			manifest[i] = entry
		})
	}

	wg.Wait()

	slices.SortFunc(manifest, func(a, b apiManifestEntry) int {
		if a.GID != b.GID {
			return a.GID - b.GID
		}
		return a.TID - b.TID
	})

	manifestFile, err := os.Create("api-manifest.json")
	if err != nil {
		return fmt.Errorf("create manifest: %w", err)
	}
	defer manifestFile.Close()

	enc := json.NewEncoder(manifestFile)
	enc.SetIndent("", "  ")
	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	logger.Info("wrote api manifest", "file", "api-manifest.json", "items", len(manifest))
	return nil
}

func runCrawl(ctx context.Context, logger *slog.Logger, sites []string) error {
	var downloads []imageDownload
	session := createClient()
	limiter := rate.NewLimiter(rate.Every(500*time.Millisecond), 1)
	cache := newPageCache()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	type result struct {
		urls *crawlResult
		err  error
	}
	results := make(chan result, len(sites))

	var wg sync.WaitGroup
	for _, site := range sites {
		wg.Go(func() {
			logger.Info("site", "site", site)
			urls, err := collectURLsFromHTML(ctx, session, limiter, logger, cache, site)
			results <- result{urls: urls, err: err}
		})
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	images := make(map[string]struct{})
	links := make(map[string]struct{})

	for r := range results {
		if r.err != nil {
			logger.Error("failed to collect URLs", "error", r.err)
			continue
		}

		for imageURL := range r.urls.Images {
			filename, err := filenameFromURL(home, imageURL)
			if err != nil {
				logger.Error(
					"failed to generate filename",
					"site", r.urls.Site,
					"url", imageURL,
					"error", err,
				)
				continue
			}

			downloads = append(downloads, imageDownload{
				Site:       r.urls.Site,
				SourceURL:  imageURL,
				Filename:   filename,
				Discovered: time.Now(),
			})
		}

		for u := range r.urls.Links {
			logger.Info("found link", "url", u)
			links[u] = struct{}{}
		}
	}

	logger.Info("found images", "count", len(images))
	logger.Info("found links", "count", len(links))

	contentDedup := &contentCache{seen: make(map[[32]byte]string)}
	sem := make(chan struct{}, workers)
	var dlWg sync.WaitGroup
	var failures atomic.Int64

loop:
	for i := range downloads {
		i := i

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}

		dlWg.Go(func() {
			defer func() { <-sem }()

			d := &downloads[i]

			if fileExists(d.Filename) {
				hash, err := hashFile(d.Filename)
				if err != nil {
					d.Status = "failed"
					d.Error = err.Error()
					return
				}

				d.Hash = hash
				d.Status = "existing"

				logger.Info(
					"skipping existing file",
					"site", d.Site,
					"file", d.Filename,
					"hash", hash,
				)

				return
			}

			hash, err := downloadFile(
				ctx,
				session,
				limiter,
				logger,
				contentDedup,
				d.SourceURL,
				d.Filename,
			)

			if err != nil {
				d.Status = "failed"
				d.Error = err.Error()

				logger.Error(
					"download failed",
					"site", d.Site,
					"url", d.SourceURL,
					"error", err,
				)

				failures.Add(1)
				return
			}

			d.Hash = hash
			d.Status = "downloaded"
		})
	}

	dlWg.Wait()

	manifest := make([]imageManifestEntry, 0, len(downloads))

	for _, d := range downloads {
		manifest = append(manifest, imageManifestEntry{
			Site:       d.Site,
			SourceURL:  d.SourceURL,
			File:       d.Filename,
			Hash:       d.Hash,
			Status:     d.Status,
			Error:      d.Error,
			Discovered: d.Discovered,
		})
	}

	manifestFile, err := os.Create("image-manifest.json")
	if err != nil {
		return err
	}
	defer manifestFile.Close()

	enc := json.NewEncoder(manifestFile)
	enc.SetIndent("", "  ")

	if err := enc.Encode(manifest); err != nil {
		return fmt.Errorf("write image manifest: %w", err)
	}

	logger.Info(
		"wrote image manifest",
		"file", "image-manifest.json",
		"images", len(manifest),
	)

	if err := writeSet("links.txt", links); err != nil {
		return err
	}

	if n := failures.Load(); n > 0 {
		return fmt.Errorf("%d download(s) failed", n)
	}

	return nil
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

func writeSet(filename string, set map[string]struct{}) error {
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	w := bufio.NewWriter(f)

	list := make([]string, 0, len(set))
	for s := range set {
		list = append(list, s)
	}

	slices.Sort(list)

	for _, s := range list {
		fmt.Fprintln(w, s)
	}

	return w.Flush()
}
