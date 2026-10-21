package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/time/rate"

	"main/internal/httpx"
)

const batchSize = 20

type PicResult struct {
	GURL string `json:"g_url"`
}

func firstPage(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, apiURL, query string) ([]PicResult, error) {
	endpoint := picsURL(0, batchSize, apiURL, query)

	req, err := httpx.NewRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := httpx.Do(ctx, client, limiter, logger, req)
	if err != nil {
		return nil, fmt.Errorf("request first page: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("parsing HTML: %w", err)
	}

	var results []PicResult
	doc.Find("a.rel-link").Each(func(i int, s *goquery.Selection) {
		if href, ok := s.Attr("href"); ok {
			results = append(results, PicResult{GURL: href})
		}
	})

	return results, nil
}

func fetchPics(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, offset int, apiURL, query string) ([]PicResult, error) {
	endpoint := picsURL(offset, batchSize, apiURL, query)

	req, err := httpx.NewRequest(ctx, http.MethodGet, endpoint)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := httpx.Do(ctx, client, limiter, logger, req)
	if err != nil {
		return nil, fmt.Errorf("fetching pics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %s", resp.Status)
	}

	var results []PicResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding JSON: %w", err)
	}

	return results, nil
}

func picsURL(offset, limit int, apiURL, query string) string {
	q := url.Values{}
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	q.Set("lang", "en")
	q.Set("q", query)

	return apiURL + "?" + q.Encode()
}

// fetchAllPics paginates apiURL in batchSize-sized batches, deduplicating
// results by GURL across every page.
func fetchAllPics(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, apiURL, query string) ([]PicResult, error) {
	seen := make(map[string]struct{})
	var deduped []PicResult

	firstBatch, err := firstPage(ctx, client, limiter, logger, apiURL, query)
	if err != nil {
		return nil, fmt.Errorf("fetching first page: %w", err)
	}

	for offset := 20; ; offset += batchSize {
		results, err := fetchPics(ctx, client, limiter, logger, offset, apiURL, query)
		if err != nil {
			return nil, err
		}

		if len(results) == 0 {
			break
		}

		results = append(results, firstBatch...)

		for _, pic := range results {
			if pic.GURL == "" {
				continue
			}

			if _, exists := seen[pic.GURL]; exists {
				continue
			}

			seen[pic.GURL] = struct{}{}
			deduped = append(deduped, pic)
		}

		if len(results) < batchSize {
			break
		}
	}

	return deduped, nil
}
