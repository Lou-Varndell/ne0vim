package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"page-fetcher/internal/scraper"
	"page-fetcher/web/templates/pages"
)

type Handler struct {
	log *slog.Logger
}

func New(log *slog.Logger) *Handler {
	return &Handler{log: log}
}

func (h *Handler) Home(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		h.fetch(w, r)
		return
	}

	component := pages.Home()
	if err := component.Render(r.Context(), w); err != nil {
		h.log.Error("render failed", "err", err)
	}
}

func (h *Handler) fetch(w http.ResponseWriter, r *http.Request) {
	text := r.FormValue("text")

	urls, err := parseURLs(text)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, pageURL := range urls {
		images, err := scraper.FetchImages(r.Context(), pageURL)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to fetch %s: %v", pageURL, err), http.StatusBadGateway)
			return
		}

		fmt.Fprintf(w, "<h2>%s</h2>", pageURL)
		fmt.Fprintf(w, "<p>Found %d images</p>", len(images))
		fmt.Fprintln(w, "<ul>")

		for _, imageURL := range images {
			fmt.Fprintf(w, "<li>%s</li>", imageURL)
		}

		fmt.Fprintln(w, "</ul>")
	}
}

func parseURLs(text string) ([]string, error) {
	lines := strings.Split(text, "\n")

	var urls []string

	for lineNumber, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		u, err := url.Parse(line)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("line %d is not a valid URL: %q", lineNumber+1, line)
		}

		if u.Scheme != "http" && u.Scheme != "https" {
			return nil, fmt.Errorf("line %d must use http or https: %q", lineNumber+1, line)
		}

		urls = append(urls, u.String())
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no URLs were provided")
	}

	return urls, nil
}
