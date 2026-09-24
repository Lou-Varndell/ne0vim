package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

var httpClient = &http.Client{}

func FetchImages(ctx context.Context, pageURL string) ([]string, error) {
	parsedURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("User-Agent", "page-fetcher/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch page: HTTP %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parse HTML: %w", err)
	}

	var images []string
	seen := make(map[string]struct{})

	doc.Find("img").Each(func(_ int, img *goquery.Selection) {
		imageURL := ""

		// If the image is inside an anchor, prefer the anchor URL.
		if anchor := img.ParentsFiltered("a").First(); anchor.Length() > 0 {
			if href, ok := anchor.Attr("href"); ok {
				imageURL = href
			}
		}

		// Otherwise use the image's src.
		if imageURL == "" {
			if src, ok := img.Attr("src"); ok {
				imageURL = src
			}
		}

		if imageURL == "" {
			return
		}

		resolved, err := resolveURL(parsedURL, imageURL)
		if err != nil {
			return
		}

		if _, exists := seen[resolved]; exists {
			return
		}

		seen[resolved] = struct{}{}
		images = append(images, resolved)
	})

	return images, nil
}

func resolveURL(base *url.URL, rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("empty URL")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}

	return base.ResolveReference(parsed).String(), nil
}
