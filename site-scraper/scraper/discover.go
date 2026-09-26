package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"

	"main/internal/httpx"
)

// discoverImageURLs fetches pageURL and returns every image URL referenced
// by <img src>/<img data-src>, plus any <a href> link whose target is
// itself image content, resolved to absolute URLs against pageURL. It
// returns before any downloading happens, so callers gather every page's
// images before starting the download phase.
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
		if abs == "" || seen[abs] {
			return
		}
		seen[abs] = true
		urls = append(urls, abs)
	}

	doc.Find("img[src]").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("src"); ok {
			addCandidate(src)
		}
	})
	doc.Find("img[data-src]").Each(func(_ int, s *goquery.Selection) {
		if src, ok := s.Attr("data-src"); ok {
			addCandidate(src)
		}
	})

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
