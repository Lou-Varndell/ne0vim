# Image Browser

A Fyne-based desktop tool for browsing, searching, organizing, and de-duplicating
large collections of images on disk. It ships as a single binary with five
subcommands: `organize`, `view`, `count`, `size`, and `process`.

Two of the subcommands (`view` and `size`) open a graphical window built on
[Fyne](https://fyne.io/); the other three (`organize`, `count`, `process`) are
plain CLI tools that print to stdout/stderr.

## Table of Contents

- [Building](#building)
- [Running](#running)
- [Subcommands](#subcommands)
  - [`view`](#view)
  - [`organize`](#organize)
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
  in the destination.

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

Both `process` and `size` operate on a JSON array of records shaped like:

```json
[
  {
    "site": "example.com",
    "source_url": "https://example.com/photo.jpg",
    "file": "/Users/you/Images/photo.jpg",
    "hash": "…",
    "status": "processed",
    "discovered_at": "2026-01-01T00:00:00Z",
    "width": 1920,
    "height": 1080
  }
]
```

`process` fills in `status`, `width`, and `height` (and `error`, on decode
failure); `size` only reads records with `status: "processed"`.

## Viewer UI Reference

The GUI opened by `view` and `size` shares one `Session`/`Browser`
implementation (`internal/viewer`). Once a window is open:

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

**Review toolbar** (bottom of the window, shown for search-driven `view` and
for every `size` batch):
- **Continue** — accept the current batch/group and move to the next
- **Trash Batch** — (only when trash is allowed for that batch) send every
  file currently shown to the Trash, then Continue
- **Move All** — (only when `-move-all` was set) move every matched image to
  the configured destination, after a confirmation dialog
- **Quit** — stop the whole review sequence

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
packages, including `internal/count`, `internal/images`, `internal/paths`,
`internal/process`, `internal/size`, `internal/viewer`, and
`internal/filedialog`.

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
internal/images/         image discovery, hashing, move/dedupe, thumbnail disk cache
internal/organize/       `organize` command
internal/count/          `count` command
internal/size/           `size` command
internal/process/        `process` command
internal/manifest/       shared manifest Entry/Status types
internal/viewer/         Fyne Session/Browser: grid, single-image view, toolbars, shortcuts
internal/filedialog/     native folder-picker dialog wrapper
internal/trash/          OS-specific "move to Trash" implementation
internal/paths/          path expansion (`~`) and default-root helpers
```

Only `organize`, `count`, and `process` are pure CLI tools with no Fyne
dependency; `view` and `size` both build on `internal/viewer`.
