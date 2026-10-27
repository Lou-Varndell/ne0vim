// Package warmthumbs implements the warm-thumbnails command: eagerly
// generating every thumbnail invlib.WarmAll finds missing from the shared
// database, instead of leaving each one to be generated lazily the first
// time some viewer (image-browser's own, inventory-manager's, or
// media-site's /thumbs route) happens to display that image.
package warmthumbs

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	invlib "inv-lib"
)

// ThumbnailSize must match the size every other thumbnail consumer
// generates and reads at (see image-browser's viewer.ThumbnailSize,
// inventory-manager's browser.go, and media-site's thumbSize) — warming
// at any other size would populate a cache entry none of them ever look
// at.
const ThumbnailSize = 150

// Run parses args and warms the shared thumbnail cache for every image
// file row in the inventory database, printing per-file progress (unless
// -quiet) and a final summary.
//
// With no flags, only thumbnails missing from the cache are generated.
// -refresh regenerates every thumbnail unconditionally. -ThumbnailSize
// generates (and reads back) thumbnails at a size other than the shared
// default, and — because that size has its own, separate cache directory
// (see ThumbCache.Dir) — passing it always implies -refresh too, so a
// non-default size is never left half-populated by whatever happened to
// already be sitting in that directory.
func Run(args []string) error {
	fs := flag.NewFlagSet("warm-thumbnails", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	dbPath := fs.String("db", "", "path to the inventory database (default: the shared ~/.config/inventory-manager/db/inv.db)")
	concurrency := fs.Int("concurrency", 0, "number of thumbnails to generate concurrently (default: GOMAXPROCS)")
	quiet := fs.Bool("quiet", false, "suppress per-file progress output")
	refresh := fs.Bool("refresh", false, "regenerate every thumbnail, even ones already cached")
	thumbnailSize := fs.Int("ThumbnailSize", ThumbnailSize, "thumbnail size in pixels on the longest edge; setting this always implies -refresh")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser warm-thumbnails [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("warm-thumbnails: unexpected positional arguments")
	}
	if *thumbnailSize <= 0 {
		return errors.New("warm-thumbnails: -ThumbnailSize must be a positive integer")
	}

	// -ThumbnailSize implies -refresh regardless of its value (even one
	// equal to the default): passing it at all means the caller wants
	// this run's cache entries for that size to be freshly, verifiably
	// generated now, not reused from whatever an earlier run may have
	// left behind.
	force := *refresh
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "ThumbnailSize" {
			force = true
		}
	})

	path := *dbPath
	if path == "" {
		var err error
		path, err = invlib.DefaultPath()
		if err != nil {
			return fmt.Errorf("warm-thumbnails: resolve inv.db path: %w", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	db, err := invlib.Open(ctx, path)
	if err != nil {
		return fmt.Errorf("warm-thumbnails: open %s: %w", path, err)
	}
	defer db.Close()

	cache, err := invlib.NewThumbCache(uint(*thumbnailSize))
	if err != nil {
		return fmt.Errorf("warm-thumbnails: create thumbnail cache: %w", err)
	}

	var progress func(done, total int, path string, err error)
	if !*quiet {
		progress = func(done, total int, path string, err error) {
			if err != nil {
				fmt.Fprintf(os.Stderr, "[%d/%d] FAILED %s: %v\n", done, total, path, err)
				return
			}
			fmt.Printf("\r[%d/%d] %s\033[K", done, total, path)
		}
	}

	start := time.Now()
	result, warmErr := invlib.WarmAll(ctx, db, cache, *concurrency, force, progress)
	if !*quiet {
		fmt.Println()
	}

	fmt.Printf("warmed %d image(s) in %s: %d generated, %d already cached, %d failed\n",
		result.Total, time.Since(start).Round(time.Millisecond), result.Generated, result.Skipped, result.Failed)

	if warmErr != nil {
		return fmt.Errorf("warm-thumbnails: %w", warmErr)
	}
	return nil
}
