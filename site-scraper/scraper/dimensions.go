package scraper

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	_ "golang.org/x/image/webp"
)

// decodeDimensions returns the pixel width/height of the image at path.
// ok is false if the format is unsupported or decoding fails; callers
// treat that as non-fatal and leave width/height at 0.
func decodeDimensions(path string) (width, height int, ok bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, false
	}

	return cfg.Width, cfg.Height, true
}
