package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"image-browser/internal/manifest"

	_ "golang.org/x/image/webp"
)

func isImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return imageExtensions[ext]
}

func imageDimensions(filePath string) (width, height int, err error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0, errors.New("invalid dimensions")
	}
	return config.Width, config.Height, nil
}

func processImageFile(filePath string) (int, int, error) {
	if filePath == "" {
		return 0, 0, errors.New("empty file path")
	}
	if !isImageFile(filePath) {
		return 0, 0, errNonImage
	}
	return imageDimensions(filePath)
}

func worker(ctx context.Context, jobs <-chan job, results chan<- result, wg *sync.WaitGroup) {
	defer wg.Done()

	for item := range jobs {
		width, height, err := processImageFile(item.file)
		select {
		case results <- result{file: item.file, width: width, height: height, err: err}:
		case <-ctx.Done():
			return
		}
	}
}

func writeJSON(outputFile string, data []manifest.Entry) error {
	dir := filepath.Dir(outputFile)
	tempFile, err := os.CreateTemp(dir, ".checkpoint-*")
	if err != nil {
		return fmt.Errorf("creating temporary output file: %w", err)
	}

	tempName := tempFile.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tempName)
		}
	}()

	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(data); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("encoding %s: %w", tempName, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tempName, err)
	}
	if err := os.Rename(tempName, outputFile); err != nil {
		return fmt.Errorf("renaming %s to %s: %w", tempName, outputFile, err)
	}

	renamed = true
	return nil
}

func classifyFiles(data []manifest.Entry, maxWorkers int) (classified []manifest.Entry, markedMissing, markedNonImage, markedEmpty int) {
	sem := make(chan struct{}, maxWorkers)
	missing := make([]bool, len(data))
	var wg sync.WaitGroup

	for i := range data {
		if data[i].File == "" {
			data[i].Status = manifest.StatusInvalid
			markedEmpty++
			continue
		}

		if !isImageFile(data[i].File) {
			data[i].Status = manifest.StatusNonImage
			markedNonImage++
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(i int, path string) {
			defer wg.Done()
			defer func() { <-sem }()

			_, err := os.Stat(path)
			switch {
			case err == nil:
			case errors.Is(err, os.ErrNotExist):
				missing[i] = true
			default:
				log.Printf("cannot stat %s: %v", path, err)
			}
		}(i, data[i].File)
	}

	wg.Wait()
	for i := range data {
		if missing[i] {
			data[i].Status = manifest.StatusMissing
			markedMissing++
		}
	}

	return data, markedMissing, markedNonImage, markedEmpty
}
