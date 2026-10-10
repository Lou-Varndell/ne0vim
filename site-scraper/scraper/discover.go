package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"

	"main/internal/httpx"
)

// discoverImageURLs fetches pageURL and returns every image URL referenced
// by <img src>/<img data-src>, every responsive candidate in <img srcset>,
// <img data-srcset>, and <picture><source srcset>, plus any <a href> link
// whose target is itself image content, resolved to absolute URLs against
// pageURL. It returns before any downloading happens, so callers gather
// every page's images before starting the download phase.
func discoverImageURLs(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, pageURL string) ([]string, error) {
	base, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse page URL: %w", err)
	}

	req, err := httpx.NewRequest(ctx, http.MethodGet, pageURL)
	if err != nil {
		return nil, err
	}

	resp, err := httpx.Do(ctx, client, limiter, logger, req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	seen := make(map[string]bool)
	var urls []string

	addCandidate := func(raw string) {
		abs := resolve(base, raw)
		if abs == "" || seen[abs] || isSVG(abs) {
			return
		}
		seen[abs] = true
		urls = append(urls, abs)
	}

	addFromAttr := func(sel, attr string) {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if v, ok := s.Attr(attr); ok {
				addCandidate(v)
			}
		})
	}
	addFromSrcset := func(sel, attr string) {
		doc.Find(sel).Each(func(_ int, s *goquery.Selection) {
			if v, ok := s.Attr(attr); ok {
				for _, u := range parseSrcset(v) {
					addCandidate(u)
				}
			}
		})
	}

	addFromAttr("img[src]", "src")
	addFromAttr("img[data-src]", "data-src")
	addFromSrcset("img[srcset]", "srcset")
	addFromSrcset("img[data-srcset]", "data-srcset")
	addFromSrcset("picture source[srcset]", "srcset")

	var linkTargets []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			if abs := resolve(base, href); abs != "" {
				linkTargets = append(linkTargets, abs)
			}
		}
	})

	for _, target := range linkTargets {
		isImage, err := classify(ctx, client, limiter, logger, target)
		if err != nil || !isImage {
			continue
		}
		addCandidate(target)
	}

	return urls, nil
}

// parseSrcset extracts each candidate URL from a srcset attribute value
// (e.g. "a.jpg 480w, b.jpg 800w, c.jpg 2x"), discarding the size/density
// descriptors. Every candidate is downloaded; dedup collapses any that
// turn out to be byte-identical.
func parseSrcset(raw string) []string {
	var urls []string
	for candidate := range strings.SplitSeq(raw, ",") {
		if fields := strings.Fields(candidate); len(fields) > 0 {
			urls = append(urls, fields[0])
		}
	}
	return urls
}

// isSVG reports whether an absolute URL's path ends in .svg. SVGs are
// vector markup, not raster image data, so they're excluded from
// collection.
func isSVG(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(filepath.Ext(u.Path), ".svg")
}

// resolve resolves raw against base and returns its absolute form, or ""
// if raw is blank or unparseable.
func resolve(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	resolved, err := base.Parse(raw)
	if err != nil {
		return ""
	}

	return resolved.String()
}
