package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"image-browser/internal/browse"
	"image-browser/internal/count"
	"image-browser/internal/images"
	"image-browser/internal/ingest"
	"image-browser/internal/move"
	"image-browser/internal/organize"
	"image-browser/internal/process"
	"image-browser/internal/reconcile"
	"image-browser/internal/scan"
	"image-browser/internal/shard"
	"image-browser/internal/size"
	"image-browser/internal/viewer"
	"image-browser/internal/warmthumbs"
)

// pattern holds the glob patterns (shell syntax, e.g. "*.jpg") that the
// scan command matches file names against. Derived from images.Extensions
// — the same list view/organize/move/count's per-folder preview already
// use — so scan recognizes exactly the same files every other command
// does, rather than maintaining its own independent (and historically
// narrower) list. See "Recognized image extensions" in the README.
var pattern = patternsFor(images.Extensions)

// patternsFor converts a set of extensions (lowercase, including the dot)
// into scan's glob pattern shape (e.g. ".jpg" -> "*.jpg").
func patternsFor(extensions map[string]struct{}) []string {
	patterns := make([]string, 0, len(extensions))
	for ext := range extensions {
		patterns = append(patterns, "*"+ext)
	}
	return patterns
}

// errUnknownCommand is returned by run when command doesn't match any
// known subcommand.
var errUnknownCommand = errors.New("unknown command")

func main() {
	// Exit code convention mirrors getopt/BSD tools: 2 for usage errors
	// (missing/unknown command), 1 for runtime errors from a command.
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	if err := run(os.Args[1], os.Args[2:]); err != nil {
		if errors.Is(err, errUnknownCommand) {
			fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
			usage()
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run dispatches command to the matching subpackage's Run function,
// returning errUnknownCommand if command doesn't match any of them.
// Most subpackage errors are already prefixed with the command name
// (e.g. "size: --batch-size must be a positive integer"), so run does
// not add another layer of wrapping here. The exception is flag-parsing
// failures: those come back unprefixed, and the flag package has
// already written its own message (and, for an invalid flag, a usage
// dump) to stderr before returning, so main's generic error print below
// ends up echoing that message a second time. A `-h`/`--help` request
// to a subcommand is handled as success (flag.ErrHelp), not an error.
func run(command string, args []string) error {
	switch command {
	case "browse":
		return browse.Run(args)
	case "organize":
		return organize.Run(args)
	case "move":
		return move.Run(args)
	case "view":
		return viewer.Run(args)
	case "count":
		return count.Run(args)
	case "size":
		return size.Run(args)
	case "process":
		return process.Run(args)
	case "scan":
		return scan.Run(args, pattern)
	case "import":
		return ingest.Run(args)
	case "reconcile":
		return reconcile.Run(args)
	case "shard":
		return shard.Run(args)
	case "warm-thumbnails":
		return warmthumbs.Run(args)
	case "help", "--help", "-h":
		usageTo(os.Stdout)
		return nil
	default:
		return errUnknownCommand
	}
}

// usage prints usage information for a usage error (missing or unknown
// command), per the getopt/BSD convention of routing it to stderr. It
// is a thin wrapper over usageTo for that one call site.
func usage() {
	usageTo(os.Stderr)
}

// usageTo prints usage information to w. Explicit help requests
// ("help", "--help", "-h") write to stdout via run so the output is
// capturable via redirection or piping; usage errors write to stderr
// via usage.
func usageTo(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  image-browser browse [flags]
  image-browser organize [pattern] [flags]
  image-browser move [value] [flags]
  image-browser view [pattern] [flags]
  image-browser count [flags]
  image-browser size [flags] [root]
  image-browser process [flags]
  image-browser scan -root <dir>
  image-browser import [flags]
  image-browser reconcile -root <dir> [-apply]
  image-browser shard [flags]
  image-browser warm-thumbnails [flags]

Commands:
  browse          Interactively browse, select, and move database records in a terminal UI.
  organize        Find images matching a pattern and move them to a destination.
  move            Find database records matching a field filter and move their files.
  view            Find images matching a pattern and display them in the viewer.
  count           Count images recursively in each immediate subdirectory.
  size            Group processed images by resolution and preview them.
  process         Fill in dimensions for database records missing them.
  scan            Recursively scan a directory for files to add to the database.
  import          Import a scraper's manifest.json provenance into the database.
  reconcile       Compare the database against disk under a root and fix drift.
  shard           Shard existing database-tracked files into a root's shard directories.
  warm-thumbnails Eagerly generate every missing thumbnail in the shared cache.

Run "image-browser <command> -h" for command-specific help.`)
}
