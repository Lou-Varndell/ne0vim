package main

import (
	"errors"
	"fmt"
	"os"

	"image-browser/internal/count"
	"image-browser/internal/organize"
	"image-browser/internal/process"
	"image-browser/internal/size"
	"image-browser/internal/viewer"
)

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
		fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

var errUnknownCommand = errors.New("unknown command")

// run dispatches command to the matching subpackage's Run function,
// returning errUnknownCommand if command doesn't match any of them.
func run(command string, args []string) error {
	switch command {
	case "organize":
		return organize.Run(args)
	case "view":
		return viewer.Run(args)
	case "count":
		return count.Run(args)
	case "size":
		return size.Run(args)
	case "process":
		return process.Run(args)
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return errUnknownCommand
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  image-browser organize [pattern] [flags]
  image-browser view [pattern] [flags]
  image-browser count [flags]
  image-browser size [flags] [image-manifest.json]
  image-browser process [flags] [image-manifest.json]

Commands:
  organize    Find images matching a pattern and move them to a destination.
  view        Find images matching a pattern and display them in the viewer.
  count       Count images recursively in each immediate subdirectory.
  size        Group manifest images by resolution and preview them.
  process     Process manifest images and add their dimensions.

Run "image-browser <command> -h" for command-specific help.`)
}
