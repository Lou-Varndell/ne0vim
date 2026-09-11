# image-browser-go

Go/Fyne image browser and image-management utility converted from the original Python/PySide6 application.

The application is designed to work with time-boxed image-download sessions. A session directory contains the downloaded files and an `image-manifest.json` describing where each image came from. `image-browser` provides tools for processing, previewing, organizing, and cleaning those images.

## Build

```bash
go mod tidy
go test ./...
go build -o image-browser ./cmd/image-browser
```

## Commands

### `view`

Search for images whose path contains a pattern and display them in the Fyne viewer.

```bash
./image-browser view "porch"
./image-browser view "porch" --root ~/Images
```

The viewer supports grid and 1:1 image views, navigation, and trashing the current image.

### `count`

Count images in each immediate subdirectory of a root directory.

```bash
./image-browser count
./image-browser count --root ~/Images
```

The image count is for each directory independently. Images in descendant directories are not included in the directory's own count.

The output is zero-padded so it remains convenient for command-line filtering:

```text
00003 /path/to/directory
00012 /path/to/another-directory
00030 /path/to/third-directory
```

`--image-count` can select either an exact count or an inclusive range:

```bash
./image-browser count --image-count 30
./image-browser count --image-count 10,30
```

`--no-subdirs` limits results to directories containing no subdirectories:

```bash
./image-browser count --image-count 10,30 --no-subdirs
```

These options make common cleanup workflows possible without additional filtering:

```bash
./image-browser count --image-count 30
./image-browser count --image-count 10,30 --no-subdirs
```

### `process`

Process an image manifest and add image dimensions to successfully decoded images.

```bash
./image-browser process -input image-manifest.json
./image-browser process -input /path/to/image-manifest.json
```

The processed manifest is written by prepending `processed-` to the input filename:

```text
image-manifest.json
processed-image-manifest.json
```

The processor uses the shared `internal/manifest.Entry` type so manifest fields are preserved when the JSON is rewritten. This includes fields such as:

- `site`
- `source_url`
- `file`
- `hash`
- `status`
- `error`
- `original_file`
- `discovered_at`
- `width`
- `height`

Files that are missing are removed from the processed record set. Files that exist but are not supported images are retained with:

```json
"status": "non-image"
```

This preserves the session manifest's record of what the scraper downloaded while preventing non-images from being treated as valid processed images.

### `size`

Group processed images by exact width and height.

```bash
./image-browser size image-manifest.json
./image-browser size --largest image-manifest.json
./image-browser size --smallest image-manifest.json
```

The default is smallest resolution first.

Only successfully processed image records whose files still exist are included. Missing files and records that do not represent successfully processed images are excluded.

Images can be previewed in batches:

```bash
./image-browser size --largest --batch-size 40 image-manifest.json
```

`--trash` enables the option to move a reviewed batch to Trash:

```bash
./image-browser size --largest --trash image-manifest.json
```

The Fyne viewer handles continuing through groups and exiting; no terminal prompt is required between groups.

### `organize`

Find images by path pattern and organize them into a destination directory.

```bash
./image-browser organize -dest ~/Pictures/Organized "porch"
./image-browser organize -dest ~/Pictures/Organized -execute "porch"
```

Without `-execute`, the operation can be reviewed without performing the moves. With `-execute`, matching files are moved.

Filename collisions are not automatically treated as duplicates.

Duplicate detection is content-based:

1. The source file size is checked.
2. Only destination files with the same size are considered for hashing.
3. Matching BLAKE3 hashes mean the file is a duplicate and it is skipped.
4. If the content is different, a filename collision is resolved by renaming the incoming file:
   `image.jpg`, `image-2.jpg`, `image-3.jpg`, etc.

Duplicate detection only examines files directly inside the destination directory. It does not recursively scan the destination.

This means two files with the same name but different content are both retained, while files with different names but identical content are correctly recognized as duplicates.

### `trash`

The trash functionality moves unwanted images into the configured Trash location rather than immediately deleting them.

It is used by the interactive viewer and review workflows.

## Manifest

The session `image-manifest.json` is intentionally kept alongside each download session.

The manifest records both the downloaded file and its source information. The `hash` field is a BLAKE3 content hash and is retained for future uses such as database indexing and efficient duplicate detection.

The project currently has an `internal/manifest` package containing the shared `manifest.Entry` definition. The same manifest definition is also used by the site-scraper project. This is an intermediate step toward eventually sharing the manifest package directly between projects.

The long-term plan is to maintain both:

- the time-boxed session manifest for download/session history and portability
- a database for persistent indexing, searching, duplicate detection, and organization metadata

## Keyboard controls

| Key | Action |
|---|---|
| `←` | Previous image |
| `→` | Next image |
| `Delete` | Trash current image |
| `Backspace` | Trash current image |
| `T` | Trash current image |
| `G` | Toggle grid / 1:1 image |
| `Q` | Quit |
| `Esc` | Quit |

Clicking a thumbnail opens that image in the 1:1 viewer.

## Current workflow

A typical download and cleanup session looks like:

```text
site-scraper
    │
    ▼
session directory
    │
    ├── downloaded images
    └── image-manifest.json
            │
            ▼
        image-browser process
            │
            ├── dimensions
            ├── preserved manifest metadata
            ├── missing-file cleanup
            └── non-image status
                    │
                    ▼
             preview / organize / cleanup
```

The tools are intentionally useful from both the command line and interactive Fyne workflows. The command-line output is kept suitable for Unix pipelines, while the interactive viewer handles image review where visual inspection is more useful.
