# media-manager

A small Go web app for browsing images in a catalog. Server-rendered with
[templ](https://templ.guide) and [htmx](https://htmx.org), styled with
[Bulma](https://bulma.io).

## Features

- **Catalog** (`/catalog`) — browse the image directory one folder at a time.
  The breadcrumb across the top is the page's only chrome and its only
  directory navigation — there's no separate sidebar. Any segment with
  sibling directories at its level renders as a dropdown, so you can jump
  sideways (e.g. between listing folders) without walking back up first; if
  the current directory has subdirectories, one trailing dropdown lets you
  descend into one. That trailing dropdown simply doesn't render for a
  directory with no subdirectories, so a leaf folder's image grid gets the
  full width instead of an empty directory box.
- Click an image to open it full-size (`/catalog/view`) in a new tab, at its
  natural 1:1 size with scrollbars if it's larger than the viewport — not a
  bare image response. "Back to grid" and "Close" buttons keep you from
  losing your place. The thumbnail's click handler also copies its path
  (relative to the image directory) to the clipboard.
- Folder and breadcrumb navigation swap in place via htmx, with the browser
  URL kept in sync (`hx-push-url`) so back/forward and reloads work as
  expected.

Images are served from `/images/*`, backed directly by the configured image
directory.

## Requirements

- Go (see `go.mod` for the minimum version)
- [`templ`](https://templ.guide) CLI, for regenerating `*_templ.go` files
  after editing a `.templ` file:
  ```bash
  go install github.com/a-h/templ/cmd/templ@v0.3.1020
  ```
- [`air`](https://github.com/air-verse/air) (optional), for live-reload during
  development:
  ```bash
  go install github.com/air-verse/air@latest
  ```

## Getting started

```bash
make build   # vendors Bulma/htmx, generates templates, builds ./bin/server
./bin/server -image-dir ~/Images
```

Then open http://localhost:8080. If `-image-dir` is omitted, it defaults to
`$HOME/Images` and is created if it doesn't already exist.

### Development

```bash
make dev
```

Runs `templ generate --watch` alongside `air`, so editing a `.templ` file or
any `.go` file rebuilds and restarts the server automatically.

### Flags

| Flag | Default | Description |
|---|---|---|
| `-image-dir` | `$HOME/Images` | Directory to catalog. |

## Project layout

```
cmd/server/           entrypoint: flag parsing, router wiring, graceful shutdown
internal/catalog/      directory-by-directory image browser
internal/catalogmodel/ view model for the catalog
web/templates/         templ components (.templ source + generated *_templ.go)
web/static/            vendored Bulma CSS and htmx JS, plus app.css overrides
```

`internal/catalog` exposes a `Mount(r chi.Router, ...)` function that wires
its routes onto the shared `chi.Router`, and owns its own scanning/business
logic separately from its HTTP handlers.

## Testing

```bash
make test        # go test ./...
golangci-lint run ./...
```

`internal/catalog` includes scan tests covering the happy path, non-image
files being skipped (never deleted), and non-directory input.

## Security notes

- The catalog browser confines `?dir=` to the configured image directory,
  resolving symlinks and clamping `..` traversal so requests can't escape the
  image root.
