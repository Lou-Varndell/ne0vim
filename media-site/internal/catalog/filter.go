package catalog

import (
	"net/http"
	"strconv"
	"strings"

	"media-site/internal/catalogmodel"
)

// parseFilter reads a FileFilter out of r's query parameters. It is the
// inverse of FileFilter.QueryValues.
func parseFilter(r *http.Request) catalogmodel.FileFilter {
	q := r.URL.Query()
	return catalogmodel.FileFilter{
		Tags:           splitTags(q.Get("tag")),
		Site:           q.Get("site"),
		Status:         q.Get("status"),
		WidthMin:       atoiOrZero(q.Get("widthMin")),
		WidthMax:       atoiOrZero(q.Get("widthMax")),
		HeightMin:      atoiOrZero(q.Get("heightMin")),
		HeightMax:      atoiOrZero(q.Get("heightMax")),
		DiscoveredFrom: q.Get("discoveredFrom"),
		DiscoveredTo:   q.Get("discoveredTo"),
	}
}

// splitTags parses a comma-separated tag field into its trimmed, non-empty
// parts.
func splitTags(raw string) []string {
	var tags []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			tags = append(tags, part)
		}
	}
	return tags
}

// atoiOrZero parses s as an int, treating anything unparseable (including
// empty) as "no bound set" rather than a request error.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}
