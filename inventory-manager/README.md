# Inventory Manager

A native Go/Fyne image inventory browser: search and manage an inventory
database by metadata rather than by folder, with:

- File/Settings/About menu bar
- metadata filter bar (tag, source site) over a responsive thumbnail grid
  of matching inventory records (column count adapts to window width)
- File > Import Folder recursively scans a directory tree (via the same
  breadcrumb/type-ahead/hidden-file folder picker as before) and indexes
  every image found into the inventory database
- thumbnail disk cache under the OS user cache directory
- background thumbnail generation
- click thumbnail to open the full-size viewer, with previous/next and
  keyboard navigation, inline editing of a record's site/tags, and a
  Delete Record action (removes the inventory record only — the
  underlying file is left on disk)
- File > Manage Tags (or the toolbar button) opens a dialog to create new
  tags up front and delete existing ones, independent of any single
  record
- external "Open" support
- fullscreen mode
- JPEG, PNG, GIF, WebP, BMP, TIFF
- every imported image indexed by BLAKE3 content hash into a local SQLite
  inventory database, via the [inv-lib](../inv-lib) library, which owns
  the schema (`files`/`images`/`origins`/`tags`/`file_tags`), migration,
  and CRUD — this app only ever searches, edits, and deletes through it

## Build

    go mod tidy
    go build -o inventory-manager ./cmd/inventory-manager

This requires `inv-lib` checked out as a sibling directory (`../inv-lib`
relative to this repo) — go.mod's `replace inv-lib => ../inv-lib` points
at it directly, since it isn't published anywhere.

## Run

    ./inventory-manager

This opens the default inventory database (see below) with no filter
applied. Use File > Import Folder to index a directory's images into it,
and the tag/site fields in the toolbar to search it. To use a different
database file:

    ./inventory-manager -db /path/to/other.db

The thumbnail cache is automatically invalidated when the source image is
newer than the cached thumbnail.

The inventory database defaults to
`$XDG_CONFIG_HOME/inventory-manager/inventory.db` (or the platform
equivalent of `os.UserConfigDir()`) and is created, with inv-lib's schema,
on first run. Each imported image's path, BLAKE3 hash, size, and
dimensions are recorded — enough to detect duplicates by content
(`inv-lib`'s `ListFilesByHash`) ahead of future organize/dedupe features —
plus whatever site and tags you attach to it from the viewer. "Site" is
stored as a file's linked `origins` row rather than a flat column — this
app creates a lightweight `type: "manual"` origin the first time you set
a site on a record, and reuses it on later edits.
