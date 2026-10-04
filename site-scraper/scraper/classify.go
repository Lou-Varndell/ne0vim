package scraper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"golang.org/x/time/rate"

	"main/internal/httpx"
)

// classify reports whether rawURL points directly at image content (true)
// or is an HTML page to scrape (false). Anything else is an error.
func classify(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, rawURL string) (isImage bool, err error) {
	ct, err := contentType(ctx, client, limiter, logger, rawURL)
	if err != nil {
		return false, err
	}

	switch {
	case strings.HasPrefix(ct, "image/"):
		return true, nil
	case strings.HasPrefix(ct, "text/html"):
		return false, nil
	default:
		return false, fmt.Errorf("unsupported content-type %q", ct)
	}
}

// contentType fetches rawURL's Content-Type via HEAD, falling back to GET
// if the server doesn't support HEAD (405/501).
func contentType(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, rawURL string) (string, error) {
	resp, err := headOrGet(ctx, client, limiter, logger, rawURL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}

	return resp.Header.Get("Content-Type"), nil
}

func headOrGet(ctx context.Context, client *http.Client, limiter *rate.Limiter, logger *slog.Logger, rawURL string) (*http.Response, error) {
	req, err := httpx.NewRequest(ctx, http.MethodHead, rawURL)
	if err != nil {
		return nil, err
	}

	resp, err := httpx.Do(ctx, client, limiter, logger, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusNotImplemented {
		return resp, nil
	}
	resp.Body.Close()

	req, err = httpx.NewRequest(ctx, http.MethodGet, rawURL)
	if err != nil {
		return nil, err
	}

	return httpx.Do(ctx, client, limiter, logger, req)
}
