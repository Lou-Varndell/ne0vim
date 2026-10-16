package catalog

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"media-site/internal/catalogmodel"
	"media-site/internal/store"
)

// ErrDirectoryNotFound is returned by LoadCatalogPage when rel isn't in the
// catalog index.
var ErrDirectoryNotFound = errors.New("directory not found")

// ImagePageSize is how many images LoadCatalogPage and loadImageBatch load
// per batch, matching the grid's "has-4-cols" layout (24 rows per batch).
const ImagePageSize = 96

// dirPrefix returns the absolute, separator-terminated path identifying
// rel (relative to root, "" for root itself) as a LIKE/substr prefix
// against the shared files table's absolute paths.
func dirPrefix(root, rel string) string {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, filepath.FromSlash(rel))
	}
	return dir + string(filepath.Separator)
}

// LoadCatalogPage builds the view model for a single directory level of the
// catalog browser, reading only from the catalog index Sync built — no
// filesystem access. rel is the directory's path relative to root ("" for
// root itself).
func LoadCatalogPage(ctx context.Context, st *store.Store, root, rel string) (catalogmodel.CatalogPage, error) {
	page := catalogmodel.CatalogPage{CurrentDir: rel}

	exists := rel == ""
	if !exists {
		var err error
		exists, err = st.DirectoryExists(ctx, dirPrefix(root, rel))
		if err != nil {
			return page, fmt.Errorf("check directory: %w", err)
		}
	}
	if !exists {
		return page, ErrDirectoryNotFound
	}

	crumbs, err := breadcrumbs(ctx, st, root, rel)
	if err != nil {
		return page, fmt.Errorf("build breadcrumbs: %w", err)
	}
	page.Breadcrumb = crumbs

	dirs, err := st.ListSubdirectories(ctx, dirPrefix(root, rel))
	if err != nil {
		return page, fmt.Errorf("list subdirectories: %w", err)
	}
	for _, name := range dirs {
		page.Directories = append(page.Directories, catalogmodel.Directory{Name: name, Path: path.Join(rel, name)})
	}

	batch, err := loadImageBatch(ctx, st, root, rel, 0)
	if err != nil {
		return page, fmt.Errorf("list images: %w", err)
	}
	page.ImageBatch = batch

	return page, nil
}

// relCatalogPath converts an absolute filesystem path under root into the
// slash-separated, root-relative form the catalog's /images route and URL
// paths are built from.
func relCatalogPath(root, fsPath string) (string, error) {
	rel, err := filepath.Rel(root, fsPath)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// loadImageBatch fetches one ImagePageSize-sized page of images directly in
// dir (relative to root) starting at offset, converting absolute store
// paths into view-model Images. It asks ListImagePaths for one extra row so
// it can tell whether more images remain beyond this batch without a
// separate COUNT query.
func loadImageBatch(ctx context.Context, st *store.Store, root, dir string, offset int) (catalogmodel.ImageBatch, error) {
	paths, err := st.ListImagePaths(ctx, dirPrefix(root, dir), offset, ImagePageSize+1)
	if err != nil {
		return catalogmodel.ImageBatch{}, err
	}

	hasMore := len(paths) > ImagePageSize
	if hasMore {
		paths = paths[:ImagePageSize]
	}

	batch := catalogmodel.ImageBatch{
		NextOffset: offset + len(paths),
		HasMore:    hasMore,
	}
	for _, abs := range paths {
		rel, err := relCatalogPath(root, abs)
		if err != nil {
			return catalogmodel.ImageBatch{}, fmt.Errorf("relativize %s: %w", abs, err)
		}
		urlPath := path.Join("/images", rel)
		batch.Images = append(batch.Images, catalogmodel.Image{
			Name:  filepath.Base(abs),
			Path:  urlPath,
			Thumb: urlPath, // temporary
		})
	}
	return batch, nil
}

// breadcrumbs builds the trail of Crumbs from the catalog root down to rel.
// Each non-root crumb carries the other directories at its own level
// (queried from the index), powering the breadcrumb's sideways-navigation
// dropdown.
func breadcrumbs(ctx context.Context, st *store.Store, root, rel string) ([]catalogmodel.Crumb, error) {
	crumbs := []catalogmodel.Crumb{{Name: "Home", Path: ""}}
	if rel == "" {
		return crumbs, nil
	}

	var acc string
	for seg := range strings.SplitSeq(rel, "/") {
		parent := acc
		acc = path.Join(acc, seg)

		siblings, err := st.ListSubdirectories(ctx, dirPrefix(root, parent))
		if err != nil {
			return nil, fmt.Errorf("list siblings of %q: %w", parent, err)
		}

		var crumbSiblings []catalogmodel.Sibling
		for _, name := range siblings {
			crumbSiblings = append(crumbSiblings, catalogmodel.Sibling{Name: name, Path: path.Join(parent, name)})
		}

		crumbs = append(crumbs, catalogmodel.Crumb{Name: seg, Path: acc, Siblings: crumbSiblings})
	}
	return crumbs, nil
}
