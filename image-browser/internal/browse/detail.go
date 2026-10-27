package browse

import (
	"fmt"
	"strings"

	"github.com/dustin/go-humanize"

	invlib "inv-lib"
)

// renderDetail formats r's full database row (file, image, origin, tags)
// as the scrollable content of the detail screen's viewport.
func renderDetail(r *invlib.FileRecord) string {
	var b strings.Builder

	field := func(label, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(&b, "%s %s\n", fieldLabelStyle.Render(label), value)
	}

	field("Path", r.File.Path)
	field("Size", humanize.Bytes(uint64(r.File.Filesize)))
	field("MIME", derefOr(r.File.MimeType, ""))
	field("Hash", derefOr(r.File.Hash, ""))
	field("Status", derefOr(r.File.Status, ""))
	field("Error", derefOr(r.File.Error, ""))
	field("Scanned", r.File.ScannedAt.Format("2006-01-02 15:04:05"))

	if img := r.Image; img != nil {
		b.WriteByte('\n')
		if img.Width != nil && img.Height != nil {
			field("Dimensions", fmt.Sprintf("%dx%d", *img.Width, *img.Height))
		}
		field("Format", derefOr(img.Format, ""))
		field("Color", derefOr(img.ColorSpace, ""))
	}

	if o := r.Origin; o != nil {
		b.WriteByte('\n')
		field("Type", o.Type)
		field("Site", derefOr(o.Site, ""))
		field("URL", derefOr(o.URL, ""))
		field("Identifier", derefOr(o.Identifier, ""))
		field("Original", derefOr(o.OriginalFile, ""))
		if o.DiscoveredAt != nil {
			field("Discovered", o.DiscoveredAt.Format("2006-01-02 15:04:05"))
		}
	}

	if len(r.Tags) > 0 {
		names := make([]string, len(r.Tags))
		for i, t := range r.Tags {
			names[i] = t.Name
		}
		b.WriteByte('\n')
		field("Tags", strings.Join(names, ", "))
	}

	return b.String()
}

// derefOr returns *p, or fallback if p is nil.
func derefOr(p *string, fallback string) string {
	if p == nil {
		return fallback
	}
	return *p
}
