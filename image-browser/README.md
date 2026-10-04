# Image Browser

A Fyne-based desktop tool for browsing, searching, organizing, and de-duplicating
large collections of images on disk. It ships as a single binary with six
subcommands: `organize`, `move`, `view`, `count`, `size`, and `process`.

Two of the subcommands (`view` and `size`) always open a graphical window
built on [Fyne](https://fyne.io/); `move` optionally does too, when given
`-preview`. `organize`, `count`, and `process` are plain CLI tools that print
to stdout/stderr and never open a window.

## Table of Contents

- [Building](#building)
- [Running](#running)
- [Subcommands](#subcommands)
  - [`view`](#view)
  - [`organize`](#organize)
  - [`move`](#move)
  - [`count`](#count)
  - [`size`](#size)
  - [`process`](#process)
- [Viewer UI Reference](#viewer-ui-reference)
- [Testing](#testing)
- [Packaging](#packaging)
- [Project Layout](#project-layout)

## Building

Requires Go 1.26+ and (for the GUI subcommands) the platform dependencies
Fyne needs to compile CGO/OpenGL bindings — on macOS this is just Xcode
command line tools; see the [Fyne getting-started docs](https://docs.fyne.io/started/)
for Linux/Windows requirements.

```bash
make build
```

This runs `go build -o image-browser ./cmd/image-browser` and produces an
`image-browser` binary in the repo root. Remove it with `make clean`.

You can also build/run directly with `go`:

```bash
go build -o image-browser ./cmd/image-browser
go run ./cmd/image-browser <command> [args]
```

`make run` is a shortcut for `go run ./cmd/image-browser view` (opens the
viewer on the default root with no search pattern).

## Running

```
image-browser <command> [args]
```

| Command   | Purpose                                                              |
|-----------|-----------------------------------------------------------------------|
| `organize`| Find images matching a pattern and move them to a destination.        |
| `move`    | Find manifest entries matching a field filter and move their files.   |
| `view`    | Browse a folder, or find images matching a pattern and review them.   |
| `count`   | Count images recursively in each immediate subdirectory of a root.    |
| `size`    | Group manifest images by exact resolution and review them in batches. |
| `process` | Read image dimensions for every file referenced in a JSON manifest.   |

Run `image-browser help` (or with no arguments) for the top-level usage
summary, and `image-browser <command> -h` for a command's flag reference.

Flags use Go's standard single-dash `flag` package syntax (e.g. `-root`, not
`--root`, though `--root` is also accepted since Go's flag package treats one
or two leading dashes identically).

## Subcommands

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
  in the destination. Any `*manifest.json` file(s) alongside a moved image's
  original directory are also updated: an entry whose `file` refers to a
  moved image is **removed** from that manifest — a manifest only ever
  describes images in its own directory or a subdirectory of it (see
  [Manifest format](#manifest-format) below), and the image has just moved
  out of that tree entirely — rather than rewritten in place to point at the
  new location. The `image-manifest.json` array format (used by
  `process`/`size`), a bare single-object `manifest.json`, and the
  `{"entries": [...]}` wrapper format (written by the sibling `site-scraper`
  tool) are all handled; entries with no matching moved file are left
  untouched, unless they fail the same directory-scope check (see below), in
  which case they're dropped too. Each moved entry is also mirrored into a
  `*manifest.json` in the destination directory,
  with `file` rewritten to the image's new absolute path — regardless of
  whether the source entry stored `file` as a bare filename or an absolute
  path, the destination's copy is always absolute, since it should
  unambiguously name the file it describes rather than depend on living
  next to it: an existing manifest there is updated in-place (matching
  entries by `file`'s base filename rather than the full string, so a
  stale entry from before this file was written as absolute is still
  replaced rather than duplicated), a new `image-manifest.json` is created
  if the destination has none, and more
  than one existing `*manifest.json` in the destination is reported as a
  failure rather than guessed at. A moved image with no source manifest
  entry is not added to the destination manifest.

  Searching by filesystem path is the right fit when you don't have (or
  don't want to rely on) a manifest. If you do, and want to filter on a
  manifest field other than the file path — site, status, hash, resolution,
  discovery date — see [`move`](#move) below, which also handles manifests
  scattered across several directories in one run instead of just the
  images under one `-root` search.

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
invocation.

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
  -root string           manifest file, or directory to search recursively for manifest files (default: ~/Images)
  -site string           match entries whose site contains value (case-insensitive)
  -sourceurl string      match entries whose source_url contains value (case-insensitive)
  -file string           match entries whose file contains value (case-insensitive)
  -hash string           match entries with exactly this hash
  -status string         match entries with exactly this status
  -error string          match entries whose error contains value (case-insensitive)
  -original string       match entries whose original_file contains value (case-insensitive)
  -discoveredat string   match entries discovered on this date (YYYY-MM-DD)
  -width int             match entries with exactly this width
  -height int            match entries with exactly this height
  -dest string           destination directory (default: derived from the match value)
  -preview               preview matches in the viewer before moving, with the option to change destination
```

Unlike `organize`, `move` doesn't search the filesystem for images that match
a path pattern — it searches **manifest entries** (see [Manifest
format](#manifest-format) below) by field, and moves the file each matching
entry points at. A bare `[value]` is shorthand for `-site`; giving both is an
error. Every other flag maps directly to one manifest field; `-site`,
`-sourceurl`, `-file`, `-original`, and `-error` match a case-insensitive
substring, while `-hash` and `-status` require an exact (case-insensitive)
match, `-width`/`-height` require an exact integer match, and `-discoveredat`
matches the calendar date portion of `discovered_at` only (time of day is
ignored). At least one filter is required. Multiple flags combine with AND —
e.g. `-status kept -width 1024` only matches entries that are both.

**Finding manifests.** `-root` (default `~/Images`, same convention as
`organize`/`view`/`count`) can point at a single manifest file or at a
directory. A directory is searched recursively for every file matching
`*manifest.json` (`manifest.json`, `image-manifest.json`, a site-scraper
`manifest.json`, …), however many directories deep and however many separate
manifest files exist — every match from every one of them is moved in a
single run.

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

**Manifest updates.** Every manifest file `move` touches is rewritten in the
same shape it was read in (bare array, single bare object, or the
`{"entries": [...]}` wrapper — see [Manifest format](#manifest-format)).
A matched entry is **removed** from the manifest it was found in once its
move succeeds — a manifest only ever describes images in its own directory
or a subdirectory of it, and the file has just moved out of that tree, so a
pointer left behind would be stale and, worse, would still match the same
filter on a later run and get "moved" a second time. Entries that didn't
match, or that matched but weren't moved (duplicate or error), are left in
place, unless loading the manifest finds them already invalid (missing file,
or `file` already outside this directory's tree from an older version of
this tool), in which case they're dropped on load regardless of whether
anything matches. The moved entries are also merged into a `*manifest.json`
in the destination directory — created if none exists there yet, or merged
in place (matched by `file`'s base name) if one does — with `file` always
written as an absolute path.

**`-preview`.** Instead of moving immediately, opens the GUI viewer on every
matched image with the same Move All / Change Destination / Quit controls
`view -move-all` has (see [Viewer UI Reference](#viewer-ui-reference)) —
look over the batch, optionally redirect to a different folder, and only
commit the move once you click Move All and confirm. Clicking Move All more
than once (after a partial failure, or after changing the destination) is
always safe: an entry that already moved is simply found sitting in the new
location and reported as a duplicate of itself rather than moved or
duplicated again, and changing the destination after some files already
moved relocates them again to the new folder.

Examples:

```bash
# Move every entry whose site mentions "bluebird" under ~/Images into ./bluebird
image-browser move bluebird

# Same, but search a specific manifest file and pick the destination explicitly
image-browser move -site bluebird -root ~/Images/wildlife/manifest.json -dest ~/Images/Keepers/bluebird

# Move every "kept" entry at exactly 1024x768 into ./kept-1024
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
`view` against them.

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
image-browser size [flags] [image-manifest.json]

Flags:
  -largest         show largest resolutions first
  -smallest        show smallest resolutions first (default)
  -batch-size int  maximum files per preview batch (default 40)
  -trash           allow moving a reviewed batch to Trash
```

Reads a JSON manifest (see [Manifest format](#manifest-format) below;
defaults to `image-manifest.json` in the current directory if no path is
given), groups every successfully processed record by exact `width x height`,
and opens the GUI viewer to walk through each resolution group in batches of
`-batch-size` files. This is meant for triaging manifests that include a lot
of thumbnail/placeholder-sized duplicates: sort smallest-first (default) to
review and trash them quickly, or largest-first to check the best copies.

Each group is shown in its own review pass; batches within a group are
requested with the Continue button (moves to the next batch), Trash Batch
(only enabled with `-trash`; sends every file in the current batch to the
system Trash before continuing), or Quit (stops the whole `size` run
immediately, including any remaining groups).

Manifest entries are only included if their `status` is `"processed"` (see
`process` below), have non-zero width/height, and the referenced file still
exists on disk — anything else is silently skipped since there is nothing
useful to preview.

Example:

```bash
# Review the smallest-resolution duplicates first, 25 per batch, allowing trash
image-browser size -smallest -batch-size 25 -trash processed-image-manifest.json
```

### `process`

```
image-browser process [flags]

Flags:
  -input string             input JSON file (default "image-manifest.json")
  -output string            output JSON file (default: "processed-<input>" alongside the input)
  -workers int              number of concurrent image-decode workers (default 8)
  -checkpoint-every int     write a checkpoint after this many unique files (0 disables checkpointing)
```

Before anything else, every record's `file` is normalized: a bare filename
(no directory component — the convention for a manifest co-located with
its images, the same one `view -move-all`'s manifest sync uses) is rewritten
to an absolute path resolved against `-input`'s own directory, e.g. `file:
"001.jpg"` in `.../laksr.v2/image-manifest.json` becomes `file:
".../laksr.v2/001.jpg"`. An already-absolute `file` is left untouched. This
rewrite is persisted to `-output`, not just used internally, so re-running
`process` against the same manifest from a different working directory
still resolves correctly.

Reads a JSON manifest of image records, decodes every unique referenced file
(`.gif`, `.jpeg`/`.jpg`, `.png`, `.webp`) with a worker pool to fill in each
record's `width`/`height`, and writes the result to `-output`. Records
sharing the same `file` value are all updated together, so duplicate
references only decode the file once. Records are marked:

- `missing` — the file no longer exists on disk
- `non-image` — the file extension isn't a recognized image type
- `invalid` — the record has no `file` path at all
- `processed` — dimensions were read successfully
- `failed` — the file exists and looks like an image but couldn't be decoded

`-checkpoint-every` (default 5000) periodically writes the in-progress output
file so a long-running process against a huge manifest can be interrupted
(`Ctrl-C`/`SIGINT` is handled gracefully — the in-flight batch finishes and a
final write happens before exit) without losing completed work. Set it to `0`
to disable and only write once at the end.

Example:

```bash
image-browser process -input image-manifest.json -output processed-image-manifest.json -workers 16
```

The output of `process` is the expected input to `size`.

#### Manifest format

`process`, `size`, and `move` all operate on a JSON array of records shaped
like:

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
when empty/zero. `process` fills in `status`, `width`, and `height` (and
`error`, on decode failure) — `status` ends up one of `missing`,
`non-image`, `invalid`, `processed`, or `failed` (see above); `size` only
reads records with `status: "processed"`.

`file` is usually an absolute path, but a manifest co-located with the
images it describes (one per directory, the layout `view -move-all`'s
manifest sync expects — see `-move-all` above) may instead store a bare
filename, resolved relative to the manifest's own directory. Move All and
`move` both accept either form, resolving a bare filename against the
manifest's own directory before touching the file it names. A single JSON
object with the same fields, rather than an array, is also accepted
wherever Move All or `move` look for a manifest, for a directory tracking
exactly one entry. `process` goes a step further and rewrites every bare
filename to an absolute path in its output (see above), so manifests that
have been through `process` always have `file` as an absolute path; `move`
removes, rather than rewrites, the specific entries it actually moves (see
below), leaving every other entry's `file` exactly as it was read, in
whichever of the three shapes below the manifest was written in.

**Directory scope.** A manifest only ever describes images in its own
directory or a subdirectory of it — never a file that lives elsewhere, even
if some other process once wrote `file` as an absolute path pointing
outside that tree. `process`, `size`, `move`, and view's Move All sync all
enforce this on every manifest they load: an entry whose `file` no longer
exists on disk, or whose `file` resolves outside the manifest's own
directory tree, is dropped — the latter case being exactly what an older
version of this tool left behind, back when a moved entry's `file` was
rewritten in place to point at the destination instead of being removed.
`process` and `size` report how many entries they dropped and why; `size`
and `move` persist the removal back to the manifest file immediately,
before doing anything else with it, so the cleanup isn't limited to just
the entries a particular run happens to touch.

`process`, `size`, and `move` also accept a third shape: `{"entries":
[...]}`, an array wrapped in an object under an `entries` key. This is the
format the sibling [`site-scraper`](../site-scraper) tool always writes (its
`manifest.Manifest` type is `{Entries []Entry}`, never a bare array) —
`site-scraper` already decodes each kept image's dimensions itself, so
feeding its manifest.json through `process` re-decodes and cross-checks
those dimensions rather than filling in new ones. `site-scraper` also
writes `status` values of its own (`kept`, `duplicate-of:<hash>`,
`download-failed:<error>`) that don't match any of `process`'s five
statuses; that's fine — anything other than `missing`, `non-image`, or
`invalid` is treated as unprocessed and `process` overwrites it with
`processed` or `failed` once it has decoded (or failed to decode) the
file. A `duplicate-of:`/`download-failed:` entry has no `file` at all, so
it correctly lands on `invalid` rather than being queued for decoding.

## Viewer UI Reference

The GUI opened by `view`, `size`, and `move -preview` shares one
`Session`/`Browser` implementation (`internal/viewer`). Once a window is
open:

**Thumbnail cache.** The first time a directory's images are shown as
thumbnails, a `cached_thumbnails` folder is created next to them (one per
directory, not centralized), holding a resized JPEG per source image named
after that image's BLAKE3 content hash. Re-opening the same directory later
reuses those files instead of re-decoding and resizing every image. Because
the filename is a content hash, an edited image simply gets a new filename
on next view rather than needing any staleness check against a stale entry.
Moving an image (`organize`, `view -move-all`, or `move`) relocates its
cached thumbnail into the destination's own `cached_thumbnails` folder along
with it, so a moved image doesn't need re-thumbnailing or leave an orphaned
cache entry behind. Deleting a `cached_thumbnails` folder is always safe —
it's regenerated on demand.

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
- `q` / `Q` or `Esc` — quit the review sequence (or close the window, outside
  a review sequence)
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
  different folder, without restarting the command, via a text field or a
  native folder picker
- **Quit** — stop the whole review sequence without moving anything

**Folder-browse toolbar** (bottom of the window, shown only for a pattern-less
`view` with no search/review batch):
- An editable path field — press Enter or click Refresh to reload whatever
  path is typed
- **Choose Directory** — opens a native folder picker
- **Refresh** — reload the current path
- **Preview subdirectories** — toggle between a flat single-folder listing
  and a recursive, grouped-by-subdirectory listing

The window's native menu bar also has File → Open Folder / Quit, Settings →
Fullscreen Mode, and About.

## Testing

```bash
make test
```

Equivalent to `go test ./...`, which runs every `_test.go` file across all
packages, including `internal/count`, `internal/images`, `internal/manifest`,
`internal/move`, `internal/paths`, `internal/process`, `internal/size`,
`internal/viewer`, and `internal/filedialog`.

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
macOS using the metadata in `FyneApp.toml`. Pass a different `-os` (e.g.
`linux`, `windows`) to package for another platform.

## Project Layout

```
cmd/image-browser/       main() and command dispatch
internal/images/         image discovery, hashing, move/dedupe, per-directory thumbnail cache
internal/organize/       `organize` command
internal/move/           `move` command: manifest field filtering, multi-manifest move, GUI preview
internal/count/          `count` command
internal/size/           `size` command
internal/process/        `process` command
internal/manifest/       shared manifest Entry/Status types, shape-preserving load/save, Move All sync
internal/viewer/         Fyne Session/Browser: grid, single-image view, toolbars, shortcuts
internal/filedialog/     native folder-picker dialog wrapper
internal/trash/          OS-specific "move to Trash" implementation
internal/paths/          path expansion (`~`) and default-root helpers
```

Only `organize`, `count`, and `process` are pure CLI tools with no Fyne
dependency; `view` and `size` both build on `internal/viewer`, and so does
`move`, for its optional `-preview` mode.
