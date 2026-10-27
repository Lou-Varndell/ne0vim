# Image Browser

A Fyne-based desktop tool for browsing, searching, organizing, and de-duplicating
large collections of images on disk. It ships as a single binary with twelve
subcommands: `browse`, `organize`, `move`, `view`, `count`, `size`, `process`,
`scan`, `import`, `reconcile`, `shard`, and `warm-thumbnails`.

Two of the subcommands (`view` and `size`) always open a graphical window
built on [Fyne](https://fyne.io/); `move` optionally does too, when given
`-preview`. `browse` opens an interactive terminal UI instead — a
keyboard-driven, SSH/headless-friendly alternative to those GUI windows.
`organize`, `count`, `process`, `scan`, `import`, `reconcile`, `shard`, and
`warm-thumbnails` are plain CLI tools that print to stdout/stderr and never
open a window.

## Table of Contents

- [Building](#building)
- [Running](#running)
- [Recognized image extensions](#recognized-image-extensions)
- [Subcommands](#subcommands)
  - [`browse`](#browse)
  - [`view`](#view)
  - [`organize`](#organize)
  - [`move`](#move)
  - [`count`](#count)
  - [`size`](#size)
  - [`process`](#process)
  - [`scan`](#scan)
  - [`import`](#import)
  - [`reconcile`](#reconcile)
  - [`shard`](#shard)
  - [`warm-thumbnails`](#warm-thumbnails)
- [The inventory database](#the-inventory-database)
- [Viewer UI Reference](#viewer-ui-reference)
- [Testing](#testing)
- [Packaging](#packaging)
- [Project Layout](#project-layout)

## Building

Requires Go 1.27.1 (the version pinned in `go.mod`) and, for the GUI
subcommands, the platform dependencies Fyne needs to compile CGO/OpenGL
bindings — on macOS this is just Xcode command line tools; see the
[Fyne getting-started docs](https://docs.fyne.io/started/) for Linux/Windows
requirements.

**`inv-lib` must be checked out as a sibling directory.** This repo depends
on a local, unpublished Go module, `inv-lib`, that owns the inventory
database's schema and CRUD, plus the shared on-disk thumbnail cache (see
[The inventory database](#the-inventory-database) and
[Viewer UI Reference](#viewer-ui-reference) below). `go.mod` points at it
with `replace inv-lib => ../inv-lib`, so it must exist at `../inv-lib`
relative to this repo's own directory — e.g. if this repo is checked out at
`~/src/image-browser`, `inv-lib` must be at `~/src/inv-lib` — before `go
build`, `go run`, or `go test` will work.

```bash
make build
```

This runs `go build -o $(BINARY) ./cmd/image-browser`, where `BINARY`
defaults to `~/.local/bin/image-browser` (edit the `BINARY` variable at the
top of the `Makefile` to install elsewhere). Remove it with `make clean`.

You can also build/run directly with `go`, which puts the binary wherever
you point `-o` instead:

```bash
go build -o image-browser ./cmd/image-browser
go run ./cmd/image-browser <command> [args]
```

Other `Makefile` targets: `make fmt` (gofmt every `.go` file in the repo,
found via `fd`), `make tidy` (`go mod tidy`), `make vet` (`go vet ./...`),
`make staticcheck` (requires the [`staticcheck`](https://staticcheck.io/)
binary on `PATH`), `make golangci-lint` (requires
[`golangci-lint`](https://golangci-lint.run/)), `make test` (see
[Testing](#testing)), `make package` (see [Packaging](#packaging)), and
`make all` (`fmt`, `tidy`, `test`, then `build`, in that order).

## Running

```
image-browser <command> [args]
```

| Command          | Purpose                                                                 |
|------------------|--------------------------------------------------------------------------|
| `browse`         | Interactively browse, select, and move database records in a terminal UI. |
| `organize`       | Find images matching a pattern and move them to a destination.         |
| `move`           | Find database file records matching a field filter and move their files. |
| `view`           | Browse a folder, or find images matching a pattern and review them.    |
| `count`          | Count images recursively in each immediate subdirectory of a root.     |
| `size`           | Group database images by exact resolution and review them in batches.  |
| `process`        | Decode dimensions for every database file record missing an images row. |
| `scan`           | Recursively scan a directory for files to add to the inventory database. |
| `import`         | Import scraper-manifest provenance and available image dimensions into the database. |
| `reconcile`      | Reconcile the database against disk under a root: new/changed/missing files, stale manifest.json entries. |
| `shard`          | Move database-tracked files not already under a root's shard directories into one, keeping the database in sync. |
| `warm-thumbnails`| Eagerly generate every missing thumbnail in the shared on-disk cache.   |

Run `image-browser help` (or with no arguments) for the top-level usage
summary, and `image-browser <command> -h` for a command's flag reference.

Flags use Go's standard single-dash `flag` package syntax (e.g. `-root`, not
`--root`, though `--root` is also accepted since Go's flag package treats one
or two leading dashes identically).

## Recognized image extensions

`images.Extensions` (`internal/images`) is the single source of truth for
"what's an image": `.jpg` `.jpeg` `.png` `.gif` `.bmp` `.tif` `.tiff`
`.webp`. Every command that matches, finds, or counts images is defined in
terms of it — `view`, `organize`, `move -preview`'s viewer, the
folder-browse/preview-subdirectories listing, the folder picker's
per-folder "(N images)" count, `reconcile` (via `images.IsImage`), and
`scan`'s own `pattern` variable (`cmd/image-browser/main.go`), which is
built directly from `images.Extensions` rather than maintaining a separate
list that can drift out of sync with it.

`count`'s own `imageExtensions` map (`internal/count`) is one deliberate
exception: it layers two extra extensions — `.avif` and `.heic` — on top of
`images.Extensions`, so `count`'s tallies for a folder can be higher than
what `view`/`organize`/`move`/`scan` would actually operate on in that same
folder. No decoder is registered for either format anywhere in this binary,
so a `.heic`/`.avif` file is counted but will never be found, opened, moved,
or scanned by anything else in this program.

`process`'s own `isImageFile` check (`internal/process`) is the other
exception, in the opposite direction: it recognizes only `.gif` `.jpeg`
`.jpg` `.png` `.webp` — a narrower set than `images.Extensions`, missing
`.bmp`/`.tif`/`.tiff`. This only matters for a `.bmp`/`.tif`/`.tiff` file
that has a `files` row but no `images` row yet (the `MissingImage` case
`process` backfills): `process` will count it under "Non-image files
skipped" and never attempt to decode it, even though `images.Decode` (the
same decoder `scan` already used successfully to give it dimensions in the
first place) handles that format fine. In practice this gap is narrow —
`scan` and `reconcile` both decode dimensions for every image extension
(including bmp/tif/tiff) themselves at discovery time, so a file only ever
reaches `process`'s backfill queue without dimensions if it failed to
decode back then (renamed/corrupted) or was added by `import` (which never
decodes). See [`process`](#process) below.

Image decoding itself (full-size display, thumbnail generation, and the
width/height/format/color-space recorded in the database) is handled by
Go's standard `image.Decode`/`image.DecodeConfig`. gif/jpeg/png are
supported directly by the standard library; webp is registered by a blank
import in `internal/images` itself; bmp and tiff are registered by blank
imports in `internal/viewer` instead (`golang.org/x/image/{bmp,tiff}`).
Because Go's image-format registry is process-global rather than
package-scoped, and `cmd/image-browser` links `internal/viewer` into the
same binary as every other command, bmp/tiff decoding works everywhere in
this program — `scan`, `process`, `reconcile` included — even though those
packages never import the bmp/tiff codecs directly themselves. So every
extension in `images.Extensions` above decodes successfully in practice,
subject to the `process`-specific caveat above.

## Subcommands

### `browse`

```
image-browser browse [flags]

Flags:
  -root string   scope the browser to files under this directory (default: ~/Images)
```

An interactive terminal UI (built on [Bubble Tea](https://github.com/charmbracelet/bubbletea))
for browsing and bulk-moving database records without opening a GUI window —
the tool of choice over SSH or in any terminal-only session. Requires an
interactive terminal on both stdin and stdout; redirecting either one exits
immediately with an error (`browse: requires an interactive terminal
(stdin/stdout must not be redirected)`) instead of hanging.

Before showing anything, `browse` removes any database row whose file no
longer exists on disk (the same `invdb.PruneMissing` upkeep `move`/`size`
do at startup — see [`move`](#move)'s own description of it), then loads
every record under `-root` that has a decoded `images` row and isn't marked
missing (`FileQuery{PathPrefix: root, RequireImage: true, ExcludeMissing:
true, WithTags: true}`) — the same structural scoping inventory-manager's
own browser uses. A file `scan` recorded but `process` hasn't decoded yet
simply won't appear here until it has an `images` row.

Shows the loaded records as a scrollable, filterable list, one line per
record (a `[ ]`/`[x]` selection marker plus the filename, then a second line
with dimensions, origin site, and containing directory):

- `↑`/`↓` or `j`/`k` — move the cursor (and the list's other built-in
  bindings, e.g. `pgup`/`pgdn`/`g`/`G` for paging/jumping)
- `/` — fuzzy-filter by path, origin site, or tag; while filtering, every
  other keystroke belongs to the filter box, not the bindings below
- `space` — toggle the current record for a move
- `enter` — view the record's full metadata (path, size, MIME, hash,
  status/error, scanned time, then — if present — dimensions/format/color,
  origin type/site/URL/identifier/original-file/discovered time, and tags)
  in a scrollable detail screen; `esc`/`q` goes back to the list from here
  (it does **not** quit the program from this screen)
- `m` — (only if at least one record is selected) opens a destination
  prompt; `enter` confirms and moves every selected record's file via
  `move.PerformMoves` — the same resolve-destination/duplicate-detection/
  move/update-database logic [`move`](#move)'s own `-preview` flow uses —
  `esc` cancels back to the list without moving anything
- `q`/`esc` — **on the list screen**, quits the program (the list widget's
  own default binding; distinct from the same keys' "go back" meaning on
  the detail screen above)
- `ctrl+c` — quits immediately, from any screen, canceling any in-flight
  database/filesystem work

After a move finishes, a summary screen reports how many files moved,
how many were skipped as duplicates, and any failures; pressing any key
returns to the (reloaded) list.

Example:

```bash
# Browse everything under ~/Images
image-browser browse

# Scope the browser to one subtree
image-browser browse -root ~/Images/vacation
```

### `view`

```
image-browser view [pattern] [flags]

Flags:
  -root string       root directory to search (default: ~/Images)
  -move-all string   enable Move All and move matches to destination
```

Opens the GUI image browser. Behavior depends on whether a search `pattern`
is given:

- **No pattern, no `-move-all`** — opens a plain folder browser rooted at
  `-root` (or `~/Images` if omitted): a flat thumbnail grid of the images
  directly inside that folder, with a toolbar to type/choose a different
  directory, refresh, or toggle a recursive "preview subdirectories" mode
  that groups results by subfolder.
- **Pattern given** — recursively searches `-root` for image files whose
  path contains `pattern` (case-insensitive substring match), then opens the
  matches in the review browser (grouped-by-directory thumbnail grid,
  Continue/Trash Batch/Quit toolbar, no destination move unless `-move-all`
  is set).
- **`-move-all` given** (with or without a pattern) — same search/review flow,
  but adds a "Move All" button that moves every currently-displayed image to
  the given destination, after a confirmation dialog. Duplicate detection
  (by file size + content hash, not filename) skips files that already exist
  in the destination. The inventory database (see
  [The inventory database](#the-inventory-database) below) is the single
  source of truth for where a file lives — there is no sidecar
  `manifest.json` to keep in sync anymore. Each moved file's `files` row has
  its `path`/`filename` rewritten in place to the new location; a moved file
  with no existing row yet (never scanned or imported) gets one created so
  the move isn't lost from the database's point of view either way.

  Searching by filesystem path is the right fit when you don't want to
  filter by anything other than where a file lives. If you want to filter on
  a database field instead — site, status, hash, resolution, discovery date
  — see [`move`](#move) below, which queries the database directly rather
  than walking the filesystem.

A search-driven `view` also wires its window's **File → Refresh** menu item
to redo the same search and reload — useful if something external
added/removed/renamed files under `-root` since the window opened. The
plain folder browser (no pattern) wires Refresh to redo its own directory
listing instead; see [Viewer UI Reference](#viewer-ui-reference) for the
rest of the window's menu bar and toolbars.

Examples:

```bash
# Browse ~/Images with the folder browser (no search)
image-browser view

# Browse a specific folder
image-browser view -root ~/Pictures/Vacation

# Search ~/Images for "sunset" and review matches
image-browser view sunset

# Search a custom root, and enable bulk-move to a destination
image-browser view -root ~/Images -move-all ~/Images/Keepers sunset
```

### `organize`

```
image-browser organize [pattern] [flags]

Flags:
  -root string   root directory to search (default: ~/Images)
  -dest string   destination directory
  -execute       actually move files
```

CLI-only equivalent of `view -move-all`, with no GUI: recursively searches
`-root` for images whose path contains `pattern`, then moves every match into
`-dest`. Duplicate files already present in `-dest` (matched by size + BLAKE3
content hash, not filename) are skipped and reported as `skip (duplicate)`.
Non-duplicate filename collisions are resolved by appending `-2`, `-3`, etc.
to the destination filename.

`-execute` and `-dest` are both required — running `organize` without them is
a deliberate safety guard so you can't accidentally move files with a bare
invocation (`organize` without `-execute` fails with `organize requires
--execute`; with `-execute` but no `-dest` it fails with `organize --execute
requires --dest`).

`organize` works purely against the filesystem and does not touch the
inventory database at all — use [`move`](#move) instead if you want to move
files based on a database field (site, hash, status, resolution, …) rather
than a path pattern.

Example:

```bash
image-browser organize -dest ~/Images/Sorted/screenshots -execute screenshot
```

Output shows each move (`src -> dest`, both printed relative to `-root` when
possible) or `skip (duplicate)` for files already present at the destination.
Exits non-zero if any file failed to move, but continues attempting the rest
of the batch first.

### `move`

```
image-browser move [value] [flags]

Flags:
  -root string           scope matches to files under this directory (default: ~/Images)
  -site string           match files whose site contains value (case-insensitive)
  -sourceurl string      match files whose source_url contains value (case-insensitive)
  -file string           match files whose path contains value (case-insensitive; not limited to the default -root)
  -hash string           match files with exactly this hash
  -status string         match files with exactly this status
  -error string          match files whose error contains value (case-insensitive)
  -original string       match files whose original_file contains value (case-insensitive)
  -discoveredat string   match files discovered on this date (YYYY-MM-DD)
  -width int             match files with exactly this width
  -height int            match files with exactly this height
  -dest string           destination directory (default: derived from the match value)
  -preview               preview matches in the viewer before moving, with the option to change destination
```

Unlike `organize`, `move` doesn't search the filesystem for images that match
a path pattern — it filters **the inventory database's file records** by
field (see [The inventory database](#the-inventory-database) below) and moves
the file each matching record points at. A bare `[value]` is shorthand for
`-site`; giving both is an error. `-site`, `-sourceurl`, `-original`, and
`-error` match a case-insensitive substring against the matching record's
origin (where available — a file with no `origin_id`, i.e. never seen by
`import`, simply never matches any of these); `-file` matches a
case-insensitive substring against the file's path; `-hash` and `-status`
require an exact (case-insensitive) match against the file's own `hash`/
`status` columns; `-width`/`-height` require an exact integer match against
the file's `images` row, if it has one; and `-discoveredat` matches the
calendar date portion of the origin's `discovered_at` only (time of day is
ignored). `-file`, unlike the other filters, is not limited to the `~/Images` default
`-root`, so it finds files stored anywhere in the database; pass `-root`
explicitly to narrow it. At least one filter is required. Multiple flags combine with AND —
e.g. `-status kept -width 1024` only matches records that are both.

**Scope.** `-root` (default `~/Images`, same convention as
`organize`/`view`/`count`) restricts matches to files whose path is at or
under this directory — a prefix match against the database's `path` column,
not a filesystem walk. Every file in the database is a candidate regardless
of where it was originally scanned, imported, or recorded from; `-root` just
narrows which of them are eligible to match.

Before matching, `move` removes any database row whose file no longer exists
on disk (see `invdb.PruneMissing`), so a stale record can never match and
"move" nothing. Unlike [`reconcile`](#reconcile)'s own handling of missing
files, this is a hard deletion with no history kept — `move`, `size`, and
`browse` all do this at startup as routine upkeep, not as a reconciliation
step.

**Destination.** `-dest` sets it explicitly. Without `-dest`, the destination
directory name is derived from whichever filter values you gave (joined with
`-`, in flag order, with characters that would otherwise create unintended
nested paths or an invalid name — `/`, `:`, `*`, etc. — replaced), e.g. `move
bluebird` creates/uses `./bluebird`, and `move -status kept -width 1024`
creates/uses `./kept-1024`. The directory is created if it doesn't exist.

**Moving and duplicate detection.** Each matching file is moved with the same
size-then-content-hash duplicate check `organize` and `view -move-all` use: a
file already present in the destination with the same size and BLAKE3 hash
is left in place and reported as a duplicate; a filename collision with
different content gets a `-2`, `-3`, … suffix instead.

**Database updates.** Each moved file's `files` row has its `path` and
`filename` rewritten in place to the new location (`invlib.UpdateFilePath`
via the exported `move.PerformMoves` helper — also reused by `browse`'s own
move action, see [`browse`](#browse) above). The database is the single
source of truth for where a file lives — there is no manifest.json for
`move` to read, write, or keep in sync.

**`-preview`.** Instead of moving immediately, opens the GUI viewer on every
matched image with the same Move All / Change Destination / Quit controls
`view -move-all` has (see [Viewer UI Reference](#viewer-ui-reference)) —
look over the batch, optionally redirect to a different folder, and only
commit the move once you click Move All and confirm. Clicking Move All more
than once (after a partial failure, or after changing the destination) is
always safe: a record that already moved is simply found sitting in the new
location and reported as a duplicate of itself rather than moved or
duplicated again, and changing the destination after some files already
moved relocates them again to the new folder.

Examples:

```bash
# Move every record whose site mentions "bluebird" under ~/Images into ./bluebird
image-browser move bluebird

# Same, but scope to a specific subtree and pick the destination explicitly
image-browser move -site bluebird -root ~/Images/wildlife -dest ~/Images/Keepers/bluebird

# Move every "kept" record at exactly 1024x768 into ./kept-1024
image-browser move -status kept -width 1024 -height 768

# Move one specific file by its content hash
image-browser move -hash f5c44044dc00d6dc11b901ec644f6880ddd706628e8bac3684fcc85862001ebd

# Preview matches before moving, with the option to change the destination
image-browser move bluebird -preview
```

### `count`

```
image-browser count [flags]

Flags:
  -root string          root directory to search (default: current directory)
  -image-count string   filter by image count: N for exactly N, or N,M for an inclusive range
  -no-subdirs           only include directories that contain no subdirectories
```

Lists every immediate subdirectory of `-root`, one line each, with the count
of images found anywhere in that subdirectory's tree (recursively):

```
00042 /Users/you/Images/Vacation2019
00003 /Users/you/Images/Screenshots
```

Useful for spotting sparse or oversized folders before running `organize` or
`view` against them. `count` works purely against the filesystem and does
not touch the inventory database. A subdirectory it can't read (permission
denied, broken symlink, etc.) is skipped rather than aborting the whole
count, the same convention `images.Find`/`scan` use.

Examples:

```bash
# Count images in every subfolder of the current directory
image-browser count

# Count images under a specific root
image-browser count -root ~/Images

# Only show folders with exactly 0 images
image-browser count -image-count 0

# Only show folders with 1 to 5 images
image-browser count -image-count 1,5

# Only show leaf folders (no subdirectories) with fewer than 10 images
image-browser count -image-count 0,9 -no-subdirs
```

### `size`

```
image-browser size [flags] [root]

Flags:
  -largest         show largest resolutions first
  -smallest        show smallest resolutions first (default)
  -batch-size int  maximum files per preview batch (default 40)
  -trash           allow moving a reviewed batch to Trash
```

Groups every file in the inventory database that has a decoded `images` row
(i.e. non-null width/height — see [The inventory database](#the-inventory-database)
below) by exact `width x height`, and opens the GUI viewer to walk through
each resolution group in batches of `-batch-size` files. The optional
positional `root` scopes grouping to files whose path is at or under that
directory; with no `root`, every processed image in the database is
considered. `-largest`/`-smallest` are mutually exclusive. This is meant for
triaging a collection that has a lot of thumbnail/placeholder-sized
duplicates: sort smallest-first (default) to review and trash them quickly,
or largest-first to check the best copies.

Before grouping, `size` removes any database row whose file no longer exists
on disk (see `invdb.PruneMissing`, the same upkeep `move`/`browse` do at
startup), so a stale record never shows up in a group.

Each group is printed (number, dimensions, pixel count, file count, batch
count) and shown in its own review pass, batch by batch, with the batch's
own header (file range) also printed to stdout before the viewer opens.
Batches within a group use the viewer's Continue button to move to the next
batch, Trash Batch (only enabled with `-trash`; sends every file in the
current batch to the system Trash before continuing), or Quit (stops the
whole `size` run immediately, including any remaining groups and batches).
Quitting prints which group/batch it stopped at; finishing every group
prints `Done.`.

Example:

```bash
# Review the smallest-resolution duplicates first, 25 per batch, allowing trash
image-browser size -smallest -batch-size 25 -trash

# Same, scoped to one subtree
image-browser size -smallest ~/Images/Vacation2019
```

### `process`

```
image-browser process [flags]

Flags:
  -root string      only process files under this directory (default: every unprocessed file in the database)
  -workers int       number of concurrent image-decode workers (default 8)
```

A database-native backfill: finds every `files` row that doesn't have an
`images` row yet (i.e. was recorded by `scan`, `import`, or `reconcile` but
never successfully decoded — a file matched by extension but actually
corrupt or renamed at scan time, say), optionally scoped to `-root` (a path
prefix match, same convention as every other root-accepting command), and
decodes each one with a pool of `-workers` concurrent goroutines
(`-workers` must be at least 1).

Only files whose extension is one of `.gif`, `.jpeg`/`.jpg`, `.png`, `.webp`
are attempted — this list is narrower than
[the extensions every other command recognizes](#recognized-image-extensions),
missing `.bmp`/`.tif`/`.tiff`; everything else without an `images` row
(including a bmp/tif/tiff file) is counted as "non-image" and skipped. For
each attempted file:

- **Decoded successfully** — a row is inserted into `images` with its
  width/height/format/color-space, and the file's `status` column is set to
  `processed`.
- **Failed to decode** — `status` is set to `failed` and `error` to the
  decode error's message.

`Ctrl-C`/`SIGINT` is handled gracefully: in-flight decodes finish, no more
new ones start, and the command exits. Nothing is lost either way — unlike
an older, manifest-file-based version of this command, each file's result is
written straight to the database as soon as it's decoded, not batched up and
written out at the end, so there's no separate checkpoint flag or partial
output file to manage.

Example:

```bash
# Backfill every unprocessed file in the database, 16 workers
image-browser process -workers 16

# Only backfill files under one subtree
image-browser process -root ~/Images/Vacation2019
```

### `scan`

```
image-browser scan -root <dir>

Flags:
  -root string   root directory to scan recursively (required)
```

Walks `-root` recursively and records every file whose name matches a glob
pattern (shell syntax, e.g. `*.jpg`; the match is case-insensitive) into the
shared inventory database — see
[The inventory database](#the-inventory-database) below.

The patterns to match are currently a package-level variable in
`cmd/image-browser/main.go` (`var pattern = patternsFor(images.Extensions)`,
one `"*"+ext` glob per entry in [`images.Extensions`](#recognized-image-extensions)),
passed down to the `scan` package rather than exposed as a flag — edit
`images.Extensions` (or `patternsFor`'s input) and rebuild to scan for
other file types. The default list covers every format `scan` can actually
decode via `images.Decode` (including bmp/tif/tiff — unlike `process`, see
[Recognized image extensions](#recognized-image-extensions)); a pattern
added without matching decode support still gets a `files` row, it just
won't get an `images` row.

For every match, its absolute path, filename, extension, size, modification
time, and BLAKE3 content hash (the same hashing `organize`/`move`/`view
-move-all`/`process` already use for duplicate detection) are inserted via
`invlib.EnsureFile`. Re-running `scan` against the same root is safe: `path`
is unique in `inv.db`, so an already-recorded file's row is left completely
untouched rather than refreshed or duplicated — `EnsureFile` only ever
inserts a brand-new row or reports an existing one's id, it never updates
size/mtime/hash for a file whose content has since changed on disk. (That
refresh is what [`reconcile`](#reconcile) is for.) A subdirectory that can't
be read (permission denied, broken symlink, etc.) is skipped rather than
aborting the whole walk; a bad `-root` itself still fails.

**Image metadata.** Every matched file is also opened and decoded with
`image.DecodeConfig` (gif, jpeg, and png via blank-imported stdlib decoders;
webp, bmp, and tiff via `golang.org/x/image/{webp,bmp,tiff}`, blank-imported
across `internal/images`/`internal/viewer` — see
[Recognized image extensions](#recognized-image-extensions)) — a
header-only decode that reads just enough to report dimensions, without
decoding full pixel data. The result
(width, height, format, and a short name for the color model, e.g. `rgba`,
`ycbcr`, `cmyk`, or `palette` for a paletted GIF/PNG) is inserted as a row in
`images`, keyed by the `files` row's id, via `invlib.InsertImageIfAbsent`. A
file that matched a pattern but isn't actually decodable (wrong extension,
corrupt data) still keeps its `files` row; decoding just prints a warning to
stderr and moves on without an `images` row for it — a later `process` run
will retry (subject to `process`'s own narrower extension list above).
Because `EnsureFile` always returns the real file id even for an
already-recorded path, re-running `scan` over a root it already recorded
still backfills `images` rows for any file that was added to `files` before
this feature existed, or by a run that failed partway through decoding it.

Example:

```bash
image-browser scan -root ~/Images
```

### `import`

```
image-browser import [flags]

Flags:
  -root string   manifest file, or directory to search recursively for manifest files (default: ~/Images)
```

Imports provenance from `*manifest.json` files written by the separate
`site-scraper` tool (not part of this repository, and not a build
dependency of it — only its manifest.json output format matters here) into
the shared inventory database. It
does not download or move images, and it never writes changes back to the
manifest files — `import` is read-only with respect to manifest.json; see
[`reconcile`](#reconcile) below for the one command that does write back to
it (and only to prune, never to add or rewrite).

**Finding manifests.** With `-root` set to a manifest file, only that file is
read. With `-root` set to a directory, the command searches recursively for
files matching `*manifest.json`. If `-root` is omitted, it defaults to
`~/Images`. A manifest that cannot be read or parsed is reported to stderr
and skipped so processing can continue with other manifests. If no manifests
are found, the command prints a message and exits successfully. Positional
arguments are not accepted.

**What gets imported.** For each manifest entry, `import` resolves `file`
relative to the manifest's directory when it is not an absolute path. It
requires the path to remain inside that directory tree and the file to exist
on disk. Entries with no file path, paths outside the manifest directory, or
missing files are skipped and counted in the summary. Valid files are ensured
in the inventory's `files` table with their path, filename, extension, size,
modification time, and manifest hash when present. The command also records
web-origin provenance when available, as a row in `origins`: `site`,
`source_url`, `original_file`, and `discovered_at`.

If an entry has `status: "processed"` and positive `width` and `height`, those
dimensions are added to the image metadata only if an image row does not
already exist. Import does not overwrite the file's processing `status` or
`error` fields; those describe decode outcomes and are set by `process` (and
cleared by `reconcile` when a file's content changes).

The database path and connection are resolved through the local Go dependency
`inv-lib` (`invlib.DefaultPath` and `invlib.Open`). That library is a local
workstation dependency rather than a package defined inside this repository,
so its database-path configuration is managed by that library.

At the end, the command prints counts for manifests scanned, entries imported,
and entries skipped because of a missing file path, a path outside the
manifest's directory, or a file missing from disk. Database errors for
individual records are reported to stderr; the command continues processing
other entries.

Examples:

```bash
# Import provenance from manifests under ~/Images (the default root)
image-browser import

# Search a specific image collection recursively
image-browser import -root ~/Images

# Import one specific scraper manifest
image-browser import -root ~/Images/collection/manifest.json
```

#### Manifest format

`import` reads, and `reconcile` prunes, a JSON array of records shaped like:

```json
[
  {
    "site": "example.com",
    "source_url": "https://example.com/photo.jpg",
    "file": "/Users/you/Images/photo.jpg",
    "hash": "…",
    "status": "processed",
    "error": "",
    "original_file": "photo-original.jpg",
    "discovered_at": "2026-01-01T00:00:00Z",
    "width": 1920,
    "height": 1080
  }
]
```

`site`, `source_url`, `status`, and `discovered_at` are always present;
`file`, `hash`, `error`, `original_file`, `width`, and `height` are omitted
when empty/zero. `file` is usually an absolute path, but a manifest
co-located with the images it describes (one per directory) may instead
store a bare filename, resolved relative to the manifest's own directory.

A single JSON object with the same fields, rather than an array, is also
accepted wherever `import` or `reconcile` look for a manifest, for a
directory tracking exactly one entry. An object with the fields wrapped in
an `{"entries": [...]}` key is accepted too — this is the format the
`site-scraper` tool always writes (its `manifest.Manifest` type is
`{Entries []Entry}`, never a bare array).

**Directory scope.** A manifest only ever describes images in its own
directory or a subdirectory of it — never a file that lives elsewhere, even
if some other process once wrote `file` as an absolute path pointing outside
that tree. `import` enforces this when reading (an out-of-tree or missing-file
entry is skipped, not imported); `reconcile` enforces it when pruning (such an
entry is dropped from the manifest file on disk, with `-apply`). Neither
command cares about an entry's `status` value beyond `import`'s own
`"processed"` check above — a manifest carrying `site-scraper`'s own status
conventions (`kept`, `duplicate-of:<hash>`, `download-failed:<error>`) is
handled exactly the same as one using `missing`/`processed`/`failed`/etc.

### `reconcile`

```
image-browser reconcile -root <dir> [-apply]

Flags:
  -root string   root directory to reconcile against the database (required)
  -apply         write the detected changes to the database (default: report only)
```

Compares what the inventory database has recorded under `-root` against
what's actually on disk, and brings the database back in line — without
losing history. Unlike `scan` (which only ever adds new files and never
revisits a path it already knows) or `invdb.PruneMissing` (which `move`,
`size`, and `browse` use at startup and which deletes a database row
outright the moment its file is gone), `reconcile` tracks a file's
lifecycle over time: it notices when a file disappears, reappears, or
changes, and reflects that in the database without ever deleting a `files`
row itself.

**Report-only by default.** A plain `reconcile -root <dir>` only prints what
it found; nothing is written to the database or to any manifest file until
you pass `-apply`. Run it without `-apply` first to see what a real pass
would do.

**What it detects**, for every file under `-root`:

| Situation                                              | With `-apply`                                                        |
|----------------------------------------------------------|-----------------------------------------------------------------------|
| On disk, no matching database row                      | A new `files` row is inserted (hashed, and decoded into `images` if it's a recognized image type) — the same treatment `scan` gives a new file. |
| On disk, matches its database row, unchanged            | `last_verified_at` and `scanned_at` are refreshed; nothing else changes. |
| On disk, matches its database row, size or mtime differs | `filesize`/`modified_at`/`hash` are refreshed, any prior `status`/`error` is cleared, and the file's `images`/`videos`/`audio`/`media_probe` rows are deleted so a later `process` run (or a future ffprobe effort) regenerates them from the new content. |
| In the database, not found on disk                      | `missing_since` is set (only the first time — an already-missing file's original `missing_since` is never reset, so you can always tell how long it's actually been gone). |
| In the database, was missing, now found on disk again   | `missing_since` is cleared. (It's independently also re-checked for a content change, per the row above — a file that was deleted and later restored from a backup is a very normal way to end up with the same bytes but a different mtime.) |

"Changed" is decided by `hasChanged`: size or modification time differing
from what the database has on record counts as changed — even a bare
`touch` that only bumps mtime, since this errs toward an extra re-hash
rather than risking a real edit sitting undetected with stale dimensions. A
database row with no `modified_at` recorded at all (e.g. one attached only
via `import`, which never sets it) is always treated as changed.

A `files` row is never deleted by `reconcile` — only `missing_since` is set
or cleared. The `images`/`videos`/`audio`/`media_probe` rows deleted for a
changed file are deleted individually (`invlib.DeleteImage`/`DeleteVideo`/
`DeleteAudio`/`DeleteMediaProbe`); the `files` row itself, its `origins` link,
and any `file_tags` are left completely alone.

**Scan errors.** A directory `reconcile` can't read (permission denied,
unmounted volume, broken symlink, …) is excluded from the missing-files
determination entirely, along with every database record under it — it is
*not* treated as an empty directory, which would otherwise make every file
recorded under it look deleted. This is reported separately as a scan error
rather than folded into the missing-files count.

**Scope.** Both the filesystem walk and the database query are scoped to
`-root`: a reconcile limited to one subtree only ever touches database
records whose path is at or under that subtree (a path-prefix match, with a
trailing separator so a sibling directory that merely shares `-root` as a
string prefix, e.g. `root` vs. `root2`, is never pulled in by mistake).

**Manifest.json cleanup.** `manifest.json` is being phased out in favor of
the database, so every `*manifest.json` file found under `-root` (the same
recursive search `import` does) also gets pruned: any entry whose `file`
no longer exists on disk, or resolves outside the manifest's own directory,
is dropped (reusing `import`'s own enforcement — see
[Manifest format](#manifest-format) above), and the file is rewritten in
whatever shape it was read in. This never adds or rewrites an entry, only
removes stale ones, and only with `-apply`.

Example:

```bash
# See what a real pass would do, without changing anything
image-browser reconcile -root ~/Images

# Apply it
image-browser reconcile -root ~/Images -apply

# Reconcile just one subtree — records elsewhere in the database are untouched
image-browser reconcile -root ~/Images/20260826-100931 -apply
```

Sample report:

```
Reconciliation summary
----------------------
Files examined:        12450
Unchanged:             11980
Changed:                 35
New files:               400
Missing files:            35
Reappeared files:          2
Scan errors:                0
Manifests checked:          3
Manifest entries removed:   7

Changes applied: no (pass -apply to write these changes)
```

### `shard`

```
image-browser shard [flags]

Flags:
  -root string       sharded root directory to distribute files under (default: ~/Images)
  -capacity int       files placed in -root's first shard directory before starting a new one (default 4096)
  -apply              move files and update the database (default: report only)
```

Consolidates every file the database knows about into numbered shard
subdirectories of `-root` (e.g. `-root/0000000000/photo.jpg`), via the
shared `invlib.Sharder` — the same mechanism `site-scraper` uses for its
own staging directory. This keeps `-root` from ending up with one giant
flat directory as a collection grows, regardless of whether its files
arrived via `scan`, `import`, `move`, or years of mixed tooling.

**What counts as "already sharded."** A file is left alone only if its path
is already a direct child of one of `-root`'s shard directories (a shard
directory is identified purely by its name being all-digits, directly under
`-root` — see `invlib.IsSharded`). Everything else — including a file
sitting loose directly in `-root`, and any file tracked anywhere else on
disk — is a candidate to move.

**Report-only by default.** A plain `shard -root <dir>` only lists which
files would move (one path per line) and how many are already correctly
sharded; nothing is touched until you pass `-apply`. Before listing
candidates, it also prunes any database row whose file no longer exists
(the same `invdb.PruneMissing` upkeep `move`/`size`/`browse` do at
startup).

**Capacity and growth.** `-capacity` (default `4096`, matching
`invlib.DefaultShardCapacity`) sets how many files the first shard
directory holds; each later shard doubles the capacity of the one before
it (second shard 8192, third 16384, and so on), so a long-lived `-root`
ends up with fewer, larger directories over time rather than an
ever-growing flat list of same-size ones. `-capacity` must be a positive
integer. Re-running `shard` against the same `-root` resumes filling
whatever shard directories already exist there instead of starting over.

**Moving and duplicate detection.** With `-apply`, each candidate file is
moved into the shard directory a `Sharder` currently has open, using the
same size-then-content-hash duplicate check `organize`/`move` use: a file
already present there with the same size and BLAKE3 hash is left in its
original (unsharded) location and reported as a duplicate, rather than
moved or counted as sharded — it will be offered again on the next `shard`
run. A missing source file (gone between listing and moving) is reported
as a failure, not silently skipped. Every file that does move has its
`files` row's `path`/`filename` rewritten in place to the new location
(`invlib.UpdateFilePath`).

Examples:

```bash
# See which files would be moved into ~/Images's shard directories
image-browser shard

# Apply it, with a smaller first-shard capacity
image-browser shard -capacity 1024 -apply

# Shard a different root entirely
image-browser shard -root ~/Images/Archive -apply
```

### `warm-thumbnails`

```
image-browser warm-thumbnails [flags]

Flags:
  -db string            path to the inventory database (default: the shared ~/.config/inventory-manager/db/inv.db)
  -concurrency int      number of thumbnails to generate concurrently (default: GOMAXPROCS)
  -quiet                suppress per-file progress output
  -refresh              regenerate every thumbnail, even ones already cached
  -ThumbnailSize int     thumbnail size in pixels on the longest edge; setting this always implies -refresh (default 150)
```

Eagerly generates every thumbnail `invlib.WarmAll` finds missing from the
shared database, instead of leaving each one to be generated lazily the
first time some viewer happens to display that image — this program's own
grid views, inventory-manager's browser, or media-site's `/thumbs` route
all read from and populate the exact same on-disk cache (see
[Viewer UI Reference](#viewer-ui-reference)'s "Thumbnail cache" section for
the cache's layout and identity rules).

With no flags, only images missing a cached thumbnail are generated;
already-cached ones are counted as skipped and left alone. `-refresh`
regenerates every thumbnail unconditionally, even ones already cached.
`-ThumbnailSize` (note the capitalization — it's the literal flag name)
generates (and reads back) thumbnails at a size other than the shared
default of 150px; because a different size has its own, entirely separate
cache directory (`invlib.DefaultThumbCacheDir`'s `<size>` path segment),
passing `-ThumbnailSize` at all — even to its own default value — always
implies `-refresh` too, so that size's cache is never left half-populated
by whatever happened to already be sitting in that directory. `-db`
overrides which inventory database to read file records from (default:
`invlib.DefaultPath()`); this only changes where the *list of files* to
warm comes from — the thumbnail cache itself is always the one shared,
OS-default location for the given size, not scoped per database.

Progress is printed one line per file as each one finishes (`[done/total]
path`, overwriting the previous line) unless `-quiet` is given; a failure
(missing source file, undecodable image, or an animated WebP — see
[Viewer UI Reference](#viewer-ui-reference)) is printed permanently rather
than overwritten. `Ctrl-C`/`SIGINT` stops starting new work but lets
already-in-flight thumbnails finish, then reports the partial result — the
same graceful-cancellation shape as `process`.

At the end, a one-line summary reports how many images were found, how
many thumbnails were generated/already cached/failed, and the elapsed
time:

```
warmed 8421 image(s) in 1m12.4s: 340 generated, 8071 already cached, 10 failed
```

Example:

```bash
# Warm whatever's missing from the default (150px) cache, using every core
image-browser warm-thumbnails

# Force-regenerate everything, quietly, with 4 workers
image-browser warm-thumbnails -refresh -quiet -concurrency 4

# Warm a second cache size (e.g. for a future larger-thumbnail UI)
image-browser warm-thumbnails -ThumbnailSize 300
```

## The inventory database

`scan`, `process`, `import`, `reconcile`, `move`, `browse`, and `shard` all
read or write one shared SQLite database — `inv.db` — rather than one per
manifest or per scanned directory. The database itself is defined and
managed by a separate local Go module, `inv-lib` (imported as `invlib`) —
not a package inside this repository, but a required sibling checkout for
building this one (see [Building](#building) above). Its location is
resolved by `invlib.DefaultPath`: `~/.config/inventory-manager/db/inv.db`,
created (along with any missing parent directories) on first use. The
path's `inventory-manager` segment names another `inv-lib` consumer — a
sibling Fyne GUI (search/tag/edit inventory records directly, rather than
by folder) that happens to share this exact default path and schema. The
same path and schema are also used by the sibling `site-scraper` tool,
which writes the `*manifest.json` files `import` and `reconcile` consume.
A fourth sibling, `media-site`, maintains its own separate copy of the
schema rather than opening this same database file — but it shares the
same on-disk thumbnail cache (see [Viewer UI Reference](#viewer-ui-reference)
below), since `invlib.ThumbCache` is keyed by content hash rather than by
which database recorded that content.

```sql
sqlite3 ~/.config/inventory-manager/db/inv.db "select path, filesize, hash from files;"
```

Set `$INVENTORY_MANAGER_DB` to use a different path instead (e.g. for a
second, separate library) — `DefaultPath` creates that path's parent
directory the same way it does for the real default. image-browser's own
tests set this to a temp file so running them never touches your real
inventory.

**Schema.** `files` is the central table — one row per physical file, keyed
by its (unique) absolute `path`. It also carries lifecycle columns beyond
the basics (size, hash, mtime): `scanned_at`/`last_verified_at`/
`missing_since` track whether and when a file was last confirmed to exist
(see [`reconcile`](#reconcile) above, the only command that manages these
today), and `status`/`error` record a `process` decode outcome
(`processed`/`failed`, or cleared by `reconcile` when a file's content
changes). Several one-to-one child tables extend a `files` row, each keyed
by its `file_id` and removed automatically if the `files` row itself is ever
deleted (`ON DELETE CASCADE`):

- `images` — width, height, format, color space. Populated by `scan`,
  `process`, `import` (when a manifest entry already reports dimensions),
  and backfilled by `reconcile` for a newly discovered file.
- `videos`, `audio`, `media_probe` — defined in the schema (duration,
  codec, bitrate, …; raw ffprobe JSON) but not populated by anything in this
  repository yet. `reconcile` does clear a changed file's rows here (so a
  future ffprobe-based tool regenerates them from scratch rather than
  keeping stale metadata), but nothing currently writes them.

Two more tables exist independently of any one file:

- `origins` — where a file came from, if known: `site`, `url`, `identifier`,
  `original_file`, `discovered_at`. Only `import` ever creates these, linked
  to a `files` row via `origin_id`.
- `tags` / `file_tags` — a plain many-to-many tagging junction. Defined in
  the schema and queryable (`invlib.ListTagsForFile`, `SetFileTags`, …), but
  no subcommand in this repository currently manages tags. (The sibling
  `inventory-manager` program does — see its own README.)

See the `inv-lib` module's own README for the complete column-by-column
schema reference, migration behavior, and full package API (`FileQuery`'s
full set of filter fields, `Sharder`, `ThumbCache`, etc.) — this section
only summarizes what matters for this program's own commands.

**Routine upkeep vs. reconciliation.** `move`, `size`, and `browse` all call
`invdb.PruneMissing` at startup, which deletes a `files` row outright
(cascading to its `images`/etc. rows) the moment its file is gone — a
one-way cleanup with no history kept, meant to keep those commands from
operating on stale records. `reconcile` (above) is the deliberate
alternative when you want to know *that* and *since when* a file went
missing, without losing the row or its metadata.

`inv.db` is purely additive from `scan`'s and `import`'s point of view: a
failure to resolve or open its path aborts the run that needed it, but a
failure to insert any one file's or image's row is only logged and does not
fail the rest of that run.

## Viewer UI Reference

The GUI opened by `view`, `size`, and `move -preview` shares one
`Session`/`Browser` implementation (`internal/viewer`). The window opens at
2880×1864 by default; toggling Fullscreen Mode (see the menu bar below) off
again restores that size. Once a window is open:

**Thumbnail cache.** Thumbnails are cached on disk via the shared
`invlib.ThumbCache` (from the sibling `inv-lib` module), **not** a package
inside this repository — a dedicated `inv-lib/thumbnails/150` subdirectory
of the OS's per-user cache directory (`~/Library/Caches` on macOS,
`$XDG_CACHE_HOME` or `~/.cache` on Linux; `150` is `viewer.ThumbnailSize`,
the pixel size every thumbnail consumer in this ecosystem generates and
reads at — see [`warm-thumbnails`](#warm-thumbnails) above). Each cached
thumbnail is a JPEG (quality 88, resized with Lanczos3 resampling), named
after its source image's own BLAKE3 content hash and sharded into a
two-hex-character subdirectory (the way git shards its object store, so no
single directory accumulates too many files) — e.g.
`inv-lib/thumbnails/150/b1/b1946ac92492d2347c6235b4d2611184.jpg`. Because
the cache key is the source's content hash rather than its path, the exact
same on-disk cache is shared with `inventory-manager`'s browser and
`media-site`'s `/thumbs` route, and moving an image (`organize`,
`view -move-all`, or `move`) needs no special handling here — the same
cached thumbnail is simply found again under the same hash, wherever the
image now lives, and an edited image simply gets a new cache entry under
its new hash rather than needing any staleness check against the old one.
An animated WebP is refused with a clear error rather than silently
thumbnailing only its first frame. If the on-disk cache can't be created at
all (e.g. the user cache directory can't be resolved), the viewer degrades
to decoding and resizing thumbnails on the fly (nearest-available
approximate bilinear scaling) without it, rather than becoming unusable.

**Grid view** (thumbnail grid, grouped by directory for search results):
- Click a thumbnail to open it full-size.
- Click a directory heading or a thumbnail's filename label to copy that text
  to the clipboard.
- "Open Folder →" on a directory drills into that folder's own thumbnail grid
  (search/preview results only).

**Single-image view** (1:1 pixel display in a scrollable area):
- `→` / `←` — next / previous image
- `Delete` or `Backspace` — send the current image to the system Trash
  (macOS: `trash` CLI if installed, else AppleScript/Finder; Linux: `gio
  trash`; not implemented elsewhere)
- `t` / `T` — same as Delete/Backspace (trash current image)
- `g` / `G` — toggle between grid and single-image view
- `q` / `Q` — **during a review sequence** (search-driven `view`, every
  `size` batch, `move -preview`), quits the whole review sequence — the
  same as clicking the toolbar's Quit button. Outside a review sequence
  (a pattern-less `view`'s plain folder browser), closes the window
  instead.
- `Esc` — **during a review sequence**, accepts the current batch and
  continues to the next one — the same as clicking Continue. This is
  **not** the same as `q`/`Q` above: Esc never quits a review sequence by
  itself. Outside a review sequence, Esc also closes the window (matching
  `q`/`Q`'s behavior there).
- "← Grid" button — back to the grid
- "Copy Path" button — copy the current image's full path to the clipboard

**Review toolbar** (bottom of the window, shown for search-driven `view`, for
every `size` batch, and for `move -preview`):
- **Continue** — accept the current batch/group and move to the next
- **Trash Batch** — (only when trash is allowed for that batch) send every
  file currently shown to the Trash, then Continue
- **Move All** — (only when `-move-all` or `move -preview` enabled it) move
  every matched image to the configured destination, after a confirmation
  dialog. Safe to click more than once (see `move -preview` above for why).
- **Change Destination** — (shown alongside Move All) redirect Move All to a
  different folder, without restarting the command, via a text field or the
  app's own folder-picker dialog (see below) — the field accepts a
  not-yet-existing path too; it's created the first time Move All actually
  runs.
- **Quit** — stop the whole review sequence without moving anything

**Folder-browse toolbar** (bottom of the window, shown only for a pattern-less
`view` with no search/review batch):
- An editable path field — press Enter or click Refresh to reload whatever
  path is typed
- **Choose Directory** — opens the app's own folder-picker dialog (see below)
- **Refresh** — reload the current path
- **Preview subdirectories** — toggle between a flat single-folder listing
  and a recursive, grouped-by-subdirectory listing

**Folder-picker dialog** (opened by Choose Directory above, and by Change
Destination's own Browse… button). This is not the operating system's native
file chooser — it's an in-app dialog (`internal/filedialog`) built from Fyne
widgets, intentionally kept close to Fyne's own file-dialog API so it could
be swapped back out later:
- Click a row to open (a directory) or select (a file); in folder mode,
  clicking a subdirectory navigates straight into it rather than merely
  selecting it — "Open" always confirms whichever directory is currently
  shown, not a highlighted row
- Type to jump to the nearest entry whose name starts with what you've
  typed so far (reset after a second's pause), preferring directories —
  only falling back to matching a file if no directory matches your most
  recently typed character
- `Return`/`Enter` — open the currently selected row
- In folder mode, each subfolder row shows its own direct (non-recursive)
  image count, e.g. `Vacation2019  (42 images)`, using the same
  `images.Extensions` list from
  [Recognized image extensions](#recognized-image-extensions) above (not
  the `count` command's own extra `.avif`/`.heic` extensions)
- `Cmd+Shift+G` (or `Super+Shift+G` on Linux/Windows) — type a path to jump
  straight to it, bypassing manual navigation
- `Cmd+Shift+.` (or `Super+Shift+.`) — toggle showing hidden (dot-prefixed)
  files and folders, off by default
- The breadcrumb bar above the list lets you jump back up to any ancestor
  directory in one click

The window's native menu bar also has **File** → Open Folder / Refresh /
Quit (Open Folder and Refresh are safe no-ops whenever the currently shown
content has nothing well-defined to refresh or browse into — e.g. mid-batch
in a `size` review), **Settings** → Fullscreen Mode, and **About** (a short
description dialog).

## Testing

```bash
make test
```

Equivalent to `go test ./...`, which runs every `_test.go` file across all
packages. Thirteen packages currently have automated tests: `internal/browse`,
`internal/count`, `internal/filedialog`, `internal/images`, `internal/ingest`,
`internal/manifest`, `internal/move`, `internal/paths`, `internal/process`,
`internal/shard`, `internal/size`, `internal/viewer`, and
`internal/warmthumbs`. `internal/invdb`, `internal/organize`,
`internal/reconcile`, `internal/scan`, and `internal/trash` have no automated
tests yet — each was exercised manually (a throwaway database and image
tree, for the ones touching `inv.db`) while being built.

Run a single package's tests, or a single test, with the standard `go test`
flags:

```bash
go test ./internal/images/...
go test ./internal/viewer/... -run TestBrowser -v
```

## Packaging

```bash
make package
```

Runs `fyne package -os darwin -name "Image Browser"` (requires the
[`fyne` CLI](https://docs.fyne.io/started/packaging), install with
`go install fyne.io/fyne/v2/cmd/fyne@latest`), producing a `.app` bundle on
macOS using the metadata in `FyneApp.toml` (including its `[Migrations]
fyneDo = true` entry). The `make package` target always targets `-os
darwin`; run `fyne package` directly with a different `-os` (e.g. `linux`,
`windows`) to package for another platform.

## Project Layout

```
cmd/image-browser/       main() and command dispatch
internal/browse/         `browse` command: Bubble Tea terminal UI for browsing/selecting/moving records
internal/images/         image discovery, hashing, move/dedupe, decode/scan helpers (no local thumbnail cache — see inv-lib)
internal/organize/       `organize` command
internal/move/           `move` command: database field filtering, move, GUI preview
internal/count/          `count` command
internal/size/           `size` command
internal/process/        `process` command
internal/scan/           `scan` command
internal/ingest/         `import` command: read scraper manifests and attach provenance
internal/reconcile/      `reconcile` command: diff the database against disk, prune stale manifest.json entries
internal/shard/          `shard` command: consolidate database-tracked files into a root's shard directories
internal/warmthumbs/     `warm-thumbnails` command: eagerly populate the shared thumbnail cache
internal/invdb/          inventory-database maintenance helpers (e.g. PruneMissing, used by `move`/`size`/`browse` at startup)
internal/manifest/       shared manifest Entry/Status types, shape-preserving load/save
internal/viewer/         Fyne Session/Browser: grid, single-image view, toolbars, shortcuts
internal/filedialog/     in-app Fyne-widget folder/file-picker dialog (not the OS's native chooser)
internal/trash/          OS-specific "move to Trash" implementation
internal/paths/          path expansion (`~`) and default-root helpers
```

`internal/manifest/sync.go` additionally defines `SyncMoves` and
`MergeIntoDirectoryManifest` — logic to keep a `*manifest.json` in sync when
images move, from before the database became the single source of truth for
file location (see [The inventory database](#the-inventory-database)).
They're still covered by `sync_test.go`, but no command in this repository
currently calls them; `move`, `organize`, and `view -move-all` all update
`inv.db` directly instead and leave manifest.json alone (`reconcile` is the
only command that still touches manifest.json, and only to prune stale
entries — see [`reconcile`](#reconcile) above).

Only `organize`, `count`, `process`, `scan`, `import`, `reconcile`, `shard`,
and `warm-thumbnails` are pure CLI tools with no Fyne dependency; `view` and
`size` both build on `internal/viewer`, and so does `move`, for its optional
`-preview` mode. `browse` has no Fyne dependency either — its terminal UI is
built on [Bubble Tea](https://github.com/charmbracelet/bubbletea) instead.
