package count

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"image-browser/internal/paths"
)

var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".bmp": true, ".tif": true, ".tiff": true, ".webp": true,
	".avif": true, ".heic": true,
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

func countImages(root string) (int, error) {
	count, _, err := inspectDirectory(root)
	return count, err
}

func inspectDirectory(root string) (imageCount int, hasSubdirs bool, err error) {
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
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
