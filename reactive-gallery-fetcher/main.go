// Command capturelayout captures the dynamically loaded image layout from a
// web page into a JSON file.
//
// It scrolls the page step by step, injecting a small JavaScript snapshot
// after each scroll to read image positions and attributes from the DOM,
// deduplicates images by their resolved absolute URL across scroll steps,
// and writes the merged result to disk.
//
// This is a chromedp-based port of the sibling playwright-go version: it
// drives Chrome directly over the DevTools Protocol instead of through
// Playwright's Node.js driver subprocess, so the only runtime dependency is
// a Chrome/Chromium binary on PATH (or discoverable via chromedp's default
// lookup) — no separate "install" step is needed.
//
// jq -r '.images[].gallery_url' layout.json
//
// Run:
//
//	go run .
//	go run . --url "https://www.url.com/" --out layout.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"
)

// captureJS is injected into the page after every scroll step. It walks the
// image tiles currently in the DOM and reports their layout, the best
// candidate image URL, and the raw attributes the page exposed for each
// <img>. The shape of the object it returns is mirrored by snapshot below.
const captureJS = `
() => {
  const grid = document.querySelector("#tiles") || document;
  const cards = Array.from(grid.querySelectorAll(".thumbwook"));
  const found = [];

  for (let cardIndex = 0; cardIndex < cards.length; cardIndex++) {
    const card = cards[cardIndex];
    if (card.matches(".r2-frame, .c-model") ||
        card.querySelector(".r2-frame, .c-model")) continue;

    const link = card.querySelector("a[href]");
    const images = Array.from(card.querySelectorAll("img"));
    for (let imageIndex = 0; imageIndex < images.length; imageIndex++) {
      const img = images[imageIndex];
      const rect = img.getBoundingClientRect();
      const attrs = {};
      for (const name of [
        "src", "data-src", "srcset", "data-srcset", "data-original",
        "data-lazy-src", "alt", "title", "width", "height"
      ]) {
        const value = img.getAttribute(name);
        if (value) attrs[name] = value;
      }

      // Prefer the real lazy-load URL, but preserve all alternatives too.
      const candidates = [
        attrs["data-src"], attrs["data-lazy-src"], attrs["data-original"],
        attrs["src"]
      ].filter(Boolean);
      const imageUrl = candidates.find(u => !u.startsWith("data:")) || null;

      found.push({
        card_index: cardIndex,
        image_index: imageIndex,
        gallery_url: link ? link.href : null,
        image_url: imageUrl,
        image_attributes: attrs,
        alt: img.getAttribute("alt") || null,
        title: img.getAttribute("title") || null,
        layout: {
          x: Math.round(rect.x + window.scrollX),
          y: Math.round(rect.y + window.scrollY),
          width: Math.round(rect.width),
          height: Math.round(rect.height),
          viewport_x: Math.round(rect.x),
          viewport_y: Math.round(rect.y),
          visible: rect.width > 0 && rect.height > 0 &&
                   rect.bottom > 0 && rect.top < window.innerHeight
        }
      });
    }
  }
  return {
    page_url: location.href,
    title: document.title,
    scroll_y: Math.round(window.scrollY),
    document_height: document.documentElement.scrollHeight,
    viewport: {width: window.innerWidth, height: window.innerHeight},
    images: found
  };
}
`

// scrollToBottomJS is injected to advance the scroll position before the
// next capture.
const scrollToBottomJS = `window.scrollTo(0, document.documentElement.scrollHeight)`

// config holds the parsed CLI flags.
type config struct {
	url          string
	out          string
	maxScrolls   int
	pauseSeconds float64
	stableRounds int
	headed       bool
}

// layout is the position/visibility data captured for a single image, in
// document coordinates.
type layout struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	ViewportX int  `json:"viewport_x"`
	ViewportY int  `json:"viewport_y"`
	Visible   bool `json:"visible"`
}

// imageRecord is one captured <img>, keyed for deduplication by its resolved
// absolute image URL once merged into a collector.
type imageRecord struct {
	CardIndex        int               `json:"card_index"`
	ImageIndex       int               `json:"image_index"`
	GalleryURL       *string           `json:"gallery_url"`
	ImageURL         *string           `json:"image_url"`
	ImageAttributes  map[string]string `json:"image_attributes"`
	Alt              *string           `json:"alt"`
	Title            *string           `json:"title"`
	Layout           layout            `json:"layout"`
	FirstSeenStep    int               `json:"first_seen_step"`
	FirstSeenScrollY int               `json:"first_seen_scroll_y"`
}

// snapshot is the full object returned by captureJS for a single capture
// call.
type snapshot struct {
	PageURL        string `json:"page_url"`
	Title          string `json:"title"`
	ScrollY        int    `json:"scroll_y"`
	DocumentHeight int    `json:"document_height"`
	Viewport       struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"viewport"`
	Images []imageRecord `json:"images"`
}

// result is the top-level JSON document written to --out.
type result struct {
	CapturedAt string         `json:"captured_at"`
	SourceURL  string         `json:"source_url"`
	Count      int            `json:"count"`
	Images     []*imageRecord `json:"images"`
	Notes      []string       `json:"notes"`
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("capturelayout: %v", err)
	}
}

func parseFlags() *config {
	cfg := &config{}
	flag.StringVar(&cfg.url, "url", "https://www.url.com/", "page URL to capture")
	flag.StringVar(&cfg.out, "out", "layout.json", "output JSON file path")
	flag.IntVar(&cfg.maxScrolls, "max-scrolls", 300, "maximum number of scroll steps")
	flag.Float64Var(&cfg.pauseSeconds, "pause", 1.2, "seconds to wait after each scroll")
	flag.IntVar(&cfg.stableRounds, "stable-rounds", 5, "consecutive unchanged-height rounds before stopping")
	flag.BoolVar(&cfg.headed, "headed", false, "show Chromium instead of running headless")
	flag.Parse()
	return cfg
}

func run(cfg *config) error {
	pauseDuration := time.Duration(cfg.pauseSeconds * float64(time.Second))

	// chromedp talks to the browser over the DevTools Protocol directly, so
	// there's no equivalent of playwright.Run()/pw.Stop() — the allocator
	// is the thing that owns the browser process, and the context built on
	// top of it owns the page/tab.
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !cfg.headed),
	)
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer allocCancel()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()

	navCtx, navCancel := context.WithTimeout(ctx, 90*time.Second)
	defer navCancel()
	if err := chromedp.Do(navCtx,
		chromedp.EmulateViewport(1440, 1000),
		chromedp.Navigate(cfg.url),
	); err != nil {
		return fmt.Errorf("navigating to %s: %w", cfg.url, err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Second)
	defer waitCancel()
	if err := chromedp.Do(waitCtx, chromedp.WaitVisible("#tiles")); err != nil {
		log.Println("Warning: #tiles was not found; capturing from the whole document.")
	}

	// This loop is intentionally sequential: each capture reads the DOM
	// state produced by the *previous* scroll, and the next scroll depends
	// on the document height observed by the current capture. There is no
	// independent work here to fan out across goroutines — the browser
	// only has one page in one state at a time, so concurrency would only
	// add synchronization risk for zero throughput gain.
	collector := newImageCollector()
	lastHeight := -1
	stable := 0

	for step := 0; step < cfg.maxScrolls; step++ {
		snap, err := captureSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("capturing snapshot at step %d: %w", step, err)
		}

		if err := collector.merge(snap, step); err != nil {
			return fmt.Errorf("merging snapshot at step %d: %w", step, err)
		}

		height := snap.DocumentHeight
		log.Printf("scroll %d/%d: %d unique image URLs, document height %dpx",
			step+1, cfg.maxScrolls, collector.count(), height)

		if height == lastHeight {
			stable++
		} else {
			stable = 0
		}
		lastHeight = height

		if stable >= cfg.stableRounds {
			// One last snapshot after the page appears to have stopped growing.
			if err := chromedp.Do(ctx,
				chromedp.Evaluate[chromedp.Void](scrollToBottomJS),
				chromedp.Sleep(pauseDuration),
			); err != nil {
				return fmt.Errorf("scrolling to bottom: %w", err)
			}

			final, err := captureSnapshot(ctx)
			if err != nil {
				return fmt.Errorf("capturing final snapshot: %w", err)
			}
			if err := collector.mergeNewOnly(final, step+1); err != nil {
				return fmt.Errorf("merging final snapshot: %w", err)
			}
			break
		}

		if err := chromedp.Do(ctx,
			chromedp.Evaluate[chromedp.Void](scrollToBottomJS),
			chromedp.Sleep(pauseDuration),
		); err != nil {
			return fmt.Errorf("scrolling down: %w", err)
		}
	}

	res := result{
		CapturedAt: time.Now().UTC().Format(time.RFC3339),
		SourceURL:  cfg.url,
		Count:      collector.count(),
		Images:     collector.orderedImages(),
		Notes: []string{
			"Positions are document coordinates captured during scrolling.",
			"Lazy-loaded image URLs may be thumbnails rather than full-size gallery images.",
			"Review the manifest and site terms before downloading or reusing content.",
		},
	}

	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling result: %w", err)
	}

	if err := os.WriteFile(cfg.out, data, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", cfg.out, err)
	}

	absPath, err := filepath.Abs(cfg.out)
	if err != nil {
		absPath = cfg.out
	}
	fmt.Printf("\nSaved %d unique images to %s\n", collector.count(), absPath)

	return nil
}

// captureSnapshot runs captureJS in the page and decodes the result into a
// typed snapshot. captureJS is a function literal ("() => {...}"), so it is
// wrapped in an immediately-invoked expression — unlike Playwright's
// page.Evaluate, chromedp.Evaluate does not auto-detect and call bare
// function literals.
//
// Instantiating Evaluate with T = []byte makes chromedp hand back the raw
// JSON-encoded result from the DevTools Protocol response, which is
// unmarshaled directly into snapshot. That avoids the marshal/unmarshal
// round trip the Playwright version needs to get from an untyped
// interface{} to a typed struct.
func captureSnapshot(ctx context.Context) (snapshot, error) {
	expr := fmt.Sprintf("(%s)()", captureJS)
	raw, err := chromedp.Run(ctx, chromedp.Evaluate[[]byte](expr))
	if err != nil {
		return snapshot{}, fmt.Errorf("evaluating capture script: %w", err)
	}

	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snapshot{}, fmt.Errorf("unmarshaling snapshot: %w", err)
	}
	return snap, nil
}

// imageCollector deduplicates imageRecords by their resolved absolute image
// URL while preserving first-seen order, mirroring the Python script's use
// of a dict keyed by URL (Go map iteration order is random, so insertion
// order is tracked separately in order).
//
// Not safe for concurrent use — it is only ever touched from the single
// sequential scroll loop in run.
type imageCollector struct {
	order   []string
	records map[string]*imageRecord
}

func newImageCollector() *imageCollector {
	return &imageCollector{records: make(map[string]*imageRecord)}
}

func (c *imageCollector) count() int {
	return len(c.order)
}

// merge folds a scroll-step snapshot into the collector. New URLs are
// inserted with their first-seen position; URLs seen before have their
// attributes refreshed and gain a gallery URL if they lacked one, but keep
// their original first-seen step/scroll position and layout.
func (c *imageCollector) merge(snap snapshot, step int) error {
	for i := range snap.Images {
		item := snap.Images[i]
		if item.ImageURL == nil || *item.ImageURL == "" {
			continue
		}

		absolute, err := resolveURL(snap.PageURL, *item.ImageURL)
		if err != nil {
			return fmt.Errorf("resolving image url %q: %w", *item.ImageURL, err)
		}
		item.ImageURL = &absolute
		item.FirstSeenStep = step
		item.FirstSeenScrollY = snap.ScrollY

		if existing, ok := c.records[absolute]; ok {
			for k, v := range item.ImageAttributes {
				existing.ImageAttributes[k] = v
			}
			if (existing.GalleryURL == nil || *existing.GalleryURL == "") &&
				item.GalleryURL != nil && *item.GalleryURL != "" {
				existing.GalleryURL = item.GalleryURL
			}
			continue
		}

		rec := item
		c.records[absolute] = &rec
		c.order = append(c.order, absolute)
	}
	return nil
}

// mergeNewOnly inserts only URLs not already present, without touching
// existing records' attributes. It mirrors the Python script's final pass,
// which uses dict.setdefault instead of the merge-in-place behavior of the
// main loop.
func (c *imageCollector) mergeNewOnly(snap snapshot, step int) error {
	for i := range snap.Images {
		item := snap.Images[i]
		if item.ImageURL == nil || *item.ImageURL == "" {
			continue
		}

		absolute, err := resolveURL(snap.PageURL, *item.ImageURL)
		if err != nil {
			return fmt.Errorf("resolving image url %q: %w", *item.ImageURL, err)
		}
		if _, exists := c.records[absolute]; exists {
			continue
		}

		item.ImageURL = &absolute
		item.FirstSeenStep = step
		item.FirstSeenScrollY = snap.ScrollY

		rec := item
		c.records[absolute] = &rec
		c.order = append(c.order, absolute)
	}
	return nil
}

// orderedImages returns the collected records in first-seen order.
func (c *imageCollector) orderedImages() []*imageRecord {
	images := make([]*imageRecord, len(c.order))
	for i, key := range c.order {
		images[i] = c.records[key]
	}
	return images
}

// resolveURL resolves ref against base the way Python's urllib.parse.urljoin
// does.
func resolveURL(base, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing base url: %w", err)
	}
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("parsing reference url: %w", err)
	}
	return baseURL.ResolveReference(refURL).String(), nil
}
