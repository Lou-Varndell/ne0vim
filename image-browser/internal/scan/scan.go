// Package scan implements the recursive directory scan that records files
// — and, for recognized image types, their dimensions, format, and color
// model — into the inventory database.
package scan

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"

	"image-browser/internal/db"
	"image-browser/internal/images"
	"image-browser/internal/paths"
	"image-browser/internal/store"

	_ "golang.org/x/image/webp"
)

// Run executes the scan command: it walks root recursively and records
// every file whose name matches one of patterns (shell glob syntax, e.g.
// "*.jpg") in the shared inventory database (see db.DefaultPath). A matched
// file recognized as an image by package image (gif, jpeg, png, or webp,
// per the blank imports above) also gets a row in the images table — see
// decodeImage.
func Run(args []string, patterns []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "root directory to scan recursively (required)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser scan -root <dir>")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("scan: unexpected positional arguments")
	}
	if *root == "" {
		return errors.New("scan: -root is required")
	}
	if len(patterns) == 0 {
		return errors.New("scan: no patterns to match")
	}

	rootPath, err := paths.Abs(*root)
	if err != nil {
		return fmt.Errorf("scan: resolving root: %w", err)
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("scan: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scan: not a directory: %s", rootPath)
	}

	dbPath, err := db.DefaultPath()
	if err != nil {
		return fmt.Errorf("scan: resolve inv.db path: %w", err)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return fmt.Errorf("scan: open inv.db: %w", err)
	}
	defer database.Close()
	inv := store.New(database)

	ctx := context.Background()
	found, added, imaged := 0, 0, 0

	// A subdirectory that can't be read (permission denied, broken symlink,
	// etc.) is skipped rather than aborting the whole walk, matching
	// images.Find and count.inspectDirectory; an error reading root itself
	// is still returned, since that indicates the caller passed a bad
	// starting point rather than an incidental obstruction.
	err = filepath.WalkDir(rootPath, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == rootPath {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !matches(patterns, d.Name()) {
			return nil
		}
		found++

		fileInfo, err := d.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: stat %s: %v\n", path, err)
			return nil
		}

		hash, err := images.Hash(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: hash %s: %v\n", path, err)
			return nil
		}

		id, inserted, err := inv.AddFile(ctx, store.File{
			Path:       path,
			Filename:   d.Name(),
			Extension:  filepath.Ext(path),
			Filesize:   fileInfo.Size(),
			BLAKE3:     sql.NullString{String: hash, Valid: hash != ""},
			ModifiedAt: sql.NullTime{Time: fileInfo.ModTime(), Valid: true},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: add %s: %v\n", path, err)
			return nil
		}
		if inserted {
			added++
		}

		// A matched pattern (e.g. "*.jpg") doesn't guarantee the file is
		// actually decodable as an image — a renamed or corrupt file still
		// gets its files row above, it just won't get an images row.
		width, height, format, space, err := decodeImage(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: decode %s: %v\n", path, err)
			return nil
		}

		imageInserted, err := inv.AddImage(ctx, store.Image{
			FileID:     id,
			Width:      width,
			Height:     height,
			Format:     format,
			ColorSpace: space,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "scan: add image %s: %v\n", path, err)
			return nil
		}
		if imageInserted {
			imaged++
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("scan: walking %s: %w", rootPath, err)
	}

	fmt.Printf("Scanned %s\n", rootPath)
	fmt.Printf("Files matched: %d\n", found)
	fmt.Printf("Files added to database: %d\n", added)
	fmt.Printf("Images added to database: %d\n", imaged)
	return nil
}

// matches reports whether name matches any of patterns, using shell glob
// syntax (see filepath.Match) compared case-insensitively.
func matches(patterns []string, name string) bool {
	name = strings.ToLower(name)
	for _, pattern := range patterns {
		if ok, err := filepath.Match(strings.ToLower(pattern), name); err == nil && ok {
			return true
		}
	}
	return false
}

// decodeImage reads just enough of the file at path to determine its
// dimensions, format, and color model, without decoding the full image.
func decodeImage(path string) (width, height int, format, space string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, "", "", err
	}
	defer file.Close()

	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, "", "", err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0, "", "", errors.New("invalid dimensions")
	}

	return config.Width, config.Height, format, colorSpace(config.ColorModel), nil
}

// colorSpace names model for the images.color_space column, falling back to
// "unknown" for a model this package doesn't recognize.
//
// Comparing model against the package-level vars below with == is safe:
// each one is backed by an unexported *color.modelFunc pointer rather than
// a bare func value specifically so that identity comparisons like this
// work (see the color.ModelFunc doc comment) — a func value itself would
// only be comparable to nil. A paletted image (GIF, some PNGs) uses
// color.Palette, a slice type, handled separately by type assertion since
// it's never one of the fixed models below.
func colorSpace(model color.Model) string {
	switch model {
	case color.RGBAModel:
		return "rgba"
	case color.RGBA64Model:
		return "rgba64"
	case color.NRGBAModel:
		return "nrgba"
	case color.NRGBA64Model:
		return "nrgba64"
	case color.AlphaModel:
		return "alpha"
	case color.Alpha16Model:
		return "alpha16"
	case color.GrayModel:
		return "gray"
	case color.Gray16Model:
		return "gray16"
	case color.CMYKModel:
		return "cmyk"
	case color.YCbCrModel:
		return "ycbcr"
	case color.NYCbCrAModel:
		return "nycbcra"
	}

	if _, ok := model.(color.Palette); ok {
		return "palette"
	}
	return "unknown"
}
