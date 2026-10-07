package catalog

import (
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"media-manager/internal/catalogmodel"
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

// imagePath builds the /images-rooted URL path for a file at fsPath, relative
// to root. It uses path.Join (not filepath.Join) so the result always uses
// forward slashes, correct for a URL on every platform.
func imagePath(root, fsPath string) (string, error) {
	rel, err := filepath.Rel(root, fsPath)
	if err != nil {
		return "", err
	}
	return path.Join("/images", filepath.ToSlash(rel)), nil
}

// breadcrumbs builds the trail of Crumbs from root down to currentDir. The
// Path of each crumb is the directory's path relative to root (slash-joined,
// "" for root), ready to hand back as ?dir=. The root crumb is labelled
// "Home".
func breadcrumbs(root, currentDir string) ([]catalogmodel.Crumb, error) {
	rel, err := filepath.Rel(root, currentDir)
	if err != nil {
		return nil, err
	}
	rel = filepath.ToSlash(rel)

	crumbs := []catalogmodel.Crumb{{Name: "Home", Path: ""}}
	if rel == "." {
		return crumbs, nil
	}

	var acc string
	for seg := range strings.SplitSeq(rel, "/") {
		parent := acc
		acc = path.Join(acc, seg)

		siblings, err := siblingDirs(root, parent)
		if err != nil {
			return nil, err
		}

		crumbs = append(crumbs, catalogmodel.Crumb{Name: seg, Path: acc, Siblings: siblings})
	}
	return crumbs, nil
}

// siblingDirs lists the immediate sub-directories of the directory at
// relDir (relative to root, "" meaning root itself) — the alternatives
// offered by a breadcrumb segment's dropdown at that level.
func siblingDirs(root, relDir string) ([]catalogmodel.Sibling, error) {
	dir := filepath.Join(root, filepath.FromSlash(relDir))

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("list directory %s: %w", dir, err)
	}

	var siblings []catalogmodel.Sibling
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		siblings = append(siblings, catalogmodel.Sibling{
			Name: entry.Name(),
			Path: path.Join(relDir, entry.Name()),
		})
	}
	return siblings, nil
}

// ScanCatalog lists a single directory level for the catalog browser. root is
// the confinement boundary (the configured image directory); currentDir must
// resolve to root or a descendant of it. It returns the immediate
// sub-directories, the images living directly in currentDir, and the
// breadcrumb trail from root. Scanning is strictly read-only.
func ScanCatalog(root, currentDir string, log *slog.Logger) (catalogmodel.CatalogPage, error) {
	page := catalogmodel.CatalogPage{}

	info, err := os.Stat(currentDir)
	if err != nil {
		return page, fmt.Errorf("access directory: %w", err)
	}
	if !info.IsDir() {
		return page, fmt.Errorf("not a directory: %s", currentDir)
	}

	crumbs, err := breadcrumbs(root, currentDir)
	if err != nil {
		return page, fmt.Errorf("build breadcrumbs: %w", err)
	}
	page.Breadcrumb = crumbs

	if rel, err := filepath.Rel(root, currentDir); err == nil {
		page.CurrentDir = filepath.ToSlash(rel)
	}

	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return page, fmt.Errorf("read directory: %w", err)
	}

	for _, entry := range entries {
		fsPath := filepath.Join(currentDir, entry.Name())

		if entry.IsDir() {
			rel, err := filepath.Rel(root, fsPath)
			if err != nil {
				log.Error("failed to get rel", "path", fsPath, "err", err)
				continue
			}
			page.Directories = append(page.Directories, catalogmodel.Directory{
				Name: entry.Name(),
				Path: filepath.ToSlash(rel),
			})
			continue
		}

		if !isImageFile(fsPath, log) {
			continue
		}

		urlPath, err := imagePath(root, fsPath)
		if err != nil {
			log.Error("failed to build image path", "path", fsPath, "err", err)
			continue
		}
		page.Images = append(page.Images, catalogmodel.Image{
			Name:  entry.Name(),
			Path:  urlPath,
			Thumb: urlPath, // temporary
		})
	}

	return page, nil
}
