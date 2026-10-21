package catalog

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"media-site/internal/store"
)

func isText(mime string) bool {
	switch {
	case strings.HasPrefix(mime, "text/"):
		return true
	case mime == "application/json":
		return true
	case mime == "application/xml":
		return true
	case mime == "application/javascript":
		return true
	case mime == "application/x-yaml":
		return true
	case mime == "application/yaml":
		return true
	default:
		return false
	}
}

// decodedImage is what inspectFile learns about a file that decodes as an
// image: its dimensions, format, and sniffed MIME type.
type decodedImage struct {
	mimeType string
	width    int
	height   int
	format   string
}

// inspectFile reports whether path holds a decodable image, decoding it at
// most once. Files that sniff as text, or fail to decode, are reported as
// not an image. It is read-only: files that fail either check are skipped,
// never modified or removed. Any I/O error is logged and treated as "not
// an image".
func inspectFile(path string, log *slog.Logger) (decodedImage, bool) {
	file, err := os.Open(path)
	if err != nil {
		log.Error("failed to open file", "path", path, "err", err)
		return decodedImage{}, false
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		log.Error("failed to read file", "path", path, "err", err)
		return decodedImage{}, false
	}
	mimeType := http.DetectContentType(buffer[:n])

	if isText(mimeType) {
		return decodedImage{}, false
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		log.Error("failed to seek file", "path", path, "err", err)
		return decodedImage{}, false
	}

	cfg, format, err := image.DecodeConfig(file)
	if err != nil {
		return decodedImage{}, false
	}

	return decodedImage{mimeType: mimeType, width: cfg.Width, height: cfg.Height, format: format}, true
}

// Sync walks root recursively and idempotently records every catalogued
// image under it into st — files already indexed (by this or any other
// tool sharing the inventory database) are left alone except for picking
// up a missing images row. It is strictly read-only on the filesystem;
// callers should follow it with a prune of root's own subtree to clear out
// entries for files removed since the last sync.
func Sync(ctx context.Context, root string, st *store.Store, log *slog.Logger) error {
	return filepath.WalkDir(root, func(fsPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", fsPath, err)
		}
		if d.IsDir() {
			return nil
		}

		img, ok := inspectFile(fsPath, log)
		if !ok {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", fsPath, err)
		}

		id, _, err := st.AddFile(ctx, store.File{
			Path:      fsPath,
			Filename:  d.Name(),
			Extension: filepath.Ext(fsPath),
			MIMEType:  img.mimeType,
			Filesize:  info.Size(),
		})
		if err != nil {
			return fmt.Errorf("index file %s: %w", fsPath, err)
		}

		if _, err := st.AddImage(ctx, store.Image{
			FileID: id,
			Width:  img.width,
			Height: img.height,
			Format: img.format,
		}); err != nil {
			return fmt.Errorf("index image %s: %w", fsPath, err)
		}
		return nil
	})
}
