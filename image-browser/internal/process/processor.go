package process

import (
	"context"
	"sync"

	"image-browser/internal/images"
)

// worker decodes each job's image dimensions, format, and color model,
// reporting the outcome on results.
func worker(ctx context.Context, jobs <-chan job, results chan<- result, wg *sync.WaitGroup) {
	defer wg.Done()

	for item := range jobs {
		width, height, format, space, err := images.Decode(item.file)

		res := result{
			id:     item.id,
			file:   item.file,
			width:  width,
			height: height,
			format: format,
			space:  space,
			err:    err,
		}

		select {
		case results <- res:
		case <-ctx.Done():
			return
		}
	}
}
