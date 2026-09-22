package main

import (
	"fmt"
	"os"

	"image-browser/internal/count"
	"image-browser/internal/organize"
	"image-browser/internal/process"
	"image-browser/internal/size"
	"image-browser/internal/viewer"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error

	switch os.Args[1] {
	case "organize":
		err = organize.Run(os.Args[2:])
	case "view":
		err = viewer.Run(os.Args[2:])
	case "count":
		err = count.Run(os.Args[2:])
	case "size":
		err = size.Run(os.Args[2:])
	case "process":
		err = process.Run(os.Args[2:])
	case "help", "--help", "-h":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  image-browser organize [pattern] [flags]
  image-browser view [pattern] [flags]
  image-browser count [flags]
  image-browser size [flags] [image-manifest.json]

Commands:
  organize    Find images matching a pattern and move them to a destination.
  view        Find images matching a pattern and display them in the viewer.
  count       Count images recursively in each immediate subdirectory.
  size        Group manifest images by resolution and preview them.
  process     Process manifest images and add their dimensions.

Run "image-browser <command> -h" for command-specific help.`)
}
