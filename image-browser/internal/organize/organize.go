package organize

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"image-browser/internal/images"
	"image-browser/internal/paths"
)

// Run executes the organize command.
func Run(args []string) error {
	fs := flag.NewFlagSet("organize", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "root directory to search (default: ~/Images)")
	dest := fs.String("dest", "", "destination directory")
	execute := fs.Bool("execute", false, "actually move files")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser organize [pattern] [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	// The standard flag package stops parsing when it encounters a
	// positional argument. Extract the single pattern first, while preserving
	// flags and their values for FlagSet to parse.
	parseArgs := make([]string, 0, len(args))
	var pattern string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if !strings.HasPrefix(arg, "-") {
			if pattern != "" {
				return errors.New("organize: too many positional arguments")
			}
			pattern = arg
			continue
		}

		parseArgs = append(parseArgs, arg)

		// -root and -dest take a separate value. Keep that value with the
		// flag arguments so FlagSet does not mistake it for the pattern.
		if arg == "-root" || arg == "-dest" {
			if i+1 >= len(args) {
				return fmt.Errorf("organize: %s requires a value", arg)
			}
			i++
			parseArgs = append(parseArgs, args[i])
		}
	}

	if err := fs.Parse(parseArgs); err != nil {
		return err
	}

	if !*execute {
		return errors.New("organize requires --execute")
	}

	if *dest == "" {
		return errors.New("organize --execute requires --dest")
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = paths.DefaultRoot()
	}

	rootPath, err := paths.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("organize: resolving root: %w", err)
	}

	destPath, err := paths.Abs(*dest)
	if err != nil {
		return fmt.Errorf("organize: resolving dest: %w", err)
	}

	matches, err := images.Find(rootPath, pattern)
	if err != nil {
		return fmt.Errorf("organize: searching %s: %w", rootPath, err)
	}

	if len(matches) == 0 {
		fmt.Printf("No images found for %q under %s\n", pattern, rootPath)
		return nil
	}

	if err := os.MkdirAll(destPath, 0o755); err != nil {
		return fmt.Errorf("organize: creating dest %s: %w", destPath, err)
	}

	var failures int

	for _, src := range matches {
		destPathForFile, duplicate, err := images.ResolveDestination(src, destPath)
		if err != nil {
			fmt.Fprintf(
				os.Stderr,
				"  error resolving destination for %s: %v\n",
				paths.RelOrPath(src, rootPath),
				err,
			)
			failures++
			continue
		}

		if duplicate {
			fmt.Printf("  skip (duplicate): %s\n", paths.RelOrPath(src, rootPath))
			continue
		}

		fmt.Printf(
			"  %s -> %s\n",
			paths.RelOrPath(src, rootPath),
			paths.RelOrPath(destPathForFile, rootPath),
		)

		if err := images.Move(src, destPathForFile); err != nil {
			fmt.Fprintf(
				os.Stderr,
				"  error moving %s: %v\n",
				paths.RelOrPath(src, rootPath),
				err,
			)
			failures++
			continue
		}
	}

	if failures > 0 {
		return fmt.Errorf("organize: %d file(s) failed to move", failures)
	}

	return nil
}
