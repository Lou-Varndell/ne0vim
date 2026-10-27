# Inventory Manager

A native Go/[Fyne](https://fyne.io/) desktop GUI for searching, tagging, and
curating a shared image inventory **database** — as opposed to
[`image-browser`](../image-browser), which is CLI-first and organizes images
by *folder*/*pattern*/*database-field filter* through separate subcommands.
Inventory Manager is a single always-open window: a metadata filter bar over
a responsive thumbnail grid, with File/Settings/About menus, that lets you
search by tag/site/path (or a full structured/raw-SQL Advanced Search),
import a directory tree by content hash, edit a record's site/tags inline,
and delete or trash a record from a full-size viewer — all without ever
needing a second window per folder.

The database itself — schema, migration, and all CRUD — is owned by the
sibling [`inv-lib`](../inv-lib) module; this app only ever searches, edits,
and deletes through it, and it is **the same shared `inv.db`** that
`site-scraper`, `image-browser`, and `media-site` read and write (see
[The inventory database](#the-inventory-database) below) — an image indexed
by any one of those tools shows up in this one too.

## Table of Contents

- [Features at a glance](#features-at-a-glance)
- [Building](#building)
- [Running](#running)
- [The main window](#the-main-window)
- [Import Folder](#import-folder)
- [Manage Tags](#manage-tags)
- [Tag Selected (bulk tagging)](#tag-selected-bulk-tagging)
- [Advanced Search](#advanced-search)
- [The image viewer](#the-image-viewer)
- [Thumbnails](#thumbnails)
- [The folder picker dialog](#the-folder-picker-dialog)
- [The inventory database](#the-inventory-database)
- [Testing](#testing)
- [Known quirks and limitations](#known-quirks-and-limitations)
- [Project layout](#project-layout)

## Features at a glance

- File/Settings/About native menu bar, plus an in-window toolbar that
  mirrors the menu's database actions.
- Metadata filter bar (tag/site/path, each a case-insensitive substring
  match) over a responsive thumbnail grid — column count adapts live to
  window width.
- **Import Folder** recursively scans a directory tree (via the app's own
  breadcrumb/type-ahead/hidden-file folder picker) and indexes every image
  found into the database by BLAKE3 content hash.
- On-disk thumbnail cache (shared implementation with `image-browser`, via
  `inv-lib`), with background generation and a manual "Refresh Thumbnails"
  button.
- Click a thumbnail to open a full-size **viewer**: previous/next keyboard
  navigation, inline editing of a record's site/tags, **Delete Record**
  (database row only — the file is left on disk) vs. the keyboard's
  **Delete/Backspace/t/T** (sends the file to the OS Trash *and* removes the
  database row — see [The image viewer](#the-image-viewer)).
- Multi-select in the grid (click, Shift+click range-select, Ctrl/Cmd+A
  select-displayed) feeding a **Tag Selected** bulk-tagging dialog.
- **Manage Tags** dialog: create tags up front or delete existing ones,
  independent of any single record.
- **Advanced Search** window: every structured field the toolbar supports,
  plus URL, identifier, status, exact hash, width/height ranges, and a
  discovered-date range — or a raw SQL box that overrides all of them.
- External "Open" (platform default viewer/handler), fullscreen mode, and
  decoding for JPEG/PNG/GIF/WebP(including animated, via an external
  `ffplay` fallback)/BMP/TIFF.

## Building

Requires **Go 1.27.1** (the exact version pinned in `go.mod`) and, because
this is a Fyne GUI app, the platform build dependencies Fyne needs to
compile its CGO/OpenGL bindings — on macOS this is just the Xcode command
line tools; see the [Fyne getting-started docs](https://docs.fyne.io/started/)
for Linux/Windows requirements (X11/GL dev packages, a C compiler, etc.).

**`inv-lib` must be checked out as a sibling directory.** `go.mod` points at
it with `replace inv-lib => ../inv-lib`, since it isn't published anywhere —
it must exist at `../inv-lib` relative to this repo's own directory. For
example, if this repo is checked out at `~/src/inventory-manager`, `inv-lib`
must be at `~/src/inv-lib`:

```
~/src/
  inv-lib/              # required sibling checkout
  inventory-manager/    # this repo
```

`go build`, `go run`, and `go test` in this directory will fail to resolve
the `inv-lib` import until that sibling checkout exists.

```bash
go mod tidy
go build -o inventory-manager ./cmd/inventory-manager
```

Or via the `Makefile`:

| Target | What it does |
|---|---|
| `make build` | `go build -o $(BINARY) ./cmd/inventory-manager`, where `BINARY` defaults to `~/.local/bin/inventory-manager` (edit the `BINARY` variable at the top of the `Makefile` to install elsewhere). |
| `make fmt` | `gofmt -w` every `.go` file in the repo, found via `fd`. |
| `make tidy` | `go mod tidy`. |
| `make vet` | `go vet ./...`. |
| `make test` | `go test ./...` (see [Testing](#testing)). |
| `make staticcheck` | `staticcheck ./...` — requires the [`staticcheck`](https://staticcheck.io/) binary on `PATH`. |
| `make golangci-lint` | `golangci-lint run` — requires [`golangci-lint`](https://golangci-lint.run/) on `PATH`. |
| `make package` | `fyne package -os darwin -name "Image Browser"` — see the caveat in [Known quirks and limitations](#known-quirks-and-limitations) before relying on this. |
| `make clean` | `rm -f $(BINARY)`. |
| `make all` | `fmt`, `tidy`, `test`, then `build`, in that order. |

## Running

```bash
./inventory-manager
```

| Flag | Default | Description |
|---|---|---|
| `-db` | *(empty — see below)* | Path to the inventory database. |

With `-db` omitted (or blank), the app opens the shared database at
`invlib.DefaultPath()` — see [The inventory database](#the-inventory-database)
below for exactly where that resolves to and how to override it. The window
opens with no filter applied (every indexed image, newest-schema-compatible
record, is loaded on startup). Use **File > Import Folder…** to index a
directory's images into it, and the toolbar's tag/site/path fields (or
**File > Advanced Search…**) to search it:

```bash
# Open the default, shared database
./inventory-manager

# Open a specific database file instead
./inventory-manager -db /path/to/other.db
```

If the database can't be opened at all (missing permissions, a corrupt
file, etc.), the window still opens — every database-dependent action
degrades to a "No inventory database available" message/dialog instead of
crashing — and an error dialog is shown once at startup naming the problem.

## The main window

**File menu** (and the toolbar buttons that mirror most of it):

| Item | Action |
|---|---|
| Import Folder… | Opens the folder picker; recursively indexes the chosen directory (see [Import Folder](#import-folder)). |
| Manage Tags… | Opens the [Manage Tags](#manage-tags) dialog. |
| Advanced Search… | Opens the [Advanced Search](#advanced-search) window. |
| Refresh | Re-runs the active filter at its current page, picking up database changes made by another tool (e.g. `site-scraper`) since the window loaded — unlike the toolbar's **Search** button, it never resets the filter or page back to the first one. Bound to **Ctrl+R** (**Cmd+R** on macOS). |
| Deselect All | Clears every checked file in the grid (any page), without re-running the search. |
| Quit | Closes the window. |

**Settings menu:** **Fullscreen Mode** toggles `fyne.Window.SetFullScreen`.
Leaving fullscreen always restores the window to its fixed **1200×850**
startup size — it does not remember a size you resized the window to
manually before entering fullscreen.

**About menu:** a static card describing the app.

**Toolbar**, left to right: three filter fields — **Tag contains…**, **Site
contains…**, **Path contains…** (each submits the search on Enter) — then
**Search**, **Clear** (blanks every filter field, deselects everything, and
reloads page 1), **Import Folder…**, **Manage Tags…**, **Refresh
Thumbnails** (forces every thumbnail on the current page to re-decode/
re-resize/re-encode, bypassing the cache), **Tag Selected…** (opens the bulk
tag dialog for whatever's checked), and a **"N selected"** label.

**Grid and pagination.** Matching records are shown **200 at a time**
(`pageSize`), newest schema rules applied: a record is only shown if it has
a decoded `images` row (this app never shows video/audio-only files) and is
not marked missing by a `reconcile` pass elsewhere (`missing_since` unset).
Prev/Next page buttons and a "Page X of Y (N images)" label sit in the
footer; only files with an actual `images` row count toward that total.
Column count recalculates automatically on every window resize
(`ThumbnailGridLayout`).

**Selecting cards.** Each card has a checkbox (for bulk tagging) independent
of clicking the thumbnail itself (which opens the viewer):

- Click a checkbox — toggles just that card.
- **Shift+click** a checkbox — selects every card between the last one you
  clicked and this one (inclusive), in addition to whatever was already
  selected.
- **Ctrl/Cmd+A** — selects every card on the **current page only** (not
  every match across all pages). Routed through Fyne's shortcut system, so
  it never fires while a filter text field has focus.
- Clicking a card's filename label (below the thumbnail) copies its full
  path to the clipboard — it does not open the viewer.

## Import Folder

**File > Import Folder…** (or the toolbar button) opens the
[folder picker](#the-folder-picker-dialog) and recursively scans the chosen
directory for files with a recognized image extension (`.jpg` `.jpeg` `.png`
`.gif` `.webp` `.bmp` `.tif` `.tiff`), then indexes every one found,
concurrently, bounded to `runtime.GOMAXPROCS(0)` in-flight goroutines at a
time.

**Indexing is non-destructive to existing metadata.** For each file:

- Its size, modification time, and a fresh BLAKE3 content hash are always
  computed and written.
- **New path** — a brand-new `files` row is created with just those
  columns (no origin, no tags).
- **Already-known path** — the *existing* row is fetched and updated in
  place: only `filesize`/`hash`/`modified_at` (and `scanned_at`, via
  `TouchScanned`) are refreshed. Everything else the import step doesn't
  know about — `origin_id` (site), `status`/`error`, and every tag — is
  left exactly as it was. Re-importing a folder you've already tagged is
  therefore safe and does not wipe that tagging out.
- The file's `images` row (width/height) is always upserted from a fresh
  header-only decode, regardless of whether the file was new or already
  known.

After indexing finishes, the current search re-runs automatically, so
newly imported files that match the active filter appear right away.

## Manage Tags

**File > Manage Tags…** (or the toolbar button) opens a modal dialog
listing every tag in the database with a **Delete** button next to each,
plus a field to type a new tag name and **Add** it (created up front, with
no file attached yet). Deleting a tag asks for confirmation first, since it
removes that tag from every file currently carrying it
(`invlib.DeleteTag`). The main grid's search re-runs after any change, in
case the active tag filter is affected.

## Tag Selected (bulk tagging)

With one or more cards checked in the grid, **Tag Selected…** opens a
dialog to add a comma-separated list of tags (with the same
autocomplete/suggestion behavior as the viewer's tag field — see
[The image viewer](#the-image-viewer)) to **every** selected file at once.
Unlike the viewer's per-file tag editor, this only **adds** tags — each
file's existing tags are left alone, since a batch of selected files may
already carry different tags of their own. The selection is cleared and the
grid re-searched afterward.

## Advanced Search

**File > Advanced Search…** opens a separate, non-modal window (it stays
open alongside the main window, unlike the modal Manage Tags/Tag Selected
dialogs) with every structured field the toolbar's Tag/Site/Path already
support, plus more:

| Field | Match |
|---|---|
| Tag, Site, Path | Case-insensitive substring (same as the toolbar). Pre-filled from the toolbar's current values when the window opens. |
| URL, Identifier, Status | Case-insensitive substring against the file's origin/status. |
| Hash | **Exact** match (not a substring). |
| Width min/max, Height min/max | Inclusive integer range against the file's `images` row. |
| Discovered from/to | Inclusive date range (`YYYY-MM-DD`) against the file's origin `discovered_at`. |

Click **Search** to run the structured filter, or type into the **raw SQL**
box underneath — whenever it's non-blank, it takes over outright: the
structured fields above visibly disable themselves, and the SQL you typed
is run instead via a short-lived, **read-only** connection to the database
(`invlib.OpenReadOnly` + `QueryFileIDs`), opened and closed just for that
one query. Your SQL must return a `file_id` or `id` column; the resulting
set of ids becomes the search's exact result set (an empty result set means
"zero matches," not "unfiltered"). A validation error (a bad number/date in
a structured field) or a SQL error is shown inline in the window — **Clear**
blanks every field in the window (including the SQL box) and calls the same
"reset to default" routine the toolbar's own **Clear** button uses, so the
two can never disagree about what "default" means.

Either path — structured or raw SQL — hands its resulting filter to the
same `Search` the main window uses: results load straight into the main
window's grid, so Tag Selected/multi-select/Refresh/pagination all keep
working against an Advanced Search result exactly as they do against a
toolbar search.

## The image viewer

Click any thumbnail to open it full-size in its own window: previous/next
navigation, inline metadata editing, and two different, **deliberately
distinct**, ways to get rid of a record.

**Navigation and shortcuts** (keyboard focus must be on the viewer window,
not a text field inside it):

| Key | Action |
|---|---|
| `←` / `↑` | Previous image |
| `→` / `↓` | Next image |
| `Esc` | Close the viewer |
| `Enter` / `Space` | Open the current file in the platform's default viewer/handler (`open` on macOS, `start` on Windows, `xdg-open` elsewhere) |
| `Delete` / `Backspace` | **Trash** the current file (see below) |
| `t` / `T` | Same as Delete/Backspace — trash the current file |
| `g` / `G` | Close the viewer and return focus to the main window ("back to grid") |

**Buttons:** **← Previous** / **Next →** (same as the arrow keys), **← Back
to Grid**, **Copy Path** (copies the full path to the clipboard), **Open**
(same as Enter/Space).

**Delete Record vs. Trash — not the same operation:**

| Action | Trigger | Removes database row? | Removes the file from disk? |
|---|---|---|---|
| **Delete Record** button | Click only | Yes | **No** — the file is left exactly where it is. |
| Trash | `Delete`/`Backspace`/`t`/`T` keys only | Yes | Yes — moved to the OS Trash/Recycle Bin (`macOS`: the `trash` CLI if installed, else an AppleScript Finder delete; `Linux`: `gio trash`; unimplemented elsewhere, returning an error instead of silently doing nothing). |

There is no button for the trash path and no keyboard shortcut for Delete
Record — the only way to actually remove a file from disk through this app
is the keyboard. Either action advances to the next record (or closes the
viewer if that was the last one) and refreshes the main grid.

**Editing metadata.** The **Site** field and a comma-separated **Tags**
field (with live autocomplete suggestions drawn from every tag already in
the database — type a few characters and matching tag names appear in a
list underneath, up to 8 at a time; click one to complete that tag without
losing whatever else you'd already typed) sit beside the image. **Save
Metadata** writes both: tags fully replace the file's existing tag set
(unlike the bulk Tag Selected dialog, which only adds); site creates a
lightweight `origins` row with `type: "manual"` the first time you set it
on a record with none, or updates the existing one's `site` column on later
edits. **The Site field is intentionally read-only once a site is already
set** — it's meant to protect scraper-captured provenance from an
accidental overwrite, and only accepts typing while the field is genuinely
empty.

**Full-resolution view.** Clicking the image itself (not a button) opens it
at native pixel size (no scaling) in a new scrollable window, sized between
300px and 1600px per side based on the image's actual dimensions. An
**animated WebP** is detected up front (the same RIFF/VP8X sniffing inv-lib
uses for its own thumbnail cache) and, instead of opening a static Fyne
image view that would only show the first frame, is handed off to an
external **`ffplay`** process (part of FFmpeg) looping fullscreen — if
`ffplay` isn't on `PATH`, this fails with a clear "ffplay not found; install
FFmpeg or check PATH" error (currently only logged to stdout, not shown as
a dialog — see [Known quirks and limitations](#known-quirks-and-limitations)).

## Thumbnails

Thumbnails are generated and cached by **`inv-lib`'s shared `ThumbCache`**
(`invlib.NewThumbCache`/`Generate`/`Path`), the exact same implementation
`image-browser`'s viewer uses — not a separate cache type local to this
repository. Key points:

- **Keyed by content**, not path: the cache key is the BLAKE3 hash of the
  source file's bytes (the same hash recorded in `files.hash`), so two
  different paths with identical bytes share one cached thumbnail, and a
  moved/renamed source image still resolves to its existing one.
- **Location:** under the OS's per-user cache directory, sized `150`px on
  the longest edge (the only size this app ever opens) — e.g.
  `~/Library/Caches/<app>/150/<hash>.jpg` on macOS. Always encoded as JPEG
  regardless of the source format.
- **Cache hit** is just an `os.Stat` on the hashed path — no re-decode, no
  re-hash of file contents beyond what computing the cache key already
  needed.
- **Cache miss** rejects animated WebP up front (it would otherwise
  silently thumbnail just the first frame), decodes the source, resizes
  with Lanczos3 resampling, and writes via a unique temp file + atomic
  rename so two concurrent generations for the same image can't corrupt
  each other's output.
- Generation for a page of results runs in the background (bounded to 10
  concurrent decode/resize goroutines, `imgPerDir`), filling thumbnails in
  as they finish rather than blocking the grid from appearing.
- There is **no automatic invalidation** if a source file's bytes change on
  disk without changing its path — the toolbar's **Refresh Thumbnails**
  button is the only way to force regeneration for whatever's currently on
  screen.
- If the cache directory can't be created (e.g. unwritable), thumbnail
  generation is skipped entirely and the grid still renders — thumbnails
  are a convenience, not core functionality.

> **Note:** `docs/thumbnails.md` in this repository describes an older,
> **no-longer-present** local `internal/images.Cache` implementation. The
> app now uses `inv-lib`'s shared `ThumbCache` as described above instead —
> treat that file as historical/stale rather than current documentation.

## The folder picker dialog

The dialog behind **Import Folder…** is not the operating system's native
file chooser — it's an in-app dialog (`internal/filedialog`) built from
Fyne widgets, kept close to Fyne's own `FileDialog` API intentionally, so it
could be swapped back out for the native one later. Folder-mode specifics:

- Clicking a directory row navigates straight into it (the current
  directory is always the implicit "selection" — **Open** confirms whatever
  directory is currently shown).
- Each subfolder row shows its own direct (non-recursive) image count in
  parentheses, e.g. `Vacation2019  (42 images)`.
- Breadcrumb buttons across the top jump straight to any ancestor
  directory.
- Type a few characters (type-ahead) to jump to the first matching entry by
  name; directory-only matches are preferred over file matches.
- **Cmd+Shift+G** (Super+Shift+G elsewhere) opens a "Go to folder" box to
  type an absolute (or `~`-relative) path directly.
- **Cmd+Shift+.** (Super+Shift+. elsewhere) toggles showing hidden
  (dot-prefixed) entries — off by default.
- A directory listing runs on a background goroutine so a large or slow
  directory never freezes the window; navigating away before it finishes
  discards the stale result instead of overwriting the newer one.

## The inventory database

This app opens the exact same shared SQLite file every other tool in this
monorepo does, resolved through `invlib.DefaultPath()` (i.e.
`~/.config/inventory-manager/db/inv.db`, created on first use) unless `-db`
overrides it — set the `INVENTORY_MANAGER_DB` environment variable to
override the default path instead of passing `-db` every time (the same
variable `site-scraper`'s and `image-browser`'s own test suites use to
avoid touching the real database). Schema creation and migration on open are
entirely `inv-lib`'s responsibility (`invlib.Open`); this app issues no SQL
of its own.

```sql
sqlite3 ~/.config/inventory-manager/db/inv.db "select path, filesize, hash from files;"
```

What this app writes, specifically:

- **`files`** — path, filename, extension-free size, BLAKE3 hash, and
  modification time, from Import Folder (see [Import Folder](#import-folder)
  for the exact update-vs-insert rule) or the viewer's metadata save.
- **`images`** — width/height, upserted from a header-only decode every
  time Import Folder sees a file (new or already-known).
- **`origins`** — a lightweight `type: "manual"` row the first time you set
  a **Site** on a record from the viewer (see
  [The image viewer](#the-image-viewer)); reused on later edits to the same
  record rather than duplicated.
- **`tags`/`file_tags`** — via Manage Tags (create/delete tags globally),
  the viewer's per-record tag editor (replaces a file's whole tag set), and
  Tag Selected (adds to many files' existing tag sets at once).

This app never writes `videos`, `audio`, `media_probe`, or `status`/`error`
— those are populated (if at all) by other tools in this monorepo. It also
never runs `ListFilesByHash` or any other duplicate-detection query itself —
the BLAKE3 hash is recorded on every import, but there is currently no
duplicate-finder UI in this app (that functionality, where it exists at
all, lives in `image-browser`'s `size`/`organize`/`move` commands instead).

## Testing

```bash
make test
# equivalent to: go test ./...
```

Only **`internal/ui`** currently has automated tests (`browser_test.go`,
`library_test.go`, `search_test.go`, `tagentry_test.go`, `viewer_test.go`),
covering: `sameFileSequence`'s grid-diffing; `searchRecords`'s filter
matching (by site, tag substring, path substring), pagination, and its
`missing_since`/deleted-file exclusion rules; `setSite`/`setTags`/
`addTagsToFiles`'s database-update semantics; the Advanced Search window's
numeric/date parsing and structured-filter building, plus its raw-SQL mode
(including the "zero matches from valid SQL" and "SQL error surfaced to the
caller" cases); the tag-input autocomplete's filter/select behavior; and the
viewer's site-field editable/read-only toggle. `cmd/inventory-manager`,
`internal/filedialog`, `internal/images`, and `internal/trash` have **no**
automated tests — each was exercised manually while being built.

```bash
# Run just one package, or one test, with the standard go test flags:
go test ./internal/ui/...
go test ./internal/ui/... -run TestSearchRecordsPagination -v
```

## Known quirks and limitations

- **`make package` is likely misconfigured for this repo.** It runs `fyne
  package -os darwin -name "Image Browser"` — both the app name and the
  lack of a `FyneApp.toml` in this directory (unlike `image-browser`, which
  has one) suggest this target was copied from that sibling's `Makefile`
  without being adapted. Treat it as unverified rather than a supported
  packaging path until it's corrected.
- **`Browser.InitialSize()` is dead code.** `cmd/inventory-manager/main.go`
  hardcodes the startup window size (`1200×850`) directly rather than
  calling it; the method exists but nothing in this repository invokes it.
- **Animated-WebP playback failures are silent.** If `ffplay` can't be
  found or started, `images.PlayAnimation`'s error is only printed to
  stdout (`fmt.Println`) rather than surfaced as a dialog — from the UI's
  perspective, clicking such an image to view it full-size can appear to
  do nothing.
- **No duplicate-detection UI.** BLAKE3 hashes are recorded on every
  import, but nothing in this app queries `ListFilesByHash` or otherwise
  surfaces duplicate content to the user.
- **`docs/thumbnails.md` is stale** — see the note in
  [Thumbnails](#thumbnails) above.
- Deleting a tag, trashing a file, and deleting a record are all
  irreversible from within the app itself (no undo) — Delete Record leaves
  the underlying file in place, so that one specifically can be re-imported
  later via Import Folder if needed; the other two cannot be undone this way.

## Project layout

```
cmd/inventory-manager/        main(): flag parsing, store/window wiring, native menu bar
internal/ui/                  the whole GUI: Browser (main window), viewer, search (Advanced Search),
                                 tags (Manage Tags / Tag Selected dialogs), tagentry (autocomplete tag field),
                                 selectcheck (checkbox click-modifier overlay), layout (responsive thumbnail grid)
internal/images/               image extension/recursive-scan helpers, header-only dimension decode,
                                 animated-WebP playback via external ffplay
internal/filedialog/           in-app Fyne-widget folder/file picker (not the OS's native chooser)
internal/trash/                OS-specific "move to Trash" implementation (macOS trash/Finder, Linux gio trash)
docs/thumbnails.md             STALE — describes a removed local thumbnail-cache implementation; see Thumbnails above
```

`inv-lib` (schema, migration, CRUD, the shared `ThumbCache`, BLAKE3 hashing,
and animated-WebP detection) is a required sibling checkout, not a package
inside this repository — see [Building](#building).
