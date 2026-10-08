package catalog

import (
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"media-site/internal/catalogdb"
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

// isImageFile reports whether path holds a decodable image. It is read-only:
// files that sniff as text or fail to decode are skipped, never modified or
// removed. Any I/O error is logged and treated as "not an image".
func isImageFile(path string, log *slog.Logger) bool {
	file, err := os.Open(path)
	if err != nil {
		log.Error("failed to open file", "path", path, "err", err)
		return false
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := io.ReadFull(file, buffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		log.Error("failed to read file", "path", path, "err", err)
		return false
	}

	if isText(http.DetectContentType(buffer[:n])) {
		return false
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		log.Error("failed to seek file", "path", path, "err", err)
		return false
	}

	if _, _, err := image.DecodeConfig(file); err != nil {
		return false
	}

	return true
}

// relCatalogPath converts an absolute filesystem path under root into the
// slash-separated, root-relative form the catalog index keys its rows on
// ("" for the root itself).
func relCatalogPath(root, fsPath string) (string, error) {
	rel, err := filepath.Rel(root, fsPath)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	return rel, nil
}

// ScanToDB walks root recursively and records every directory and
// catalogued image into db, relative to root. It is strictly read-only on
// the filesystem; callers that want a clean rebuild should call db.Reset
// first.
func ScanToDB(root string, db *catalogdb.DB, log *slog.Logger) error {
	return filepath.WalkDir(root, func(fsPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", fsPath, err)
		}

		rel, err := relCatalogPath(root, fsPath)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", fsPath, err)
		}

		if d.IsDir() {
			if rel == "" {
				return nil // the root itself isn't indexed as an entry
			}
			parent := path.Dir(rel)
			if parent == "." {
				parent = ""
			}
			if err := db.InsertDirectory(rel, d.Name(), parent); err != nil {
				return fmt.Errorf("index directory %s: %w", rel, err)
			}
			return nil
		}

		if !isImageFile(fsPath, log) {
			return nil
		}

		dir := path.Dir(rel)
		if dir == "." {
			dir = ""
		}
		if err := db.InsertImage(rel, d.Name(), dir); err != nil {
			return fmt.Errorf("index image %s: %w", rel, err)
		}
		return nil
	})
}
