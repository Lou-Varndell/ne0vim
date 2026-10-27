# media-site

A small Go web app that browses a directory of images one folder at a
time. Server-rendered with [templ](https://templ.guide) and
[htmx](https://htmx.org) (Bulma 1.0.4 for styling), it reads entirely from
a shared SQLite inventory database rather than walking the filesystem
itself — point it at an image directory and database that another tool
(`image-browser`'s `scan`, `site-scraper`, `import`) has already indexed.

## Contents

- [Features](#features)
- [How it works](#how-it-works)
- [Requirements](#requirements)
- [Building](#building)
- [Running](#running)
- [Flags](#flags)
- [Routes](#routes)
- [The flyout filter panel](#the-flyout-filter-panel)
- [The inventory database](#the-inventory-database)
- [Development](#development)
- [Testing](#testing)
- [Security notes](#security-notes)
- [Project layout](#project-layout)

## Features

- **Catalog** (`/catalog`) — browse the image directory one folder at a
  time. The breadcrumb across the top is the page's only chrome and its
  only directory navigation — there's no separate sidebar. Any segment
  with more than one sibling directory at its level renders as a
  `<select>` dropdown, so you can jump sideways (e.g. between sibling
  listing folders) without walking back up first; if the current
  directory itself has subdirectories, one trailing dropdown lets you
  descend into one. That trailing dropdown simply doesn't render for a
  directory with no subdirectories, so a leaf folder's image grid gets the
  full width instead of an empty directory box. Only directories that
  contain at least one catalogued image somewhere beneath them are
  considered navigable — a subdirectory of non-image files (or an empty
  one) never appears in a breadcrumb dropdown.
- **Infinite scroll.** The image grid loads `ImagePageSize` (96) images at
  a time; a trailing sentinel cell fetches the next batch from
  `/catalog/images/more` once it scrolls into view and swaps itself out
  for the result (more cells, plus a fresh sentinel if more remain).
- **Full-size image view** (`/catalog/view`) — click a thumbnail to open
  the image at its natural 1:1 size, in a new tab, with scrollbars if it's
  larger than the viewport — not a bare image response. "← Back to grid"
  and "Close" buttons keep you from losing your place. Clicking a
  thumbnail in the grid also copies its path (relative to the image
  directory) to the clipboard via `navigator.clipboard.writeText`.
- **Left-edge filter/search flyout** (`/catalog/flyout`) — see
  [The flyout filter panel](#the-flyout-filter-panel) below.
- Folder, breadcrumb, and filter navigation all swap in place via htmx,
  with the browser URL kept in sync (`hx-push-url`) so back/forward and
  reloads work as expected.
- On-demand, cached **thumbnails** (`/thumbs/*`) — generated the first time
  an image is requested and reused afterward, via the shared
  `invlib.ThumbCache` (see [How it works](#how-it-works)).
- Full-size images are served directly from disk at `/images/*`; vendored
  CSS/JS is served from `/static/*`. The root path `/` redirects
  (`302 Found`) to `/catalog`.

## How it works

- **media-site is a read-only consumer of the shared inventory database in
  normal operation.** Nothing in the `cmd/server` binary walks
  `-image-dir` or writes rows into the database — `internal/store.Store`
  does define `AddFile`/`AddImage`, but in this repository they're only
  ever called from this module's own tests, to seed fixture rows. In
  practice, the catalog is only as complete as whatever indexed
  `-image-dir` into `-db-path` beforehand (`image-browser scan`,
  `site-scraper`, `image-browser import`, …); a file that exists on disk
  but was never indexed simply never appears.
- **media-site does not import `invlib.Open`/`invlib.DefaultPath` for its
  own database connection**, even though it reads and writes the exact
  same SQLite file and table shapes `inv-lib` defines. `internal/db` owns
  its own byte-for-byte copy of the schema (`schema.sql`, `indexes.sql`)
  and its own legacy-column migration logic (`migrate.go`), opened via
  `db.Open`/`db.DefaultPath`. The two implementations are wire-compatible
  — same default path, same `$INVENTORY_MANAGER_DB` override, identical
  table/column shapes, including the unused-by-media-site `videos`,
  `audio`, and `media_probe` tables — but they are two independently
  maintained copies of the same migration code, not a shared import. The
  **only** thing media-site imports from `inv-lib` itself is
  `invlib.ThumbCache` (thumbnail generation/caching) — see
  [The inventory database](#the-inventory-database) for how to keep the
  two schema copies in sync if either one changes.
- **Every handler jails user-supplied paths to `-image-dir` before
  touching the filesystem or building a SQL query.** `?dir=` (catalog
  browsing) and `?path=` (the image viewer) go through
  `resolveDir`/`canonicalRoot` in `internal/catalog/handlers.go`, which
  cleans `..` segments and resolves symlinks before confirming the result
  is at-or-under the canonicalized image root. The raw `/images/*` and
  `/thumbs/*` file servers in `cmd/server/main.go` apply the same
  `path.Clean("/"+...)` trick independently, since they're wired up
  directly as `http.Handler`s outside the catalog package. See
  [Security notes](#security-notes).
- **Thumbnails are generated and cached on first request**, not
  pre-warmed. `thumbFileServer` calls `invlib.ThumbCache.Generate` (150px
  longest edge — the same size and shared, content-hash-keyed cache
  directory `image-browser` and `inventory-manager` already populate when
  displaying the same images), so a thumbnail any of those tools has
  already generated for an image is reused here instead of being
  regenerated, and vice versa.

## Requirements

- Go — `go.mod` pins `go 1.27.1`.
- **`inv-lib` checked out as a sibling directory.** `go.mod` has `replace
  inv-lib => ../inv-lib`, so it must exist at `../inv-lib` relative to
  this repo's own directory (e.g. if this repo is at `~/src/media-site`,
  `inv-lib` must be at `~/src/inv-lib`) before `go build`/`go run`/`go
  test` will work. This is the same sibling-checkout requirement
  `image-browser`, `site-scraper`, and `inventory-manager` all have.
- The [`templ`](https://templ.guide) CLI, for regenerating `*_templ.go`
  files after editing a `.templ` file (both the `.templ` source and its
  generated `_templ.go` output are committed to this repo, so you only
  need `templ` if you're changing a template — a plain `go build` works
  against the checked-in generated files as-is):
  ```bash
  go install github.com/a-h/templ/cmd/templ@v0.3.1020
  ```
- [`air`](https://github.com/air-verse/air) (optional), for live-reload
  during development only:
  ```bash
  go install github.com/air-verse/air@latest
  ```
- `curl`, used once by `make bulma`/`make htmx` (and so by `make build`/
  `make dev`, which depend on them) to vendor Bulma's CSS and htmx's JS
  into `web/static/`. Both files are already present in a fresh checkout
  of this repo, so this only matters after `make clean` or if they're
  ever removed — see [Building](#building).
- A populated shared inventory database at (or pointed to by) `-db-path`
  — media-site itself never populates one; see
  [How it works](#how-it-works).

## Building

```bash
make build   # vendors Bulma/htmx if missing, regenerates templates, builds ./bin/server
```

`build` depends on `bulma`, `htmx`, and `templ-gen`, in that order, so a
single `make build` is enough from a clean checkout:

| Target | What it does |
|---|---|
| `make bulma` | Downloads Bulma 1.0.4 to `web/static/css/bulma.min.css` via `curl` — skipped if the file already exists. |
| `make htmx` | Downloads htmx 2.0.3 to `web/static/js/htmx.min.js` via `curl` — skipped if the file already exists. |
| `make templ-gen` | Runs `templ generate`, regenerating every `*_templ.go` from its `.templ` source. |
| `make build` | `bulma htmx templ-gen`, then `go build -o bin/server ./cmd/server`. |
| `make test` | `go test ./...` |
| `make clean` | Removes `tmp/`, `bin/`, and the two vendored asset files `bulma`/`htmx` downloaded (so the next `make build`/`make dev` re-fetches them). |

You can also build directly with `go build -o <path> ./cmd/server` without
`make` — this works as-is against the checked-in `web/static` assets and
generated template files, as long as you haven't run `make clean` and
aren't editing a `.templ` file.

## Running

```bash
./bin/server -image-dir ~/Images -db-path ~/.config/inventory-manager/db/inv.db
```

Then open **http://localhost:8181** — the listen address is hardcoded to
`:8181` in `cmd/server/main.go`; there is no flag or environment variable
to change the port. If `-image-dir` is omitted it defaults to
`$HOME/Images` (created if it doesn't already exist); if `-db-path` is
omitted it defaults to the shared inventory database path (see
[The inventory database](#the-inventory-database)). The server binds its
listener synchronously before logging "server starting", so a port
already in use fails immediately with a clear error rather than hanging.
`Ctrl-C` (`SIGINT`) or `SIGTERM` triggers a graceful shutdown (up to a 10s
deadline) rather than dropping in-flight requests.

## Flags

| Flag | Default | Description |
|---|---|---|
| `-image-dir` | `$HOME/Images` | Directory containing the images to catalog. Canonicalized (symlinks resolved) once at startup; every request's `?dir=`/`?path=` is jailed to this resolved root. Created if it doesn't exist. |
| `-db-path` | `~/.config/inventory-manager/db/inv.db` (or `$INVENTORY_MANAGER_DB` if set) | Path to the shared inventory SQLite database — see [The inventory database](#the-inventory-database). |

Run `./bin/server -h` (or `go run ./cmd/server -h`) to see this from the
binary itself.

## Routes

| Method | Path | Handler | Description |
|---|---|---|---|
| `GET` | `/` | — | Redirects (`302 Found`) to `/catalog`. |
| `GET` | `/catalog?dir=` | `home` | The catalog page for directory `dir` (root if omitted/empty): breadcrumb, subdirectory dropdown, and the first `ImagePageSize` (96) images. Returns the full HTML page for a normal browser request, or just the `#catalog-content` fragment for an htmx request (`HX-Request: true`) — used by breadcrumb/flyout navigation to swap content in place. Query params from [the filter form](#the-flyout-filter-panel) (`tag`, `site`, `status`, `widthMin`/`widthMax`, `heightMin`/`heightMax`, `discoveredFrom`/`discoveredTo`) turn this into a recursive filtered search of `dir` and everything beneath it instead of a plain single-directory listing. `400` for a `dir` that escapes `-image-dir` or doesn't exist in the index. |
| `GET` | `/catalog/images/more?dir=&offset=&...filter` | `moreImages` | One more `ImagePageSize`-sized batch of image grid cells for `dir`, continuing from `offset`, with the same filter params as `/catalog` re-applied. Renders only the appended cells (and a fresh sentinel if more remain) — never the breadcrumb or directory listing. `400` for a non-numeric or negative `offset`. |
| `GET` | `/catalog/view?path=` | `view` | The full-size single-image viewer for `path` (relative to `-image-dir`). `404` if `path` isn't a catalogued image (including a directory, or a path that clamps to one via a traversal attempt). |
| `GET` | `/catalog/flyout?dir=&...filter` | `flyout` | The left-edge panel's HTML fragment for `dir` and everything beneath it: aggregated counts plus the filter form, pre-filled from whatever filter params are present. Lazy-loaded by `/static/js/flyout.js` the first time the panel opens for a given directory. |
| `GET`/`HEAD` | `/images/*` | `imageFileServer` | Serves an individual file under `-image-dir` as-is. `404` for a directory (including `-image-dir` itself) rather than a listing, and for a missing file. Opens the file once and stats that handle (not stat-then-open), closing a TOCTOU window between the directory check and serving. |
| `GET`/`HEAD` | `/thumbs/*` | `thumbFileServer` | Serves a cached thumbnail for the image at the matching path under `-image-dir`, generating one first via `invlib.ThumbCache.Generate` if it doesn't already exist. `404` if the source image is missing or isn't decodable. |
| `GET`/`HEAD` | `/static/*` | — | Serves the embedded `web/static` assets (`css/`, `js/`) via `http.FileServer`. |

## The flyout filter panel

Hovering the mouse into the left `12px` edge of any page slides out a
panel (`#flyout-panel`, `web/static/js/flyout.js`) whose content is
lazy-loaded over htmx the first time it opens for a given directory —
keyed off whichever on-page element currently carries a `data-dir`
attribute (the catalog page's `#catalog-content`, or the image viewer's
`#page-content`), so the same panel works unmodified on both pages.
Moving the mouse back past the panel's own width slides it closed; an
`htmx:afterSwap` on `#catalog-content` invalidates the cached directory key
so the next hover re-fetches fresh aggregates instead of a stale
directory's.

The panel shows, for the current directory and everything beneath it
(always computed over the *full*, unfiltered subtree, so the pickable
values below don't shrink along with an active filter):

- Total catalogued file count.
- Every distinct **tag** present, with a per-tag count, as a `<datalist>`
  behind a comma-separated text input.
- Every distinct **origin site**, with a per-site count.
- Every distinct **`files.status`** value, with a per-status count.
- The **width/height range** observed (shown as a label hint, e.g. `Width
  (320–4032)`), with min/max number inputs to narrow it.
- The **discovered-date range** observed (`origins.discovered_at`, shown
  `YYYY-MM-DD`), with `from`/`to` date inputs.

Clicking **Apply** submits the form as a `GET /catalog` (swapping
`#catalog-content` in place, URL pushed); **Clear** reloads the same
directory with no filter params. Selected tags are **AND**ed — a file must
carry every selected tag, not just one — and every other criterion (site,
status, width/height bounds, discovered date range) also combines with
AND. Once applied, the filter is re-encoded as query parameters
(`FileFilter.QueryValues`) and threaded through every subsequent infinite-
scroll continuation request, so "load more" keeps matching the same
criteria instead of reverting to an unfiltered listing.

## The inventory database

Resolved by `-db-path`, defaulting to `db.DefaultPath()` — the same
`~/.config/inventory-manager/db/inv.db` default (and the same
`$INVENTORY_MANAGER_DB` override) that `inv-lib`'s `invlib.DefaultPath`
resolves to for `image-browser`, `site-scraper`, and `inventory-manager`.
`internal/db.Open` applies this module's own embedded `schema.sql` (the
same six tables as `inv-lib`'s — `files`, `images`, `videos`, `audio`,
`media_probe`, `origins`, `tags`, `file_tags` — note `videos`/`audio`/
`media_probe` are defined but never populated or read by anything in this
repository), then its own `migrateFiles`/`migrateOrigins` column
migrations (renaming a legacy `blake3` column to `hash`, adding
`origin_id`/`last_verified_at`/`missing_since`/`status`/`error` to
`files` and `original_file`/`discovered_at` to `origins` if missing,
dropping superseded flat-schema columns), then `indexes.sql`. See
[How it works](#how-it-works) for why this is a separate, parallel
implementation of `inv-lib`'s schema rather than an import of it — if
`inv-lib`'s `schema.sql`/`indexes.sql`/migration logic ever changes, this
module's copies under `internal/db/` need the equivalent change by hand.

Only two tables are actually read by this server: `files` (joined to
`images` for anything the catalog shows — a file with no `images` row,
i.e. not recognized/decoded as an image, never appears anywhere in the
UI), and, for the flyout panel and filters, `origins`, `tags`/`file_tags`.
All queries scope by an absolute-path `LIKE 'prefix%'` against `files.path`
— there is no recursive directory walk at query time, just this one
prefix match per request.

```sql
sqlite3 ~/.config/inventory-manager/db/inv.db "select path, filesize, hash from files;"
```

## Development

```bash
make dev
```

Runs `templ generate --watch` alongside `air`, so editing a `.templ` file
or any `.go` file rebuilds and restarts the server automatically (`air`
watches `*.go` files per `.air.toml`'s `include_ext`, excluding `tmp/`,
`web/static/`, and `node_modules/`).

**`.air.toml` hardcodes an `-image-dir`** (`args_bin`) pointing at a
specific developer's own local photo directory — edit that path to a real
directory that exists on your machine before running `make dev`, or `air`
will build and start the server against a directory that isn't there.

## Testing

```bash
make test   # go test ./...
```

Automated tests exist for `cmd/server` (flag resolution, the `/images` and
`/thumbs` file servers — including the thumbnail-cache round-trip and a
path-traversal guard test), `internal/catalog` (page building, the
`view`/`moreImages` handlers, directory-prefix query helpers), and
`internal/store` (the flyout aggregate queries, filter clause building).
`internal/catalogmodel`, `internal/db`, `web/static`, and
`web/templates/...` have no test files of their own (`go test ./...`
reports `[no test files]` for each).

```bash
go test ./internal/catalog/... -run TestHandlerView -v   # a single package/test
golangci-lint run ./...                                  # optional; no repo-specific config file
```

## Security notes

- **Path jailing.** Every request path that reaches the filesystem or a
  database query is confined to the canonicalized (symlink-resolved)
  `-image-dir`: `internal/catalog/handlers.go`'s `resolveDir` for `?dir=`
  and `?path=`, and an equivalent `path.Clean("/"+...)` guard duplicated
  in `cmd/server/main.go`'s `imageFileServer`/`thumbFileServer` (which are
  wired up directly as `http.Handler`s, outside the catalog package). A
  `..` traversal attempt clamps to a path inside the root rather than
  escaping it or erroring loudly — verified by
  `TestImageFileServer`/`TestHandlerView`'s `../../../etc/passwd` cases,
  both of which assert `404`, never `200`.
- **No directory listings.** `/images/*` and `/thumbs/*` explicitly `404`
  on a directory (including the root) rather than falling back to
  `http.FileServer`'s default listing behavior — directory browsing is
  the catalog page's job, one level at a time, via the database index.
- **Parameterized SQL throughout.** Every query built from user input
  (`internal/store`, `internal/catalog`) uses bound `?` parameters, never
  string-interpolated values. The one `fmt.Sprintf`-built statement in the
  codebase (`internal/db/migrate.go`'s `PRAGMA table_info(%s)`) only ever
  interpolates one of this package's own fixed table-name constants, never
  request input.
- **TOCTOU-safe file serving.** Both file servers open the target path
  once and `Stat` that same handle, rather than stat-then-open, so the
  filesystem can't change between the directory check and serving the
  file.
- **No authentication.** There is no login, session, or access control of
  any kind — anyone who can reach the listening port can browse the
  entire indexed catalog and read any file under `-image-dir`. Treat this
  as a trusted-network/localhost tool, not an internet-facing one.

## Project layout

```
cmd/server/              entrypoint: flag parsing, router wiring, graceful shutdown, /images and /thumbs file servers
internal/catalog/        HTTP handlers (handlers.go), page/view-model building (page.go), filter parsing (filter.go)
internal/catalogmodel/   plain view-model types shared between internal/catalog and web/templates/catalog
internal/db/             this module's own embedded schema.sql/indexes.sql + column migration (parallel to inv-lib's)
internal/store/          read-only (in practice) query layer over the inventory database: listings, lookups, flyout aggregates, filtered search
web/templates/layout/    templ: shared page chrome (Base) — Bulma/htmx includes, the flyout panel's mount point
web/templates/catalog/   templ: Catalog, CatalogContent, Breadcrumb, ImageGrid/ImageGridItems, ImageView, Flyout
web/static/              embed.go (go:embed css js) + vendored Bulma CSS, htmx JS, app.css overrides, flyout.js
```

`inv-lib` (imported as `invlib`) is a required sibling checkout, not a
package inside this repository — see [Requirements](#requirements) and
[How it works](#how-it-works) for exactly what this module does (and
doesn't) use it for.
