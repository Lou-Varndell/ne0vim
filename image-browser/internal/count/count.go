package count

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"image-browser/internal/images"
	"image-browser/internal/paths"
)

// extraCountExtensions are formats count deliberately treats as images on
// top of images.Extensions, even though no other command in this program
// can find, open, move, scan, or thumbnail them — see "Recognized image
// extensions" in the README for why this superset is intentional.
var extraCountExtensions = map[string]struct{}{
	".avif": {}, ".heic": {},
}

// imageExtensions is images.Extensions — the single source of truth every
// other command uses — plus count's own documented extras above.
var imageExtensions = buildImageExtensions()

func buildImageExtensions() map[string]bool {
	exts := make(map[string]bool, len(images.Extensions)+len(extraCountExtensions))
	for ext := range images.Extensions {
		exts[ext] = true
	}
	for ext := range extraCountExtensions {
		exts[ext] = true
	}
	return exts
}

type imageCountFilter struct {
	min int
	max int
}

// Run executes the count command.
func Run(args []string) error {
	fs := flag.NewFlagSet("count", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "root directory to search (default: current directory)")
	imageCountArg := fs.String("image-count", "", "filter by image count: N for exactly N, or N,M for an inclusive range")
	noSubdirs := fs.Bool("no-subdirs", false, "only include directories that contain no subdirectories")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser count [flags]")
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

	if len(fs.Args()) > 0 {
		return fmt.Errorf("count: unexpected argument: %s", fs.Args()[0])
	}

	imageFilter, err := parseImageCountFilter(*imageCountArg)
	if err != nil {
		return fmt.Errorf("count: invalid --image-count: %w", err)
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = "."
	}

	rootPath, err = paths.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("count: resolving root: %w", err)
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("count: not a directory: %s: %w", rootPath, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("count: not a directory: %s", rootPath)
	}

	entries, err := os.ReadDir(rootPath)
	if err != nil {
		return fmt.Errorf("count: reading %s: %w", rootPath, err)
	}

	var subdirs []string
	for _, entry := range entries {
		if entry.IsDir() {
			subdirs = append(subdirs, entry.Name())
		}
	}
	sort.Strings(subdirs)

	for _, name := range subdirs {
		subdir := filepath.Join(rootPath, name)

		imageCount, hasSubdirs, err := inspectDirectory(subdir)
		if err != nil {
			return fmt.Errorf("count: %s: %w", subdir, err)
		}

		if imageFilter != nil && !imageFilter.matches(imageCount) {
			continue
		}

		if *noSubdirs && hasSubdirs {
			continue
		}

		fmt.Printf("%05d %s\n", imageCount, subdir)
	}

	return nil
}

func parseImageCountFilter(value string) (*imageCountFilter, error) {
	if value == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	if len(parts) > 2 {
		return nil, fmt.Errorf("expected N or N,M")
	}

	min, err := parseNonNegativeInt(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil, err
	}

	max := min
	if len(parts) == 2 {
		max, err = parseNonNegativeInt(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, err
		}
		if min > max {
			return nil, fmt.Errorf("minimum %d is greater than maximum %d", min, max)
		}
	}

	return &imageCountFilter{min: min, max: max}, nil
}

func parseNonNegativeInt(value string) (int, error) {
	if value == "" {
		return 0, fmt.Errorf("expected a non-negative integer")
	}

	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%q is not a non-negative integer", value)
	}

	return n, nil
}

func (f imageCountFilter) matches(count int) bool {
	return count >= f.min && count <= f.max
}

// inspectDirectory counts images and detects subdirectories under root. A
// subdirectory that can't be read (permission denied, broken symlink, etc.)
// is skipped rather than aborting the whole walk, matching images.Find; an
// error reading root itself is still returned, since that indicates the
// caller passed a bad starting point rather than an incidental obstruction.
func inspectDirectory(root string) (imageCount int, hasSubdirs bool, err error) {
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if entry.IsDir() {
			if path != root {
				hasSubdirs = true
			}
			return nil
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		if imageExtensions[strings.ToLower(filepath.Ext(entry.Name()))] {
			imageCount++
		}

		return nil
	})

	return imageCount, hasSubdirs, err
}
