# inv-lib

`inv-lib` (Go package `invlib`) is a Go library for creating, migrating, and
querying a SQLite-backed media inventory database, plus two related
filesystem concerns every consumer of that database also needs: an
on-disk thumbnail cache keyed by content hash, and a directory-sharding
helper that keeps a growing collection from piling up in one flat
directory. It is the shared storage layer underneath a family of
file-cataloging tools that all need to track the same set of physical
files, their media metadata, where they came from, and how they're
tagged.

`invlib.Open` embeds and applies `schema.sql` and `indexes.sql` directly from
the compiled binary (via `//go:embed`), so a consumer only needs to import
the package and call `Open` — no separate migration step or SQL file to ship
alongside it.

This repository currently has four sibling consumers, each checked out next
to `inv-lib` and pulling it in via a `replace inv-lib => ../inv-lib` directive
in their own `go.mod`:

| Consumer | What it uses inv-lib for |
|---|---|
| `../site-scraper` | Records every downloaded survivor (`EnsureFile`/`EnsureOrigin`/`InsertImageIfAbsent`/`GetOrCreateTag`/`TagFile`) and shards its staging directory (`NewSharder`). |
| `../image-browser` | The primary consumer of nearly the whole API: CRUD across all tables, `FileQuery`/`ListFileRecords` for every search-driven command, `HashFile` for duplicate detection, `ThumbCache`/`WarmAll` for its GUI thumbnail grid, and `Sharder`/`IsSharded` for its `shard` command. |
| `../inventory-manager` | A second Fyne-based browser/tagger UI over the same database (file CRUD, origins, tags). |
| `../media-site` | A read-mostly web catalog/gallery server (`store.go` wraps `invlib.DB` for catalog queries and `images.Format`). |

## Status

There is no `LICENSE` file in this repository and no version tags or
releases — this directory is not currently under version control at all
(`git status` from inside it reports "not a git repository"). The module
path in `go.mod` is the bare name `inv-lib`, not a VCS-style path like
`github.com/you/inv-lib`, which is consistent with how it's actually
consumed: every sibling module above pulls it in with a local
`replace inv-lib => ../inv-lib` directive rather than `go get`, so it is
not published anywhere and has no install path beyond "checked out as a
sibling directory of whatever imports it."

## Installation

There is no module path to `go get` — `inv-lib` is consumed exclusively via
a local sibling checkout and a `replace` directive, the same way every
current consumer does it. From a new consumer module's directory:

```bash
# inv-lib must already be checked out at ../inv-lib relative to your module
go mod edit -require inv-lib@v0.0.0-00010101000000-000000000000
go mod edit -replace inv-lib=../inv-lib
go mod tidy
```

(Or just copy the two lines above out of any sibling module's `go.mod` —
`site-scraper`, `image-browser`, `inventory-manager`, and `media-site` all
have the identical pair.)

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

`DefaultPath` resolves to `~/.config/inventory-manager/db/inv.db` by
default — creating that directory (but not the database file itself,
which `Open` creates) if it doesn't already exist — or to the value of the
`$INVENTORY_MANAGER_DB` environment variable if it's set, still creating
that path's parent directory either way. Every consumer's test suite (and
`inv-lib`'s own) sets `$INVENTORY_MANAGER_DB` to a path under `t.TempDir()`
so tests never touch a real, shared inventory. A production consumer
normally calls `DefaultPath()` with no override so every tool on one
machine shares the exact same database file.

`Open` is idempotent: every `CREATE TABLE`/`CREATE INDEX` statement is
guarded with `IF NOT EXISTS`, so calling it against an already-migrated
database is a no-op after the (also idempotent) legacy-column repair runs.
Call it once per process (or once per connection you intend to keep open) —
there's no harm in calling it more than once against the same path, but
there's no need to either.

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
- **Read-only access is a separate entry point.** `OpenReadOnly` (see
  [Read-only access](#read-only-access-readonlygo) below) connects without
  running any migration at all, for a caller that only ever wants to run
  arbitrary, untrusted-ish `SELECT`s against an already-migrated database.

## Database schema

The database is a single SQLite file containing seven tables. `files` is the
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

(Seven tables: `origins`, `files`, `images`, `videos`, `audio`,
`media_probe`, `tags` — plus the `file_tags` junction, eight tables total if
you count it separately from `tags`.)

### `origins`

Where a file came from, if known. A file may have no origin at all
(`files.origin_id IS NULL`) — this table is only populated by manifest-,
site-scraper-, or manual-tagging-driven ingestion, never by a plain
filesystem sync (`image-browser`'s `scan` command never creates one).

| Column | Type | Nullable | Description |
|---|---|---|---|
| `id` | `INTEGER` | PK | Row identifier. |
| `type` | `TEXT` | No | Freeform label for what kind of origin this is. Every current writer sets exactly one of two values: `"web"` (`site-scraper`'s downloader, and `image-browser import`'s manifest ingestion) or `"manual"` (`inventory-manager`'s UI, when a user types a site into a file's detail pane and it had no prior origin). No validation or enum is enforced — any string is accepted. |
| `site` | `TEXT` | Yes | The site or source the file was pulled from. Part of the `(site, identifier)` unique key. |
| `url` | `TEXT` | Yes | The URL the file was fetched from, if applicable. |
| `identifier` | `TEXT` | Yes | The source's own identifier for this item (e.g. a listing or post ID). Part of the `(site, identifier)` unique key. |
| `metadata` | `TEXT` | Yes | Freeform metadata about the origin. Plumbed all the way through the CRUD API (`Origin.Metadata`, every `origins.go` function) but **no current consumer ever sets it** — it is always `nil` in practice today. No shape (JSON or otherwise) is enforced or assumed. |
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
| `path` | `TEXT` | No | The file's full (absolute) path on disk. Globally unique — a given path can only ever map to one row. |
| `filename` | `TEXT` | No | The file's base name (the last path component), kept redundant with `path` for convenient querying/display. |
| `extension` | `TEXT` | Yes | The file's extension (e.g. `jpg`), if known. |
| `mime_type` | `TEXT` | Yes | The file's detected MIME type. Plumbed through the CRUD API and indexed (`idx_files_mime_type`), but no current consumer sets it — always `nil` in practice today. |
| `filesize` | `INTEGER` | No | File size in bytes. |
| `hash` | `TEXT` | Yes | A content fingerprint used to find duplicate files via `ListFilesByHash`. **Not unique** — multiple rows may share a hash (that's the point: it's how duplicates are detected), and it may be absent if hashing hasn't run yet. Still BLAKE3 (hex-encoded), as the legacy `blake3` column name implied — computed via this package's own `HashFile`, the single hashing implementation shared by every consumer (`site-scraper`'s dedup pass, `image-browser`'s move/organize/scan duplicate check) instead of each maintaining its own. |
| `origin_id` | `INTEGER` | Yes | Foreign key to `origins.id`. `NULL` means the file has no known origin (e.g. it was found by a filesystem-only scan). Not cascaded on delete — see Deletion behavior below. |
| `created_at` | `DATETIME` | Yes | The file's creation time, as reported by the filesystem. |
| `modified_at` | `DATETIME` | Yes | The file's last-modified time, as reported by the filesystem. |
| `scanned_at` | `DATETIME` | No (defaults to `CURRENT_TIMESTAMP`) | When this row was last (re-)observed by a scan. Set automatically on insert; refreshed by `TouchScanned`. |
| `last_verified_at` | `DATETIME` | Yes | The last time something confirmed the file still exists / is intact. Set exclusively by `image-browser`'s `reconcile` command today — no other consumer touches this column. |
| `missing_since` | `DATETIME` | Yes | Set when a scan expects the file but can't find it on disk; `NULL` while the file is known to be present. Set and cleared exclusively by `image-browser`'s `reconcile` command — `inv-lib` itself never touches it, and no other consumer sets it either (`move`/`size` instead hard-delete a missing file's row outright via `invdb.PruneMissing` in `image-browser`, rather than flagging it here). |
| `status` | `TEXT` | Yes | The outcome of a local image-decode step. The only current writer is `image-browser`'s `process` command, which sets exactly `"processed"` (decode succeeded) or `"failed"` (decode failed) via `UpdateFileStatus` — no other value is written by anything in this family of tools today, and `inv-lib` itself does not enforce or enumerate the set. Never set by import/ingest — provenance from ingestion lives on `origins` instead. |
| `error` | `TEXT` | Yes | The error message associated with `status`, when that processing step failed — set alongside `status = "failed"` by `image-browser process`. |

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
| `format` | `TEXT` | Yes | The decoded image format name, exactly as Go's `image.DecodeConfig` reports it for whichever decoder recognized the file (e.g. `"jpeg"`, `"png"`, `"gif"`, `"webp"`, `"bmp"`, `"tiff"` — lowercase, matching each format's registered name in the standard library / `golang.org/x/image`). |
| `color_space` | `TEXT` | Yes | A short name for the decoded image's Go color model, written by `image-browser`'s own `colorModelName` helper: one of `"rgba"`, `"rgba64"`, `"nrgba"`, `"nrgba64"`, `"alpha"`, `"alpha16"`, `"gray"`, `"gray16"`, `"cmyk"`, `"ycbcr"`, `"nycbcra"`, `"palette"` (a paletted GIF/PNG), or `"unknown"` as a fallback for any model not in that list. |

### `videos`

A one-to-one extension of `files`, holding video-specific metadata. Defined
in the schema and fully plumbed through the CRUD API (`UpsertVideo`,
`GetVideo`, `DeleteVideo`), but **no consumer in any of the four sibling
repos populates it yet** — it exists for a future `ffprobe`-based pass.

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
| `video_bitrate` | `INTEGER` | Yes | Video stream bitrate. No writer exists yet to confirm a unit in practice; `ffprobe`'s own `bit_rate` field (the obvious intended source) reports bits per second. |
| `audio_codec` | `TEXT` | Yes | The embedded audio stream's codec, if the video has one. |
| `audio_channels` | `INTEGER` | Yes | Number of audio channels in the embedded audio stream. |
| `audio_sample_rate` | `INTEGER` | Yes | Sample rate (Hz) of the embedded audio stream. |
| `audio_bitrate` | `INTEGER` | Yes | Bitrate of the embedded audio stream. Same caveat as `video_bitrate` — unpopulated today. |

### `audio`

A one-to-one extension of `files`, holding metadata for standalone audio
files (as distinct from the audio track embedded in a video, which is
recorded on `videos` instead). Like `videos`, this is fully plumbed through
the CRUD API (`UpsertAudio`, `GetAudio`, `DeleteAudio`) but **unpopulated by
every current consumer** — reserved for the same future `ffprobe` effort.

| Column | Type | Nullable | Description |
|---|---|---|---|
| `file_id` | `INTEGER` | PK, FK → `files.id` | The file this row describes. `ON DELETE CASCADE`. |
| `duration_ms` | `INTEGER` | Yes | Total duration, in milliseconds. |
| `codec` | `TEXT` | Yes | The audio codec (e.g. `aac`, `flac`). |
| `sample_rate` | `INTEGER` | Yes | Sample rate, in Hz. |
| `channels` | `INTEGER` | Yes | Number of audio channels. |
| `bitrate` | `INTEGER` | Yes | Bitrate. Same unconfirmed-unit caveat as `videos.video_bitrate` — unpopulated today. |

### `media_probe`

A one-to-one extension of `files` holding the **raw** `ffprobe` output for a
file, if it was ever captured — a durable escape hatch for data that
`videos`/`audio`'s structured columns don't (yet) expose. Like those two
tables, fully plumbed through the CRUD API (`UpsertMediaProbe`,
`GetMediaProbe`, `DeleteMediaProbe`) but unpopulated by any current
consumer.

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
| `idx_files_mime_type` | `files(mime_type)` | Speeds up filtering files by MIME type (currently unused in practice — see `mime_type`'s note above). |
| `idx_files_origin_id` | `files(origin_id)` | Speeds up finding every file for a given origin. |
| `idx_files_status` | `files(status)` | Speeds up filtering files by processing status. |
| `idx_files_path_sortkey` | `files(replace(path, '/', char(1)))` | An expression index intended to back a sorted path range-scan sync — the comment in `indexes.sql` references a `sync.QueryPathRange`/`store.FilesInPathRange` pair, but **no such package exists in any of the four current sibling repos** (`site-scraper`, `image-browser`, `inventory-manager`, `media-site`); nothing queries by this expression today. Replacing `/` with `char(1)` (the lowest byte any filename can contain) makes SQL's lexical ordering agree with `filepath.WalkDir`'s depth-first, lexically-sorted-siblings traversal order in every case — including when a directory name is a prefix of a sibling's name plus a character like `-` or `.` that sorts below `/` in plain byte order (e.g. sibling directories `2024` and `2024-edited`). |
| `idx_file_tags_tag_id` | `file_tags(tag_id)` | Defined in `schema.sql` alongside the table itself; speeds up "every file with tag X" lookups. |

## Legacy column migration

`legacy_migrate.go` repairs databases created by two **pre-`inv-lib`** tools
(`site-scraper` and `image-browser`, before this library existed) that each
maintained their own, byte-identical copy of this migration logic before
being consolidated onto this shared library. It is purely historical and
has no effect on a brand-new database — `schema.sql` has never contained
any of the legacy columns these steps touch:

- Renames a `files.blake3` column to `hash` (or, if both somehow exist on a
  database two older tools each migrated independently, backfills `hash`
  from `blake3`, drops the now-unreferenced `idx_files_blake3` index, and
  drops `blake3`).
- Adds `files.origin_id`, `files.last_verified_at`, `files.missing_since`,
  `files.status`, and `files.error` if missing.
- Drops legacy flat-schema columns `files.site`, `files.source_url`,
  `files.original_file`, and `files.discovered_at` — superseded by the
  normalized `origins` table. Nothing backfills these into `origins` first:
  every live row's values were empty in practice by the time this
  migration was written, so there was nothing to lose by dropping them
  outright.
- Adds `origins.original_file` and `origins.discovered_at` if missing.

Per its own file-header comment, this is "safe to delete in a future
release once no pre-cutover database files remain in the wild" — there is
no tracking issue for that removal in this repository; it's simply still
present because the risk of a pre-cutover file surviving somewhere hasn't
been ruled out.

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

### Read-only access (`readonly.go`)

For a caller that wants to run arbitrary, user-supplied `SELECT` statements
against the shared database — e.g. an "advanced search" box — without any
risk of a crafted or buggy query writing to it.

| Function | Description |
|---|---|
| `OpenReadOnly(ctx, path) (*DB, error)` | Connects to the database at `path` in SQLite's query-only mode (`mode=ro` in the DSN): SQLite itself rejects `INSERT`/`UPDATE`/`DELETE`/`PRAGMA` writes/`ATTACH`/schema changes outright, regardless of what the caller's SQL says. Runs no migration — `path` must already have been through a normal `Open` at some point. Does **not** create a missing database file; opening a path that doesn't exist yet fails rather than creating an empty one (unlike `Open`). |
| `(*DB) QueryFileIDs(ctx, rawSQL string) ([]int64, error)` | Runs `rawSQL` (expected to be a single `SELECT`) and returns the distinct values of its `file_id` or `id` result column (matched case-insensitively against whichever column names the query actually returns), in first-seen order. An error carries SQLite's own message through unmodified — syntax errors, unknown tables/columns, a write rejected by a read-only connection, or a result with neither a `file_id` nor an `id` column are all surfaced this way, since that's the only way a caller can tell the person who supplied `rawSQL` what went wrong. A query that matches zero rows returns an empty, non-nil `[]int64` (not `nil`) — see `FileQuery.FileIDs` below, where that distinction matters: non-nil-empty means "match nothing," while `nil` means "no ID restriction at all." `QueryFileIDs` itself does not enforce read-only-ness; pair it with a `*DB` from `OpenReadOnly` so a write is rejected by SQLite rather than merely discouraged by convention. |

### Hashing (`hash.go`)

| Function | Description |
|---|---|
| `HashFile(path) (string, error)` | Returns the hex-encoded BLAKE3 digest of the file at `path`, streamed through a 1MiB buffer rather than reading the whole file into memory. The single hashing implementation shared by every consumer that needs a `files.hash` value or a content-based duplicate check — `site-scraper`'s dedup pass and `image-browser`'s move/organize/scan duplicate checks all call this instead of maintaining their own BLAKE3 plumbing. |

### Thumbnail cache (`thumbcache.go`)

An on-disk JPEG thumbnail cache shared by every consumer that renders
image grids, keyed by the source image's own BLAKE3 content hash (see
`HashFile`) rather than its path — so a moved or renamed source still
finds its existing thumbnail with nothing to invalidate, and two different
programs thumbnailing the same image land on the identical cache entry.

| Function | Description |
|---|---|
| `DefaultThumbCacheDir(size uint) (string, error)` | Returns `<user cache dir>/inv-lib/thumbnails/<size>` — e.g. `~/Library/Caches/inv-lib/thumbnails/150` on macOS, or `$XDG_CACHE_HOME/inv-lib/thumbnails/150` (falling back to `~/.cache/...`) on Linux — one subdirectory per thumbnail size, since a 150px cache and a 400px cache for the same source are unrelated files. |
| `NewThumbCache(size uint) (*ThumbCache, error)` | Builds a `ThumbCache{Dir: DefaultThumbCacheDir(size), Size: size}`. Touches no disk itself beyond resolving the path — the directory tree is created lazily by `Generate`. |
| `(*ThumbCache) Path(path string) (string, error)` | Returns where the thumbnail for the source file at `path` is (or would be) stored, without generating it — hashes `path` (via `HashFile`) to compute the cache key, so a missing or unreadable source surfaces here rather than later in `Generate`. |
| `(*ThumbCache) Generate(path string, force bool) (string, error)` | Returns the cached thumbnail path for `path`, generating one first if it doesn't exist (or unconditionally, if `force` is `true`). A thumbnail is a JPEG, quality 88, resized with Lanczos3 resampling so its longest edge is `Size` pixels, written atomically (temp file + `os.Rename`, one temp name per call so concurrent `Generate` calls for the same source never collide). For an animated WebP source, thumbnails the first frame, extracted with `ffmpeg` (see `AnimatedWebPFirstFrame`); returns an error wrapping `ErrAnimatedWebP` if `ffmpeg` is missing or fails. |
| `(*ThumbCache) Size`, `.Dir` | Public struct fields: `Dir` is the cache's root directory, `Size` is the configured longest-edge pixel size. |
| `WarmAll(ctx, db, cache, concurrency int, force bool, progress func(done, total int, path string, err error)) (WarmAllResult, error)` | Ensures every file in `db` with an `images` row (via `db.ListImageFiles`, which excludes files already marked `missing_since`) has a cached thumbnail, generating only the missing ones unless `force` is set. Runs with up to `concurrency` thumbnails in flight at once (`concurrency <= 0` uses `runtime.GOMAXPROCS(0)`); `progress`, if non-nil, is called once per file in no particular order as soon as its outcome (success or failure) is known. Returns as soon as `ctx` is canceled, with a partial `WarmAllResult` and `ctx.Err()` — files already in flight finish, but no new ones start. |
| `IsAnimatedWebP(f *os.File) (bool, error)` | Reports whether `f` is an animated WebP by parsing its RIFF/VP8X header directly (checking the animation flag bit), without decoding any pixel data. Leaves `f` positioned at the start for a subsequent `image.Decode` call. |
| `AnimatedWebPFirstFrame(path string) (image.Image, error)` | Decodes the first frame of an animated WebP by piping it through `ffmpeg` (PNG on stdout). Errors wrap `ErrAnimatedWebP`. |
| `ErrAnimatedWebP` | Sentinel wrapped by errors from `Generate`/`AnimatedWebPFirstFrame` when an animated WebP's first frame can't be extracted (e.g. no `ffmpeg`). |

`WarmAllResult` fields: `Total` (image files found), `Generated` (newly
thumbnailed), `Skipped` (already had a thumbnail), `Failed` (missing
source, undecodable image, or animated WebP).

On-disk layout: `thumbnailPath` shards each thumbnail into a two-hex-
character subdirectory of its hash (`<Dir>/<hash[:2]>/<hash>.jpg`) — the
same technique git uses for its object store — so the cache directory
itself never accumulates an unbounded number of entries at one level,
independent of the `Sharder` mechanism below (which shards a *source image
collection*, not this cache).

### Sharding (`shard.go`)

Distributes files across numbered subdirectories of a root directory
instead of letting a single directory accumulate an unbounded number of
entries — the mechanism behind `site-scraper`'s per-run staging directory
and `image-browser`'s `shard` command.

| Function | Description |
|---|---|
| `NewSharder(root, capacity) (*Sharder, error)` | Prepares a `Sharder` over `root` (created with `os.MkdirAll` if missing), resuming from whatever shard directories already exist there — it scans `root` for subdirectories whose name parses as the shard naming convention and picks up from the highest-indexed one, counting its current entries. `capacity <= 0` uses `DefaultShardCapacity`. |
| `(*Sharder) Dir() (string, error)` | Returns the shard directory the next file should be placed into, creating a new one (one past the highest index seen so far) if the active shard has reached its capacity or none has been allocated yet. Does not itself move or create anything beyond the directory — call `Advance` only after the caller has actually placed a file there, so a failed move can simply retry against the same `Dir()` result. |
| `(*Sharder) Advance()` | Records that one more file was successfully placed in the directory `Dir` most recently returned. |
| `IsSharded(root, path) bool` | Reports whether `path`'s immediate parent directory is itself a direct child of `root` whose name matches the shard naming convention — i.e. whether some `Sharder` for `root` has already placed `path` there. A file sitting loose directly in `root`, or nested two or more levels under a shard directory, reports `false`. |
| `DefaultShardCapacity` | `4096` — how many entries the first shard directory (index 0) under a root holds before a `Sharder` starts a new one. |

Shard directories are named zero-padded sequential integers, ten digits
wide (`0000000000`, `0000000001`, ...) — sortable lexically in the exact
same order as numerically, which is what lets `NewSharder` find the
highest-indexed one with a plain string comparison. This naming convention
is an implementation detail; callers should rely only on "direct child of
root" (via `IsSharded`), not on the exact digit format.

**Capacity doubles per shard.** Shard index 0 holds `capacity` entries,
index 1 holds `2*capacity`, index 2 holds `4*capacity`, and so on —
`capacityForShard(capacity, idx) = capacity << idx`, capped at a shift of
40 so a pathologically long-lived root can't overflow an `int`. A
collection that keeps growing ends up with fewer, larger directories over
time instead of an ever-lengthening flat list of same-size ones.

`Sharder` is **not** safe for concurrent use — callers sharding from
multiple goroutines must serialize their `Dir`/`Advance` calls themselves.

### Files (`files.go`, plus `ListImageFiles` in `images.go`)

| Function | Description |
|---|---|
| `CreateFile(ctx, f *File) error` | Inserts `f`, sets `f.ID`, and backfills `f.ScannedAt` from the column default. |
| `GetFile(ctx, id) (*File, error)` | Fetches a file by id. Returns `sql.ErrNoRows` if it doesn't exist. |
| `GetFileByPath(ctx, path) (*File, error)` | Fetches a file by its unique path. Returns `sql.ErrNoRows` if it doesn't exist. |
| `ListFilesByHash(ctx, hash) ([]*File, error)` | Finds every file sharing a content hash — duplicate detection. |
| `ListFiles(ctx) ([]*File, error)` | Returns every file, ordered by id. |
| `ListImageFiles(ctx) ([]*File, error)` | Returns every file with a corresponding `images` row, excluding any already marked `missing_since`, ordered by id — the set `WarmAll` uses to decide which files a thumbnail cache should cover. |
| `UpdateFile(ctx, f *File) error` | Overwrites every column (except `scanned_at`) for `f.ID`. |
| `EnsureFile(ctx, f *File) (inserted bool, error)` | Inserts `f` if its path is new; otherwise leaves the existing row untouched and sets `f.ID` to it. Runs the insert-then-fallback-lookup inside one transaction so a concurrent delete between the two can't produce a spurious `sql.ErrNoRows`. |
| `UpdateFilePath(ctx, id, path, filename) error` | Updates only `path`/`filename` — for a file that moved on disk. |
| `UpdateFileStatus(ctx, id, status, errMsg *string) error` | Updates only `status`/`error`. Either argument may be `nil` to store SQL `NULL`. |
| `UpdateFileOrigin(ctx, id, originID *int64) error` | Updates only `origin_id`. A `nil` `originID` clears the association. |
| `CreateFilesTx(ctx, files []*File) error` | Bulk-inserts files in one transaction, skipping (not erroring on) any whose path already exists, and rolling back entirely on any other error; sets each `f.ID` to its row's id — existing or newly inserted — either way. Does **not** backfill `ScannedAt` for newly inserted rows (unlike `CreateFile`) — follow up with `GetFile` if you need it. |
| `TouchScanned(ctx, id) error` | Sets `scanned_at` to now. |
| `DeleteFile(ctx, id) error` | Deletes a file; cascades to `images`/`videos`/`audio`/`media_probe`/`file_tags`. |

### Origins (`origins.go`)

| Function | Description |
|---|---|
| `CreateOrigin(ctx, o *Origin) error` | Inserts `o` and sets `o.ID`. |
| `GetOrigin(ctx, id) (*Origin, error)` | Fetches an origin by id. |
| `GetOriginBySiteIdentifier(ctx, site, identifier) (*Origin, error)` | Fetches an origin by its unique `(site, identifier)` key. |
| `EnsureOrigin(ctx, o *Origin) (inserted bool, error)` | Inserts `o` if no origin matches `(o.Site, o.Identifier)` yet; otherwise sets `o.ID` to the existing row. Same same-transaction guarantee against a concurrent delete as `EnsureFile`. |
| `ListOrigins(ctx) ([]*Origin, error)` | Returns every origin, ordered by id. |
| `UpdateOrigin(ctx, o *Origin) error` | Overwrites every column for `o.ID`. |
| `DeleteOrigin(ctx, id) error` | Deletes an origin. Referencing files keep their (now dangling) `origin_id` — not cascaded. |

### Images (`images.go`)

| Function | Description |
|---|---|
| `UpsertImage(ctx, img *Image) error` | Inserts or overwrites the image row for `img.FileID`. |
| `InsertImageIfAbsent(ctx, img *Image) (inserted bool, error)` | Inserts only if no row exists yet; never overwrites. |
| `GetImage(ctx, fileID) (*Image, error)` | Fetches the image row for a file. Returns `sql.ErrNoRows` if none exists. |
| `DeleteImage(ctx, fileID) error` | Removes the image row without touching the file itself. |
| `ListImageFiles(ctx) ([]*File, error)` | See [Files](#files-filesgo-plus-listimagefiles-in-imagesgo) above — grouped there since it returns `*File`, not `*Image`, despite living in `images.go`. |

### Videos (`videos.go`)

| Function | Description |
|---|---|
| `UpsertVideo(ctx, v *Video) error` | Inserts or overwrites the video row for `v.FileID`. |
| `GetVideo(ctx, fileID) (*Video, error)` | Fetches the video row for a file. Returns `sql.ErrNoRows` if none exists. |
| `DeleteVideo(ctx, fileID) error` | Removes the video row without touching the file itself. |

### Audio (`audio.go`)

| Function | Description |
|---|---|
| `UpsertAudio(ctx, a *Audio) error` | Inserts or overwrites the audio row for `a.FileID`. |
| `GetAudio(ctx, fileID) (*Audio, error)` | Fetches the audio row for a file. Returns `sql.ErrNoRows` if none exists. |
| `DeleteAudio(ctx, fileID) error` | Removes the audio row without touching the file itself. |

### Media probe (`mediaprobe.go`)

| Function | Description |
|---|---|
| `UpsertMediaProbe(ctx, p *MediaProbe) error` | Inserts or overwrites raw `ffprobe` JSON for `p.FileID`; refreshes `probed_at` to now on overwrite (left to the column default on first insert). |
| `GetMediaProbe(ctx, fileID) (*MediaProbe, error)` | Fetches the probe row for a file. Returns `sql.ErrNoRows` if none exists. |
| `DeleteMediaProbe(ctx, fileID) error` | Removes the probe row. |

### Tags (`tags.go`)

| Function | Description |
|---|---|
| `GetOrCreateTag(ctx, name) (*Tag, error)` | Looks up a tag by its normalized name, creating it if absent. |
| `GetTagByName(ctx, name) (*Tag, error)` | Looks up a tag by exact (already-normalized) name. Returns `sql.ErrNoRows` if none exists. |
| `ListTags(ctx) ([]*Tag, error)` | Returns every tag, ordered by name. |
| `DeleteTag(ctx, id) error` | Deletes a tag and every `file_tags` row referencing it; does not touch tagged files. |
| `TagFile(ctx, fileID, tagID) error` | Associates a tag with a file. Idempotent. |
| `UntagFile(ctx, fileID, tagID) error` | Removes a tag association, if present. |
| `ListTagsForFile(ctx, fileID) ([]*Tag, error)` | Returns every tag on a file, ordered by name. |
| `SetFileTags(ctx, fileID, names []string) error` | Replaces a file's entire tag set with `names` in one transaction: creates any tag that doesn't exist yet (via `GetOrCreateTag`), tags the file with it if not already tagged, and untags whatever's no longer in `names`. A failure partway through leaves the file's tag set unchanged rather than half-updated. |
| `ListFilesForTag(ctx, tagID) ([]*File, error)` | Returns every file carrying a given tag, ordered by id. |

### Composite file records (`filerecords.go`)

Higher-level read helpers for UI-facing use cases that need a file alongside
its related rows in one round trip, rather than N+1 separate calls. This is
the primary search/filter API every GUI and CLI command in the sibling
repos builds its search box or `-flag` filters on top of.

| Function | Description |
|---|---|
| `ListFileRecords(ctx, q FileQuery) ([]*FileRecord, error)` | Returns files matching `q` (see fields below), each left-joined with its `Origin` and `Image`, ordered by `path` (case-insensitive). Supports `Limit`/`Offset` pagination and optional batch tag loading via `q.WithTags`. |
| `CountFileRecords(ctx, q FileQuery) (int, error)` | Counts files matching `q`, ignoring `Limit`/`Offset` — for "page X of Y" UI without fetching every row. |
| `ListImageResolutions(ctx, pathPrefix string) ([]ResolutionGroup, error)` | Returns `{Width, Height, Path}` for every file with a decoded `images` row (non-`NULL` width and height), optionally restricted to a path prefix (case-insensitive; empty matches every path). |

**`FileQuery` fields** (zero value matches every file; every string field
below is a case-insensitive match unless noted):

| Field | Type | Description |
|---|---|---|
| `PathPrefix` | `string` | Prefix match against `path`. Empty matches every path. |
| `Path` | `string` | Substring match against `path`. Independent of `PathPrefix` — both apply if both are set. |
| `Site` | `string` | Substring match against the file's origin `site`. Empty matches every site, including files with no origin. |
| `URL` | `string` | Substring match against the file's origin `url`. Empty matches every url, including files with no origin. |
| `Identifier` | `string` | Substring match against the file's origin `identifier`. Empty matches every identifier, including files with no origin. |
| `Status` | `string` | Substring match against the file's `status`. Empty matches every status, including files with none set. |
| `Hash` | `string` | **Exact** match against the file's `hash` (case-insensitive only because hex case shouldn't matter). Empty matches every hash, including files with none recorded. |
| `Tag` | `string` | Substring match against the file's tag names. Empty matches every file. |
| `WidthMin`, `WidthMax` | `int` | Restrict to images whose `width` falls within `[WidthMin, WidthMax]`. Zero on either side means unbounded on that side — zero is never treated as "exactly zero." |
| `HeightMin`, `HeightMax` | `int` | Same, for `height`. |
| `DiscoveredFrom`, `DiscoveredTo` | `*time.Time` | Restrict to files whose origin's `discovered_at` falls within `[DiscoveredFrom, DiscoveredTo]`. A `nil` bound is unbounded on that side. |
| `RequireImage` | `bool` | Only files **with** an `images` row (`INNER JOIN`). Mutually exclusive with `MissingImage` — setting both returns an error. |
| `MissingImage` | `bool` | Only files **without** an `images` row (anti-join via `i.file_id IS NULL`). Mutually exclusive with `RequireImage`. |
| `ExcludeMissing` | `bool` | Only files whose `missing_since` is `NULL` — hides files a `reconcile` pass found absent from disk. `false` (the zero value) matches every file regardless of `missing_since`. Applies even when `FileIDs` is set (see below). |
| `FileIDs` | `[]int64` | When **non-nil**, restricts results to exactly these file IDs and takes priority over every other content filter above (`PathPrefix` through `Tag`, plus the width/height/discovered-at bounds) — those are all ignored once `FileIDs` is non-nil. `RequireImage`/`MissingImage`/`ExcludeMissing` still apply on top, since those are structural invariants a caller sets independently of what's being searched for. A non-nil **empty** slice means "match nothing" (e.g. a raw-SQL search via `QueryFileIDs` that matched zero rows); `nil` (the zero value) means "no ID restriction at all." This is the bridge from an arbitrary `QueryFileIDs` result back into the rest of this filtering API. |
| `WithTags` | `bool` | Batch-attach each result's `Tags` (`ListFileRecords` only, in batches of 500 to stay under SQLite's variable limit). Left `false` by default to skip the extra query when callers don't need tags. |
| `Limit` | `int` | Caps the number of results (`ListFileRecords` only; `0` = no limit). |
| `Offset` | `int` | Skips this many matches before collecting `Limit` of them. Only meaningful alongside a positive `Limit`. |

**`FileRecord` fields:** `File *File`, `Origin *Origin` (nil if the file has
no `origin_id`), `Image *Image` (nil if the file has no `images` row),
`Tags []*Tag` (nil unless `FileQuery.WithTags` was set).

**`ResolutionGroup` fields:** `Width int`, `Height int`, `Path string`.

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
represented in the API as `[]*Tag` / `[]*File` association helpers instead.
`FileRecord` and `ResolutionGroup`, documented above under
[Composite file records](#composite-file-records-filerecordsgo), live in
`filerecords.go` rather than `models.go` since they're composite read
shapes, not table mirrors.)

## Testing

```bash
go test ./...
```

As of this writing, every test passes (`go build ./... && go vet ./... &&
go test ./...` all exit clean). Tests open a throwaway database via
`t.TempDir()` (see `openTestDB` in `invlib_test.go`) rather than touching
the real `DefaultPath()` location. Test files, one per corresponding
source file except where noted:

| Test file | Covers |
|---|---|
| `invlib_test.go` | `Open`/migration idempotency, file CRUD, image upsert + cascade delete, origin CRUD, the tags/file_tags junction, and `time.Time` round-tripping through the `_texttotime` DSN pragma. |
| `newapi_test.go` | `EnsureFile`, `EnsureOrigin`, `InsertImageIfAbsent`, `CreateFilesTx` (including its rollback-on-error path), `UpdateFilePath`, `UpdateFileStatus`, `UpdateFileOrigin`, `SetFileTags`. |
| `legacy_migrate_test.go` | Legacy-column repair paths (`blake3`→`hash` rename/backfill, added/dropped columns) and that `indexes.sql` applies correctly afterward. |
| `filerecords_test.go` | `ListFileRecords`/`CountFileRecords` across every `FileQuery` field — unfiltered, `RequireImage`/`MissingImage` (including their mutual-exclusivity error), the full set of substring/range/date filters, `FileIDs` (including that `ExcludeMissing` still applies on top of it) — and `ListImageResolutions`. |
| `readonly_test.go` | `QueryFileIDs` (id-column matching, dedup, empty-but-non-nil result, missing-id-column error, SQL errors surfaced), `OpenReadOnly` (write rejection, and that it does not create a missing database file). |
| `thumbcache_test.go` | `(*ThumbCache).Generate` — content-hash naming, reuse vs. `force` regeneration, different content producing a different cache entry, source-location independence, and the undecodable-file error path. |
| `warmall_test.go` | `WarmAll` — normal operation, context-cancellation behavior, and `force` regenerating every thumbnail unconditionally. |
| `shard_test.go` | `Sharder` filling a shard to capacity and doubling into the next one, `NewSharder` resuming from existing shard directories, and `IsSharded` across direct/nested/outside-root/non-numeric cases. |

Run a single test or package the standard way: `go test -run TestWarmAll -v ./...`.

## Dependencies

Direct dependencies, per `go.mod`'s first (non-`// indirect`) `require`
block:

| Module | Purpose |
|---|---|
| [`modernc.org/sqlite`](https://pkg.go.dev/modernc.org/sqlite) | Pure-Go (no cgo) SQLite driver, used by every `Open`/`OpenReadOnly` connection. |
| [`github.com/zeebo/blake3`](https://pkg.go.dev/github.com/zeebo/blake3) | BLAKE3 hashing for `HashFile`. |
| [`github.com/nfnt/resize`](https://pkg.go.dev/github.com/nfnt/resize) | Lanczos3 thumbnail resizing in `(*ThumbCache).Generate`. |
| [`golang.org/x/image`](https://pkg.go.dev/golang.org/x/image) | `bmp`/`webp` image decoders (blank-imported in `thumbcache.go`) so `Generate` can thumbnail those formats in addition to the standard library's gif/jpeg/png. |

Everything else in `go.mod` is a transitive dependency of the four above.

**Go version.** `go.mod` declares `go 1.27.1`. Nothing in the current
source requires a toolchain that new specifically — the newest language
feature actually used is the `min` builtin (Go 1.21+, in `shard.go` and
`filerecords.go`); the `1.27.1` floor appears to simply be whatever
toolchain was installed when the module was created/last `go mod tidy`'d,
not a deliberate feature-driven pin.

## License

No `LICENSE` file exists in this repository, and this directory is not
under version control (no `.git`, no tags). There is currently no stated
license for this code.
