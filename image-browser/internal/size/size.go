package size

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"image-browser/internal/manifest"
	"image-browser/internal/paths"
	"image-browser/internal/trash"
	"image-browser/internal/viewer"
)

const defaultManifest = "image-manifest.json"

// ManifestRecord contains the manifest fields needed by the size command.
// type ManifestRecord struct {
// 	File   string `json:"file"`
// 	Status string `json:"status"`
// 	Width  *int   `json:"width"`
// 	Height *int   `json:"height"`
// }

// Group contains all unique files sharing an exact width and height.
type Group struct {
	Width  int
	Height int
	Files  []string
}

// Run executes the size command.
func Run(args []string) error {
	fs := flag.NewFlagSet("size", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	largest := fs.Bool("largest", false, "show largest resolutions first")
	smallest := fs.Bool("smallest", false, "show smallest resolutions first (default)")
	batchSize := fs.Int("batch-size", 40, "maximum files per preview batch")
	allowTrash := fs.Bool("trash", false, "allow moving a reviewed batch to Trash")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser size [flags] [image-manifest.json]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *batchSize < 1 {
		return errors.New("size: --batch-size must be a positive integer")
	}
	if *largest && *smallest {
		return errors.New("size: --largest and --smallest cannot be used together")
	}

	manifestFile := defaultManifest
	positional := fs.Args()
	if len(positional) > 1 {
		return errors.New("size: too many positional arguments")
	}
	if len(positional) == 1 {
		manifestFile = positional[0]
	}

	groups, err := Load(manifestFile)
	if err != nil {
		return err
	}

	if *largest {
		SortGroups(groups, true)
	} else {
		SortGroups(groups, false)
	}

	session := viewer.NewSession()

	result := make(chan error, 1)
	go func() {
		defer session.Quit()

		for groupNumber, group := range groups {
			quit, err := reviewGroup(session, groupNumber+1, group, *batchSize, *allowTrash)
			if err != nil {
				result <- err
				return
			}
			if quit {
				result <- nil
				return
			}
		}

		fmt.Fprintln(os.Stdout, "Done.")
		result <- nil
	}()

	session.Run()

	return <-result
}

// Load reads the manifest and groups existing, successfully processed image
// records by exact resolution.
func Load(filename string) ([]Group, error) {
	path, err := paths.Abs(filename)
	if err != nil {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var records []manifest.Entry
	if err := json.NewDecoder(file).Decode(&records); err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}

	groups := make(map[[2]int]map[string]struct{})

	for _, record := range records {
		if record.Status != "processed" {
			continue
		}

		if record.Width == 0 || record.Height == 0 {
			continue
		}

		if record.File == "" {
			continue
		}

		if _, err := os.Stat(record.File); err != nil {
			continue
		}

		key := [2]int{record.Width, record.Height}

		if groups[key] == nil {
			groups[key] = make(map[string]struct{})
		}

		groups[key][record.File] = struct{}{}
	}

	result := make([]Group, 0, len(groups))

	for key, files := range groups {
		// Only review resolutions shared by at least two unique images.
		// if len(files) < 2 {
		// 	continue
		// }

		pathsForGroup := make([]string, 0, len(files))

		for file := range files {
			pathsForGroup = append(pathsForGroup, file)
		}

		sort.Strings(pathsForGroup)

		result = append(result, Group{
			Width:  key[0],
			Height: key[1],
			Files:  pathsForGroup,
		})
	}

	return result, nil
}

// SortGroups sorts groups by pixel count, smallest first unless largest is true.
func SortGroups(groups []Group, largest bool) {
	sort.Slice(groups, func(i, j int) bool {
		pi := groups[i].Width * groups[i].Height
		pj := groups[j].Width * groups[j].Height

		if pi != pj {
			if largest {
				return pi > pj
			}
			return pi < pj
		}

		if groups[i].Width != groups[j].Width {
			return groups[i].Width < groups[j].Width
		}

		return groups[i].Height < groups[j].Height
	})
}

func reviewGroup(session *viewer.Session, groupNumber int, group Group, batchSize int, allowTrash bool) (bool, error) {
	totalFiles := len(group.Files)
	totalBatches := (totalFiles + batchSize - 1) / batchSize
	pixels := group.Width * group.Height
	dimensions := fmt.Sprintf("%dx%d", group.Width, group.Height)

	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, "========================================")
	fmt.Fprintf(os.Stdout, "Group %d: %s\n", groupNumber, dimensions)
	fmt.Fprintf(os.Stdout, "Resolution: %d pixels\n", pixels)
	fmt.Fprintf(os.Stdout, "Files: %d\n", totalFiles)
	fmt.Fprintf(os.Stdout, "Batches: %d\n", totalBatches)
	fmt.Fprintln(os.Stdout, "========================================")

	for batch := 0; batch < totalBatches; batch++ {
		start := batch * batchSize
		end := start + batchSize

		if end > totalFiles {
			end = totalFiles
		}

		batchFiles := group.Files[start:end]

		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "----------------------------------------")
		fmt.Fprintf(os.Stdout, "Group %d: %s\n", groupNumber, dimensions)
		fmt.Fprintf(os.Stdout, "Batch %d/%d: files %d-%d\n", batch+1, totalBatches, start+1, end)
		fmt.Fprintf(os.Stdout, "Files in batch: %d\n", len(batchFiles))
		fmt.Fprintln(os.Stdout, "----------------------------------------")

		for _, path := range batchFiles {
			fmt.Fprintf(os.Stdout, "  %s\n", path)
		}

		var trashFunc func() error
		if allowTrash {
			trashFunc = func() error {
				return trashBatch(batchFiles)
			}
		}

		session.ShowImages(
			"",
			dimensions,
			batchFiles,
			allowTrash,
			trashFunc,
			viewer.ReviewContinue,
		)

		action := session.WaitAction()

		switch action {
		case viewer.ReviewQuit:
			fmt.Fprintf(os.Stdout, "Stopped at group %d, batch %d.\n", groupNumber, batch+1)
			return true, nil

		case viewer.ReviewContinue:
			// Continue with the next batch.
		}
	}

	return false, nil
}

func trashBatch(files []string) error {
	var failures int

	for _, path := range files {
		if err := trash.Move(path); err != nil {
			failures++

			fmt.Fprintf(
				os.Stderr,
				"  Warning: failed to move to Trash: %s: %v\n",
				path,
				err,
			)
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d file(s) could not be moved to Trash", failures)
	}

	return nil
}
