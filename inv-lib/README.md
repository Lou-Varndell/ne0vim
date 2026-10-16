# inv-lib

`inv-lib` (Go package `invlib`) is a Go library for creating, migrating, and
querying a SQLite-backed media inventory database. It is the shared storage
layer underneath a family of file-cataloging tools (a filesystem scanner, a
site-scraper/importer, and an image browser) that all need to track the same
set of physical files, their media metadata, where they came from, and how
they're tagged.

`invlib.Open` embeds and applies `schema.sql` and `indexes.sql` directly from
the compiled binary (via `//go:embed`), so a consumer only needs to import
the package and call `Open` — no separate migration step or SQL file to ship
alongside it.

> <!-- PLACEHOLDER: confirm which repos/binaries actually import this module
> (filesystem scanner, site-scraper, image-browser were inferred from
> comments in the source — name them precisely here) and link them. -->

## Status

<!-- PLACEHOLDER: state whether this library is pre-release, internal-only,
versioned/tagged, etc. The module path in go.mod is the bare name "inv-lib"
rather than a VCS-style path (e.g. github.com/you/inv-lib), which normally
means it's consumed via a local replace directive rather than `go get`.
Confirm and document the actual consumption method here. -->

## Installation

```bash
go get inv-lib   # PLACEHOLDER: replace with the real module path once set
```

```go
import invlib "inv-lib"
```

## Quick start

```go
package main

import (
	"context"
	"log"

	invlib "inv-lib"
)

func main() {
	ctx := context.Background()

	path, err := invlib.DefaultPath() // ~/.config/inventory-manager/db/inv.db
	if err != nil {
		log.Fatal(err)
	}

	db, err := invlib.Open(ctx, path)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	f := &invlib.File{
		Path:     "/photos/vacation/beach.jpg",
		Filename: "beach.jpg",
		Filesize: 2048576,
	}
	if err := db.CreateFile(ctx, f); err != nil {
		log.Fatal(err)
	}
	log.Printf("inserted file %d, scanned at %s", f.ID, f.ScannedAt)
}
```

`Open` is idempotent: every `CREATE TABLE`/`CREATE INDEX` statement is
guarded with `IF NOT EXISTS`, so calling it against an already-migrated
database is a no-op after the (also idempotent) legacy-column repair runs.

## Design overview

- **SQLite via `modernc.org/sqlite`** — a pure-Go SQLite driver, so consumers
  don't need cgo or a system SQLite library to build.
- **One `DB` type wraps `*sql.DB`.** `invlib.DB` embeds `*sql.DB` directly, so
  callers can drop down to `Begin`, `Ping`, raw `Query`/`Exec`, etc. whenever
  the CRUD helpers below aren't enough.
- **Per-connection pragmas via the DSN**, since SQLite pragmas are
  connection-scoped rather than database-wide and `database/sql` pools
  multiple connections:
  - `_pragma=foreign_keys(1)` — enforce declared foreign keys (e.g. the
    `ON DELETE CASCADE` relationships below) on every connection.
  - `_pragma=busy_timeout(5000)` — wait up to 5s for a lock instead of
    failing immediately when another connection is writing.
  - `_texttotime=true` — scan `TEXT`-stored datetimes directly into
    `time.Time` fields.
- **Nullable columns are modeled as Go pointers** (`*string`, `*int64`,
  `*time.Time`, etc.) throughout `models.go`, so a `nil` pointer round-trips
  as SQL `NULL` and a non-nil pointer round-trips as that value — there's no
  separate "is this set" flag to keep in sync.
- **Migration happens in three ordered phases** on every `Open` call (see
  `db.go`'s `migrate` and `legacy_migrate.go`):
  1. Apply `schema.sql` (creates tables that don't exist yet).
  2. Repair legacy column shapes left behind by two pre-`inv-lib` tools that
     each maintained their own, byte-identical copy of this schema's
     migration code (see [Legacy column migration](#legacy-column-migration)
     below).
  3. Apply `indexes.sql` — deliberately last, since several indexes
     reference columns (`hash`, `origin_id`, `status`) that step 2 may need
     to add first.

## Database schema

The database is a single SQLite file containing six tables. `files` is the
hub: every other table either extends a file one-to-one (`images`, `videos`,
`audio`, `media_probe`) or relates files many-to-many (`tags` via
`file_tags`), and `origins` is an optional many-to-one lookup `files` points
into.

```
origins ◄───────── files ─────┬──► images        (1:1)
  (0..1 per file)              ├──► videos        (1:1)
                               ├──► audio         (1:1)
                               ├──► media_probe   (1:1)
                               └──► file_tags ──► tags  (M:N)
```

### `origins`

Where a file came from, if known. A file may have no origin at all
(`files.origin_id IS NULL`) — this table is only populated by manifest- or
site-scraper-driven ingestion, never by a plain filesystem sync.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | `INTEGER` | PK | Row identifier. |
| `type` | `TEXT` | No | What kind of origin this is (e.g. a scrape vs. a manual import). <!-- PLACEHOLDER: enumerate the actual set of `type` values the ingesting tools write. --> |
| `site` | `TEXT` | Yes | The site or source the file was pulled from. Part of the `(site, identifier)` unique key. |
| `url` | `TEXT` | Yes | The URL the file was fetched from, if applicable. |
| `identifier` | `TEXT` | Yes | The source's own identifier for this item (e.g. a listing or post ID). Part of the `(site, identifier)` unique key. |
| `metadata` | `TEXT` | Yes | Freeform metadata about the origin. <!-- PLACEHOLDER: confirm whether this is expected to be JSON, and document its shape if so. --> |
| `original_file` | `TEXT` | Yes | The filename a scraper run would have given this file before collision-resolution renamed it on disk — manifest.json provenance. |
| `discovered_at` | `DATETIME` | Yes | When the scraper run that produced `original_file` discovered this item. |

**Constraints:** `UNIQUE (site, identifier)`. Per SQLite semantics, a `NULL`
in either column is treated as distinct from every other `NULL` for
uniqueness purposes — so two origins with no `site`/`identifier` set don't
collide, and `EnsureOrigin` (see below) always inserts a fresh row in that
case rather than matching an existing one.

### `files`

**The central table of the library.** One row represents one physical file
in the library — a photo, video, audio clip, or any other file the owning
tool chooses to track.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | `INTEGER` | PK | Row identifier. |
| `path` | `TEXT` | No | The file's full path on disk. Globally unique — a given path can only ever map to one row. |
| `filename` | `TEXT` | No | The file's base name (the last path component), kept redundant with `path` for convenient querying/display. |
| `extension` | `TEXT` | Yes | The file's extension (e.g. `jpg`), if known. |
| `mime_type` | `TEXT` | Yes | The file's detected MIME type. |
| `filesize` | `INTEGER` | No | File size in bytes. |
| `hash` | `TEXT` | Yes | A content fingerprint used to find duplicate files via `ListFilesByHash`. **Not unique** — multiple rows may share a hash (that's the point: it's how duplicates are detected), and it may be absent if hashing hasn't run yet. Still BLAKE3 (hex-encoded), as the legacy `blake3` column name implied — computed via this package's own `HashFile`, the single hashing implementation shared by every consumer (site-scraper's dedup pass, image-browser's move/organize duplicate check) instead of each maintaining its own. |
| `origin_id` | `INTEGER` | Yes | Foreign key to `origins.id`. `NULL` means the file has no known origin (e.g. it was found by a filesystem-only scan). Not cascaded on delete — see Deletion behavior below. |
| `created_at` | `DATETIME` | Yes | The file's creation time, as reported by the filesystem. |
| `modified_at` | `DATETIME` | Yes | The file's last-modified time, as reported by the filesystem. |
| `scanned_at` | `DATETIME` | No (defaults to `CURRENT_TIMESTAMP`) | When this row was last (re-)observed by a scan. Set automatically on insert; refreshed by `TouchScanned`. |
| `last_verified_at` | `DATETIME` | Yes | The last time something confirmed the file still exists / is intact. <!-- PLACEHOLDER: confirm exactly what operation sets this and how it differs in intent from `scanned_at`. --> |
| `missing_since` | `DATETIME` | Yes | Set when a scan expects the file but can't find it on disk; `NULL` while the file is known to be present. <!-- PLACEHOLDER: confirm which code path sets/clears this — it isn't touched by anything in this library itself. --> |
| `status` | `TEXT` | Yes | The outcome of a local processing step (e.g. image-browser's `process` command recording a decode result). Never set by import/ingest — provenance from ingestion lives on `origins` instead. <!-- PLACEHOLDER: enumerate the actual set of status values a consumer writes. --> |
| `error` | `TEXT` | Yes | The error message associated with `status`, when that processing step failed. |

**Constraints:** `UNIQUE (path)`.

**Deletion behavior:** Deleting a `files` row cascades (`ON DELETE CASCADE`)
to its `images`, `videos`, `audio`, `media_probe`, and `file_tags` rows.
Deleting an `origins` row that files reference does **not** cascade —
referencing files simply keep their (now dangling) `origin_id`; see
`DeleteOrigin`'s doc comment in `origins.go`.

### `images`

A one-to-one extension of `files`, holding image-specific metadata. Only
files recognized as images get a row here — most files will have no
matching `images` row at all.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK, FK → `files.id` | The file this row describes. `ON DELETE CASCADE`. |
| `width` | `INTEGER` | Yes | Image width in pixels. |
| `height` | `INTEGER` | Yes | Image height in pixels. |
| `format` | `TEXT` | Yes | The decoded image format (e.g. `jpeg`, `png`). <!-- PLACEHOLDER: confirm the exact value set/casing a consumer writes here. --> |
| `color_space` | `TEXT` | Yes | The image's color space. <!-- PLACEHOLDER: confirm the exact value set/casing a consumer writes here (e.g. `srgb`, `cmyk`). --> |

### `videos`

A one-to-one extension of `files`, holding video-specific metadata.
Populated separately from file discovery — rows here are only backfilled by
a dedicated `ffprobe` pass, so a freshly-scanned video file may exist in
`files` with no matching `videos` row yet.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK, FK → `files.id` | The file this row describes. `ON DELETE CASCADE`. |
| `duration_ms` | `INTEGER` | Yes | Total duration, in milliseconds. |
| `width` | `INTEGER` | Yes | Video frame width in pixels. |
| `height` | `INTEGER` | Yes | Video frame height in pixels. |
| `video_codec` | `TEXT` | Yes | The video stream's codec (e.g. `h264`). |
| `video_profile` | `TEXT` | Yes | The codec profile (e.g. `High`). |
| `video_level` | `TEXT` | Yes | The codec level (e.g. `4.1`). |
| `pix_fmt` | `TEXT` | Yes | The pixel format (e.g. `yuv420p`). |
| `fps` | `REAL` | Yes | Frames per second. |
| `video_bitrate` | `INTEGER` | Yes | Video stream bitrate, in bits per second. <!-- PLACEHOLDER: confirm the exact unit ffprobe's value is stored in. --> |
| `audio_codec` | `TEXT` | Yes | The embedded audio stream's codec, if the video has one. |
| `audio_channels` | `INTEGER` | Yes | Number of audio channels in the embedded audio stream. |
| `audio_sample_rate` | `INTEGER` | Yes | Sample rate (Hz) of the embedded audio stream. |
| `audio_bitrate` | `INTEGER` | Yes | Bitrate of the embedded audio stream, in bits per second. <!-- PLACEHOLDER: confirm unit. --> |

### `audio`

A one-to-one extension of `files`, holding metadata for standalone audio
files (as distinct from the audio track embedded in a video, which is
recorded on `videos` instead). Like `videos`, this is only populated by a
separate `ffprobe` pass.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK, FK → `files.id` | The file this row describes. `ON DELETE CASCADE`. |
| `duration_ms` | `INTEGER` | Yes | Total duration, in milliseconds. |
| `codec` | `TEXT` | Yes | The audio codec (e.g. `aac`, `flac`). |
| `sample_rate` | `INTEGER` | Yes | Sample rate, in Hz. |
| `channels` | `INTEGER` | Yes | Number of audio channels. |
| `bitrate` | `INTEGER` | Yes | Bitrate, in bits per second. <!-- PLACEHOLDER: confirm unit. --> |

### `media_probe`

A one-to-one extension of `files` holding the **raw** `ffprobe` output for a
file, if it was ever captured — a durable escape hatch for data that
`videos`/`audio`'s structured columns don't (yet) expose.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK, FK → `files.id` | The file this row describes. `ON DELETE CASCADE`. |
| `ffprobe_json` | `TEXT` | No | The complete raw JSON output of `ffprobe` for this file. |
| `probed_at` | `DATETIME` | No (defaults to `CURRENT_TIMESTAMP`) | When this probe was captured. Refreshed automatically whenever `UpsertMediaProbe` overwrites an existing row. |

### `tags`

The distinct set of tag names known to the library. Tag rows are created on
first use and are **not** deleted when the last file referencing them is
untagged — a tag only disappears via an explicit `DeleteTag` call.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | `INTEGER` | PK | Row identifier. |
| `name` | `TEXT` | No | The tag's name. Always stored trimmed and lowercased — see [Tag name normalization](#tag-name-normalization) below. |

**Constraints:** `UNIQUE (name)`.

### `file_tags`

A plain many-to-many junction table associating `files` with `tags`.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK (composite), FK → `files.id` | The tagged file. `ON DELETE CASCADE`. |
| `tag_id` | `INTEGER` | PK (composite), FK → `tags.id` | The tag applied to the file. `ON DELETE CASCADE`. |

**Constraints:** `PRIMARY KEY (file_id, tag_id)` — a file can't be tagged
with the same tag twice (and `TagFile` relies on this: duplicate tagging is
a no-op, not an error).

**Indexes:** `idx_file_tags_tag_id` on `tag_id`, so "every file with tag X"
lookups don't require a full table scan.

## Indexes

Defined in `indexes.sql`, applied last in the migration sequence (see
[Design overview](#design-overview)):

| Index | Column(s) | Purpose |
|---|---|---|
| `idx_files_hash` | `files(hash)` | Speeds up `ListFilesByHash` duplicate-detection lookups. |
| `idx_files_mime_type` | `files(mime_type)` | Speeds up filtering files by MIME type. |
| `idx_files_origin_id` | `files(origin_id)` | Speeds up finding every file for a given origin. |
| `idx_files_status` | `files(status)` | Speeds up filtering files by processing status. |
| `idx_files_path_sortkey` | `files(replace(path, '/', char(1)))` | An expression index backing a sorted path range-scan sync. Replacing `/` with `char(1)` (the lowest byte any filename can contain) makes SQL's lexical ordering agree with `filepath.WalkDir`'s depth-first, lexically-sorted-siblings traversal order in every case — including when a directory name is a prefix of a sibling's name plus a character like `-` or `.` that sorts below `/` in plain byte order (e.g. sibling directories `2024` and `2024-edited`). <!-- PLACEHOLDER: name/link the "sync" package (`sync.QueryPathRange` / `store.FilesInPathRange`) this index exists for — it lives outside this repo. --> |
| `idx_file_tags_tag_id` | `file_tags(tag_id)` | Defined in `schema.sql` alongside the table itself; speeds up "every file with tag X" lookups. |

## Legacy column migration

`legacy_migrate.go` repairs databases created by two **pre-`inv-lib`** tools
(a site-scraper and an image-browser) that each maintained their own,
byte-identical copy of this migration logic before being consolidated onto
this shared library. It is purely historical and has no effect on a
brand-new database:

- Renames a `files.blake3` column to `hash` (or, if both somehow exist on a
  database two older tools each migrated independently, backfills `hash`
  from `blake3` and drops `blake3`).
- Adds `files.origin_id`, `files.last_verified_at`, `files.missing_since`,
  `files.status`, and `files.error` if missing.
- Drops legacy flat-schema columns `files.site`, `files.source_url`,
  `files.original_file`, and `files.discovered_at` — superseded by the
  normalized `origins` table.
- Adds `origins.original_file` and `origins.discovered_at` if missing.

<!-- PLACEHOLDER: this is "safe to delete in a future release once no
pre-cutover database files remain in the wild" per the source comment —
track that removal here once it happens. -->

## Tag name normalization

Tag names are always folded through `normalizeTagName` (trim whitespace,
lowercase) before being matched or stored, so `"Beach"`, `"beach"`, and
`" beach "` are all treated as the same tag. This favors consistent
free-text tagging (as typed into a comma-separated field) over
case-sensitive distinctions a photo library is less likely to need.

## Package API

### Opening a database

| Function | Description |
|---|---|
| `Open(ctx, path) (*DB, error)` | Opens (creating if necessary) the SQLite database at `path`, applying schema, legacy-column repair, and indexes. Safe to call repeatedly against the same path. |
| `DefaultPath() (string, error)` | Returns `~/.config/inventory-manager/db/inv.db`, creating its parent directory. Override with the `INVENTORY_MANAGER_DB` environment variable (tests should always do this, pointed at `t.TempDir()`, to avoid touching the real shared database). |

### Hashing (`hash.go`)

| Function | Description |
|---|---|
| `HashFile(path) (string, error)` | Returns the hex-encoded BLAKE3 digest of the file at `path`. The single hashing implementation shared by every consumer that needs a `files.hash` value or a content-based duplicate check — site-scraper's dedup pass and image-browser's move/organize duplicate check both call this instead of maintaining their own BLAKE3 plumbing. |

### Files (`files.go`)

| Function | Description |
|---|---|
| `CreateFile(ctx, f *File) error` | Inserts `f`, sets `f.ID`, and backfills `f.ScannedAt` from the column default. |
| `GetFile(ctx, id) (*File, error)` | Fetches a file by id. |
| `GetFileByPath(ctx, path) (*File, error)` | Fetches a file by its unique path. |
| `ListFilesByHash(ctx, hash) ([]*File, error)` | Finds every file sharing a content hash — duplicate detection. |
| `ListFiles(ctx) ([]*File, error)` | Returns every file, ordered by id. |
| `UpdateFile(ctx, f *File) error` | Overwrites every column (except `scanned_at`) for `f.ID`. |
| `EnsureFile(ctx, f *File) (inserted bool, error)` | Inserts `f` if its path is new; otherwise leaves the existing row untouched and sets `f.ID` to it. |
| `UpdateFilePath(ctx, id, path, filename) error` | Updates only `path`/`filename` — for a file that moved on disk. |
| `UpdateFileStatus(ctx, id, status, errMsg *string) error` | Updates only `status`/`error`. |
| `UpdateFileOrigin(ctx, id, originID *int64) error` | Updates only `origin_id`. |
| `CreateFilesTx(ctx, files []*File) error` | Bulk-inserts files in one transaction, skipping any whose path already exists; sets each `f.ID`. |
| `TouchScanned(ctx, id) error` | Sets `scanned_at` to now. |
| `DeleteFile(ctx, id) error` | Deletes a file; cascades to `images`/`videos`/`audio`/`media_probe`/`file_tags`. |

### Origins (`origins.go`)

| Function | Description |
|---|---|
| `CreateOrigin(ctx, o *Origin) error` | Inserts `o` and sets `o.ID`. |
| `GetOrigin(ctx, id) (*Origin, error)` | Fetches an origin by id. |
| `GetOriginBySiteIdentifier(ctx, site, identifier) (*Origin, error)` | Fetches an origin by its unique `(site, identifier)` key. |
| `EnsureOrigin(ctx, o *Origin) (inserted bool, error)` | Inserts `o` if no origin matches `(o.Site, o.Identifier)` yet; otherwise sets `o.ID` to the existing row. |
| `ListOrigins(ctx) ([]*Origin, error)` | Returns every origin, ordered by id. |
| `UpdateOrigin(ctx, o *Origin) error` | Overwrites every column for `o.ID`. |
| `DeleteOrigin(ctx, id) error` | Deletes an origin. Referencing files keep their (now dangling) `origin_id` — not cascaded. |

### Images (`images.go`)

| Function | Description |
|---|---|
| `UpsertImage(ctx, img *Image) error` | Inserts or overwrites the image row for `img.FileID`. |
| `InsertImageIfAbsent(ctx, img *Image) (inserted bool, error)` | Inserts only if no row exists yet; never overwrites. |
| `GetImage(ctx, fileID) (*Image, error)` | Fetches the image row for a file. |
| `DeleteImage(ctx, fileID) error` | Removes the image row without touching the file itself. |

### Videos (`videos.go`)

| Function | Description |
|---|---|
| `UpsertVideo(ctx, v *Video) error` | Inserts or overwrites the video row for `v.FileID`. |
| `GetVideo(ctx, fileID) (*Video, error)` | Fetches the video row for a file. |
| `DeleteVideo(ctx, fileID) error` | Removes the video row without touching the file itself. |

### Audio (`audio.go`)

| Function | Description |
|---|---|
| `UpsertAudio(ctx, a *Audio) error` | Inserts or overwrites the audio row for `a.FileID`. |
| `GetAudio(ctx, fileID) (*Audio, error)` | Fetches the audio row for a file. |
| `DeleteAudio(ctx, fileID) error` | Removes the audio row without touching the file itself. |

### Media probe (`mediaprobe.go`)

| Function | Description |
|---|---|
| `UpsertMediaProbe(ctx, p *MediaProbe) error` | Inserts or overwrites raw `ffprobe` JSON for `p.FileID`; refreshes `probed_at` on overwrite. |
| `GetMediaProbe(ctx, fileID) (*MediaProbe, error)` | Fetches the probe row for a file. |
| `DeleteMediaProbe(ctx, fileID) error` | Removes the probe row. |

### Tags (`tags.go`)

| Function | Description |
|---|---|
| `GetOrCreateTag(ctx, name) (*Tag, error)` | Looks up a tag by its normalized name, creating it if absent. |
| `GetTagByName(ctx, name) (*Tag, error)` | Looks up a tag by exact (already-normalized) name. |
| `ListTags(ctx) ([]*Tag, error)` | Returns every tag, ordered by name. |
| `DeleteTag(ctx, id) error` | Deletes a tag and every `file_tags` row referencing it; does not touch tagged files. |
| `TagFile(ctx, fileID, tagID) error` | Associates a tag with a file. Idempotent. |
| `UntagFile(ctx, fileID, tagID) error` | Removes a tag association, if present. |
| `ListTagsForFile(ctx, fileID) ([]*Tag, error)` | Returns every tag on a file, ordered by name. |
| `SetFileTags(ctx, fileID, names []string) error` | Replaces a file's entire tag set with `names`, creating new tags as needed. |
| `ListFilesForTag(ctx, tagID) ([]*File, error)` | Returns every file carrying a given tag, ordered by id. |

### Composite file records (`filerecords.go`)

Higher-level read helpers for UI-facing use cases that need a file alongside
its related rows in one round trip, rather than N+1 separate calls.

| Function | Description |
|---|---|
| `ListFileRecords(ctx, q FileQuery) ([]*FileRecord, error)` | Returns files matching `q` (see fields below), each left-joined with its `Origin` and `Image`, ordered by path. Supports `Limit`/`Offset` pagination and optional batch tag loading. |
| `CountFileRecords(ctx, q FileQuery) (int, error)` | Counts files matching `q`, ignoring `Limit`/`Offset` — for "page X of Y" UI without fetching every row. |
| `ListImageResolutions(ctx, pathPrefix string) ([]ResolutionGroup, error)` | Returns width/height/path for every file with a decoded `images` row, optionally restricted to a path prefix. |

**`FileQuery` fields** (zero value matches every file):

| Field | Type | Description |
|---|---|---|
| `PathPrefix` | `string` | Case-insensitive prefix match against `path`. |
| `Path` | `string` | Case-insensitive substring match against `path`. Independent of `PathPrefix` — both apply if both are set. |
| `Site` | `string` | Case-insensitive substring match against the file's origin `site`. |
| `Tag` | `string` | Case-insensitive substring match against the file's tag names. |
| `RequireImage` | `bool` | Only files **with** an `images` row (inner join). Mutually exclusive with `MissingImage`. |
| `MissingImage` | `bool` | Only files **without** an `images` row (anti-join). Mutually exclusive with `RequireImage`. |
| `WithTags` | `bool` | Batch-attach each result's `Tags`. Left `false` by default to skip the extra query when callers don't need tags. |
| `Limit` | `int` | Caps the number of results (`ListFileRecords` only; `0` = no limit). |
| `Offset` | `int` | Skips this many matches before collecting `Limit` of them. |

**`FileRecord` fields:** `File *File`, `Origin *Origin` (nil if the file has
no `origin_id`), `Image *Image` (nil if the file has no `images` row),
`Tags []*Tag` (nil unless `FileQuery.WithTags` was set).

## Go types (`models.go`)

Each type below mirrors its table one-to-one; nullable SQL columns are Go
pointer fields (`nil` ⇄ SQL `NULL`).

| Type | Mirrors table |
|---|---|
| `Origin` | `origins` |
| `File` | `files` |
| `Image` | `images` |
| `Video` | `videos` |
| `Audio` | `audio` |
| `MediaProbe` | `media_probe` |
| `Tag` | `tags` |

(`file_tags` has no corresponding struct — it's a pure junction table,
represented in the API as `[]*Tag` / `[]*File` association helpers instead.)

## Testing

```bash
go test ./...
```

Tests open a throwaway database via `t.TempDir()` (see `openTestDB` in
`invlib_test.go`) rather than touching the real `DefaultPath()` location.
Test files: `invlib_test.go` (open/migrate + file CRUD), `newapi_test.go`,
`legacy_migrate_test.go` (legacy-column repair paths), and
`filerecords_test.go` (composite query helpers).

## Dependencies

| Module | Purpose |
|---|---|
| [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) | Pure-Go (no cgo) SQLite driver. |
| [`github.com/zeebo/blake3`](https://pkg.go.dev/github.com/zeebo/blake3) | BLAKE3 hashing for `HashFile`. |

Everything else in `go.mod` is a transitive dependency of the two above.
Requires Go 1.27.1+ per `go.mod`. <!-- PLACEHOLDER: confirm whether 1.27.1
is a hard requirement or just whatever was installed when go.mod was
generated — it's a very recent toolchain version to pin as a floor. -->

## License

<!-- PLACEHOLDER: no LICENSE file exists in this repo yet — add one and
reference it here. -->
