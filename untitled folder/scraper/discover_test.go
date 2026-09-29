package scraper

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"golang.org/x/time/rate"
)

func unlimited() *rate.Limiter {
	return rate.NewLimiter(rate.Inf, 1)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDiscoverImageURLsResponsive(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/page.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body>
			<img src="/img/fallback.jpg" srcset="/img/small.jpg 480w, /img/large.jpg 1200w">
			<img data-srcset="/img/lazy-small.jpg 480w, /img/lazy-large.jpg 1200w">
			<picture>
				<source srcset="/img/pic.webp" type="image/webp">
				<source srcset="/img/pic-small.jpg 480w, /img/pic-large.jpg 1200w">
				<img src="/img/pic-fallback.jpg">
			</picture>
		</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	got, err := discoverImageURLs(context.Background(), &http.Client{}, unlimited(), discardLogger(), srv.URL+"/page.html")
	if err != nil {
		t.Fatalf("discoverImageURLs: %v", err)
	}

	want := []string{
		srv.URL + "/img/fallback.jpg",
		srv.URL + "/img/small.jpg",
		srv.URL + "/img/large.jpg",
		srv.URL + "/img/lazy-small.jpg",
		srv.URL + "/img/lazy-large.jpg",
		srv.URL + "/img/pic.webp",
		srv.URL + "/img/pic-small.jpg",
		srv.URL + "/img/pic-large.jpg",
		srv.URL + "/img/pic-fallback.jpg",
	}

	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseSrcset(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", nil},
		{"single, no descriptor", "a.jpg", []string{"a.jpg"}},
		{"width descriptors", "a.jpg 480w, b.jpg 800w", []string{"a.jpg", "b.jpg"}},
		{"density descriptor", "a.jpg 1x, a-2x.jpg 2x", []string{"a.jpg", "a-2x.jpg"}},
		{"ragged whitespace", " a.jpg  480w ,\tb.jpg 800w", []string{"a.jpg", "b.jpg"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseSrcset(tc.raw)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseSrcset(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
