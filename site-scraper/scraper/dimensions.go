package scraper

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	_ "golang.org/x/image/webp"
)

// decodeDimensions returns the pixel width/height and format (e.g. "jpeg",
// "png") of the image at path. ok is false if the format is unsupported or
// decoding fails; callers treat that as non-fatal and leave the fields at
// their zero values.
func decodeDimensions(path string) (width, height int, format string, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, "", false
	}
	defer f.Close()

	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, "", false
	}

	return cfg.Width, cfg.Height, format, true
}
