// Package static embeds the vendored CSS and JS assets served under
// /static/*, so the server binary doesn't depend on the process's working
// directory to find them.
package static

import "embed"

// FS holds the vendored Bulma CSS, htmx JS, and app.css overrides. Run
// `make bulma htmx` (or `make build`/`make dev`, which depend on it) before
// building so these files exist for go:embed to pick up.
//
//go:embed css js
var FS embed.FS
