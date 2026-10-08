// Package catalogmodel holds the plain view types shared between the catalog
// scanner (internal/catalog) and the templ components (web/templates/catalog).
// It imports neither, so it is a leaf package that both can depend on without
// forming an import cycle.
package catalogmodel

// CatalogPage is the full view model rendered by the catalog page: the
// breadcrumb trail to CurrentDir, the immediate sub-directories, and the
// first batch of images in CurrentDir.
type CatalogPage struct {
	CurrentDir  string
	Breadcrumb  []Crumb
	Directories []Directory
	ImageBatch
}

// ImageBatch is one page of images within a directory. NextOffset and
// HasMore drive infinite scroll: the grid's trailing sentinel element
// requests NextOffset next, and simply isn't rendered once HasMore is
// false.
type ImageBatch struct {
	Images     []Image
	NextOffset int
	HasMore    bool
}

// Directory is a single navigable sub-directory of the current directory.
type Directory struct {
	Name string
	Path string
}

// Image is a single catalogued image, with its full-size and thumbnail paths
// rooted under the /images route.
type Image struct {
	Name  string
	Path  string
	Thumb string
}

// Crumb is one segment of the breadcrumb trail leading to the current
// directory. Siblings holds the other directories at the same level, so a
// crumb can double as a dropdown for jumping sideways instead of only up.
type Crumb struct {
	Name     string
	Path     string
	Siblings []Sibling
}

// Sibling is one alternative directory at a breadcrumb segment's level,
// selectable from that segment's dropdown.
type Sibling struct {
	Name string
	Path string
}
