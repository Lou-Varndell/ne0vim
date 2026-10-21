package images

import (
	"image"
	"os"
)

// Dimensions returns the pixel width and height of the image at path,
// decoding only its header rather than the full image data.
func Dimensions(path string) (width, height int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}
