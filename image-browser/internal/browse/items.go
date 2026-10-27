package browse

import (
	"fmt"
	"path/filepath"
	"strings"

	invlib "inv-lib"
)

// recordItem adapts an invlib.FileRecord into a list.Item (and
// list.DefaultItem, via Title/Description) for display in the browse list.
//
// selected is a pointer rather than a bool: list.Model keeps its own copy
// of each list.Item value internally (it's stored in a slice, not behind a
// pointer to this struct), so toggling a plain bool field here would only
// change this copy, not the one the list renders. Routing the flag through
// a shared *bool means every reference to the same record's item — the
// one list.Model holds and the one this package iterates when collecting
// a move's selection — sees the same toggle without needing list.SetItem.
type recordItem struct {
	rec      *invlib.FileRecord
	selected *bool
}

// newRecordItem wraps rec as a recordItem, starting unselected.
func newRecordItem(rec *invlib.FileRecord) recordItem {
	selected := false
	return recordItem{rec: rec, selected: &selected}
}

// FilterValue is what list.Model's built-in fuzzy filter (triggered by "/")
// matches against. Including the site and tags alongside the path lets a
// user filter by provenance or tag, not just filename.
func (i recordItem) FilterValue() string {
	var b strings.Builder
	b.WriteString(i.rec.File.Path)
	if i.rec.Origin != nil && i.rec.Origin.Site != nil {
		b.WriteByte(' ')
		b.WriteString(*i.rec.Origin.Site)
	}
	for _, t := range i.rec.Tags {
		b.WriteByte(' ')
		b.WriteString(t.Name)
	}
	return b.String()
}

// Title renders the item's row in the list: a selection checkbox followed
// by the filename.
func (i recordItem) Title() string {
	mark := "[ ]"
	if *i.selected {
		mark = "[x]"
	}
	return mark + " " + filepath.Base(i.rec.File.Path)
}

// Description renders the item's second line: dimensions, origin site (if
// known), and containing directory.
func (i recordItem) Description() string {
	dims := "?"
	if img := i.rec.Image; img != nil && img.Width != nil && img.Height != nil {
		dims = fmt.Sprintf("%dx%d", *img.Width, *img.Height)
	}

	site := "-"
	if i.rec.Origin != nil && i.rec.Origin.Site != nil && *i.rec.Origin.Site != "" {
		site = *i.rec.Origin.Site
	}

	return fmt.Sprintf("%s\n%s\n%s",
		dims,
		site,
		filepath.Dir(i.rec.File.Path),
	)
}
