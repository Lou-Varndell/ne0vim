package catalog

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"media-site/internal/catalogdb"
	"media-site/internal/catalogmodel"
)

// ErrDirectoryNotFound is returned by LoadCatalogPage when rel isn't in the
// catalog index.
var ErrDirectoryNotFound = errors.New("directory not found")

// ImagePageSize is how many images LoadCatalogPage and loadImageBatch load
// per batch, matching the grid's "has-4-cols" layout (24 rows per batch).
const ImagePageSize = 96

// LoadCatalogPage builds the view model for a single directory level of the
// catalog browser, reading only from the catalog index built by ScanToDB —
// no filesystem access. rel is the directory's path relative to the
// catalog root ("" for the root itself).
func LoadCatalogPage(db *catalogdb.DB, rel string) (catalogmodel.CatalogPage, error) {
	page := catalogmodel.CatalogPage{CurrentDir: rel}

	exists, err := db.DirectoryExists(rel)
	if err != nil {
		return page, fmt.Errorf("check directory: %w", err)
	}
	if !exists {
		return page, ErrDirectoryNotFound
	}

	crumbs, err := breadcrumbs(db, rel)
	if err != nil {
		return page, fmt.Errorf("build breadcrumbs: %w", err)
	}
	page.Breadcrumb = crumbs

	dirs, err := db.ListSubdirectories(rel)
	if err != nil {
		return page, fmt.Errorf("list subdirectories: %w", err)
	}
	for _, d := range dirs {
		page.Directories = append(page.Directories, catalogmodel.Directory{Name: d.Name, Path: d.Path})
	}

	batch, err := loadImageBatch(db, rel, 0)
	if err != nil {
		return page, fmt.Errorf("list images: %w", err)
	}
	page.ImageBatch = batch

	return page, nil
}

// loadImageBatch fetches one ImagePageSize-sized page of images in dir
// starting at offset, converting catalogdb rows into view-model Images.
// It asks ListImages for one extra row so it can tell whether more images
// remain beyond this batch without a separate COUNT query.
func loadImageBatch(db *catalogdb.DB, dir string, offset int) (catalogmodel.ImageBatch, error) {
	rows, err := db.ListImages(dir, offset, ImagePageSize+1)
	if err != nil {
		return catalogmodel.ImageBatch{}, err
	}

	hasMore := len(rows) > ImagePageSize
	if hasMore {
		rows = rows[:ImagePageSize]
	}

	batch := catalogmodel.ImageBatch{
		NextOffset: offset + len(rows),
		HasMore:    hasMore,
	}
	for _, img := range rows {
		urlPath := path.Join("/images", img.Path)
		batch.Images = append(batch.Images, catalogmodel.Image{
			Name:  img.Name,
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
func breadcrumbs(db *catalogdb.DB, rel string) ([]catalogmodel.Crumb, error) {
	crumbs := []catalogmodel.Crumb{{Name: "Home", Path: ""}}
	if rel == "" {
		return crumbs, nil
	}

	var acc string
	for seg := range strings.SplitSeq(rel, "/") {
		parent := acc
		acc = path.Join(acc, seg)

		siblings, err := db.ListSubdirectories(parent)
		if err != nil {
			return nil, fmt.Errorf("list siblings of %q: %w", parent, err)
		}

		var crumbSiblings []catalogmodel.Sibling
		for _, s := range siblings {
			crumbSiblings = append(crumbSiblings, catalogmodel.Sibling{Name: s.Name, Path: s.Path})
		}

		crumbs = append(crumbs, catalogmodel.Crumb{Name: seg, Path: acc, Siblings: crumbSiblings})
	}
	return crumbs, nil
}
