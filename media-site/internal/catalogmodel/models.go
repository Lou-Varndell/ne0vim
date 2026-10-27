// Package catalogmodel holds the plain view types shared between the catalog
// scanner (internal/catalog) and the templ components (web/templates/catalog).
// It imports neither, so it is a leaf package that both can depend on without
// forming an import cycle.
package catalogmodel

import (
	"net/url"
	"strconv"
	"strings"
)

// CatalogPage is the full view model rendered by the catalog page: the
// breadcrumb trail to CurrentDir, the immediate sub-directories, and the
// first batch of images in CurrentDir. Filter is whatever flyout-panel
// criteria produced ImageBatch — zero-value when browsing unfiltered.
type CatalogPage struct {
	CurrentDir  string
	Breadcrumb  []Crumb
	Directories []Directory
	Filter      FileFilter
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

// FlyoutData is the aggregated inv.db summary for a directory and
// everything beneath it, rendered in the left-edge flyout panel. The
// aggregates are always computed over the full, unfiltered subtree —
// Filter only echoes back whatever criteria are already applied to the
// catalog page, so the panel's inputs reflect the active search without
// the pickable values (tag names, sites, ranges) shrinking along with it.
type FlyoutData struct {
	Dir        string
	TotalFiles int
	Tags       []TagCount
	Sites      []SiteCount
	Statuses   []StatusCount
	Dimensions DimensionRange
	Discovered DateRange
	Filter     FileFilter
}

// TagCount is one distinct tag and how many files in scope carry it.
type TagCount struct {
	Name  string
	Count int
}

// SiteCount is one distinct origin site and how many files in scope came
// from it.
type SiteCount struct {
	Site  string
	Count int
}

// StatusCount is one distinct files.status value and how many files in
// scope carry it.
type StatusCount struct {
	Status string
	Count  int
}

// DimensionRange is the min/max image width and height observed in scope.
// All four fields are zero when no catalogued image carries dimensions.
type DimensionRange struct {
	WidthMin, WidthMax   int
	HeightMin, HeightMax int
}

// DateRange is the earliest and latest origins.discovered_at observed in
// scope, formatted YYYY-MM-DD. Both fields are empty when no file in scope
// has a known origin.
type DateRange struct {
	From, To string
}

// FileFilter is the set of criteria currently applied to a catalog page —
// built from the flyout panel's form and, once applied, re-threaded through
// pagination so "load more" keeps matching the same criteria. Multiple
// selected Tags are ANDed: a file must carry all of them, not just one.
type FileFilter struct {
	Tags                 []string
	Site                 string
	Status               string
	WidthMin, WidthMax   int
	HeightMin, HeightMax int
	DiscoveredFrom       string
	DiscoveredTo         string
}

// Empty reports whether no criteria are set, i.e. browsing should use the
// normal single-directory listing rather than a recursive filtered search.
func (f FileFilter) Empty() bool {
	return len(f.Tags) == 0 &&
		f.Site == "" &&
		f.Status == "" &&
		f.WidthMin == 0 && f.WidthMax == 0 &&
		f.HeightMin == 0 && f.HeightMax == 0 &&
		f.DiscoveredFrom == "" && f.DiscoveredTo == ""
}

// QueryValues re-encodes f as URL query parameters — the form used both to
// drive /catalog filtering and to carry an applied filter forward into
// infinite-scroll continuation requests.
func (f FileFilter) QueryValues() url.Values {
	v := url.Values{}
	if len(f.Tags) > 0 {
		v.Set("tag", strings.Join(f.Tags, ","))
	}
	if f.Site != "" {
		v.Set("site", f.Site)
	}
	if f.Status != "" {
		v.Set("status", f.Status)
	}
	if f.WidthMin > 0 {
		v.Set("widthMin", strconv.Itoa(f.WidthMin))
	}
	if f.WidthMax > 0 {
		v.Set("widthMax", strconv.Itoa(f.WidthMax))
	}
	if f.HeightMin > 0 {
		v.Set("heightMin", strconv.Itoa(f.HeightMin))
	}
	if f.HeightMax > 0 {
		v.Set("heightMax", strconv.Itoa(f.HeightMax))
	}
	if f.DiscoveredFrom != "" {
		v.Set("discoveredFrom", f.DiscoveredFrom)
	}
	if f.DiscoveredTo != "" {
		v.Set("discoveredTo", f.DiscoveredTo)
	}
	return v
}
