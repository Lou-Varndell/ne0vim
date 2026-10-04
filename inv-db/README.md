# inv-db

A SQLite-backed inventory database for a media library (files, images, videos).
Tracks physical files on disk — path, BLAKE3 content hash, size, timestamps —
plus type-specific metadata for images and videos.

## Status

`db` and `store` are a stable public library: open a database, insert/query
files. `cmd/inv-db` is a thin CLI wrapper that scans a directory for images,
hashes and stats each file, and ingests them via the library. `finder`,
`importer`, and `hash` are implementation details of that CLI and are not
part of the public API.

## Library usage

Other Go modules in this workspace can import `db` and `store` directly
against a shared SQLite file:

```go
import (
    "inv-db/db"
    "inv-db/store"
)

database, err := db.Open("inv.db")
// handle err, defer database.Close()

s := store.New(database)
id, err := s.AddFile(ctx, store.File{Path: "/path/to/file.jpg"})
```

Since this module isn't published, consumers need a `replace` directive
pointing at this directory until/unless it's tagged and pushed.

## Packages

| Package | Visibility | Responsibility |
|---|---|---|
| `db` | Public | Opens the SQLite database and applies the embedded schema (`db/schema.sql`) |
| `store` | Public | Typed queries against the `files`/`images`/`videos` tables (`AddFile`, `AddFiles`) |
| `internal/finder` | CLI-internal | Concurrently walks a directory tree for files matching glob patterns |
| `internal/importer` | CLI-internal | Walks a directory tree for `manifest.json` files and decodes their entries |
| `internal/hash` | CLI-internal | Computes the hex-encoded BLAKE3 digest of a file |

Schema design notes and the target schema live in [`db.md`](db.md).

## Build

```sh
make build   # builds ./cmd/inv-db to ~/.local/bin/inv-db
```

Other targets: `make fmt`, `make tidy`, `make test`, or `make all` to run all four.

## Run

```sh
go run ./cmd/inv-db -db inv.db -root /path/to/media
```

Both flags are optional. `-db` defaults to `inv.db` in the current directory
and is created with the schema applied on first run (`db/schema.sql`).
Re-running against an existing database is safe — files already present at
a given path are skipped, not duplicated. Files with identical content
(same BLAKE3 hash) at different paths each still get their own row; query
`files` grouped by `blake3` to find them.

## Requirements

- Go 1.27+
- No CGO required — `modernc.org/sqlite` is pure Go, and `github.com/zeebo/blake3`
  uses Go assembly (SIMD) rather than cgo
