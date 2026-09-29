package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/time/rate"

	"main/internal/httpx"
)

// work is one image to download, tracing back to the input URL it came
// from.
type work struct {
	site      string // input URL passed to Run (page or direct image)
	sourceURL string // resolved image URL (== site for direct-image inputs)
}

// downloadedFile describes one file that landed in tempDir. name is the
// logical filename derived from the URL/Content-Disposition — the value
// dedup groups by — which is distinct from path's basename once
// uniqueTempPath has disambiguated an on-disk collision.
type downloadedFile struct {
	work work
	name string
	path string
	size int64
}

// downloadToTemp fetches w.sourceURL and writes it to a collision-free
// path under tempDir, named after the URL (or Content-Disposition).
func downloadToTemp(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, tempDir string, w work) (downloadedFile, error) {
	req, err := httpx.NewRequest(ctx, http.MethodGet, w.sourceURL)
	if err != nil {
		return downloadedFile{}, err
	}

	resp, err := httpx.Do(ctx, client, limiter, logger, req)
	if err != nil {
		return downloadedFile{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return downloadedFile{}, fmt.Errorf("unexpected status %s", resp.Status)
	}

	name := filenameFromURL(w.sourceURL, resp.Header.Get("Content-Disposition"))
	path := uniqueTempPath(tempDir, name)

	f, err := os.Create(path)
	if err != nil {
		return downloadedFile{}, err
	}
	defer f.Close()

	n, err := io.Copy(f, resp.Body)
	if err != nil {
		os.Remove(path)
		return downloadedFile{}, fmt.Errorf("write %s: %w", path, err)
	}

	return downloadedFile{work: w, name: name, path: path, size: n}, nil
}

// filenameFromURL derives a filename from a Content-Disposition header if
// present, else from the URL path, falling back to a generic name if
// neither yields one.
func filenameFromURL(rawURL, contentDisposition string) string {
	if _, params, err := mime.ParseMediaType(contentDisposition); err == nil {
		if fn := params["filename"]; fn != "" {
			return filepath.Base(fn)
		}
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return "download"
	}

	base := filepath.Base(u.Path)
	if base == "" || base == "/" || base == "." {
		return "download"
	}
	return base
}

// uniqueTempPath returns a path under dir for name, disambiguating with a
// -2, -3, ... suffix if name is already taken. This is independent of (and
// unrelated to) the destDir collision handling in move.go: two different
// SourceURLs that happen to share a basename must not clobber each other
// while both are still candidates for dedup.
func uniqueTempPath(dir, name string) string {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	path := filepath.Join(dir, name)
	for i := 2; fileExists(path); i++ {
		path = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
	return path
}
