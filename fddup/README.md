# fddup

Find and remove duplicate files using a **scan-then-delete** model.

`fddup` walks one or more directory trees, identifies byte-identical files using
a three-stage filter (size → 64 KB head hash → full BLAKE3 hash), and queues
duplicates in a SQLite database. A separate `delete` invocation reads from that
queue and removes files. Scan never touches your filesystem; deletion is opt-in
and resumable after a crash.

---

## Why a queue?

Most duplicate-finders read your disk and remove files in a single pass. That
works until you Ctrl-C halfway through, the power flickers, or the run prints a
duplicate you wish you'd reviewed. `fddup` separates discovery from destruction:

- **Scan** is read-only. Run it as many times as you like.
- **List** shows what would be deleted, with the surviving file marked.
- **Delete** consumes the queue, recording each file's outcome
  (`deleted` / `missing` / `failed`) in the same database. A crash mid-delete
  leaves the queue in a state the next call can resume from.

---

## Install

```bash
go install fddup/cmd/fddup@latest
```

Or build from source:

```bash
git clone https://fddup
cd fddup
go build ./cmd/fddup        # produces ./fddup
```

Requires Go 1.26+. The only runtime dependency is the SQLite database file
created at `~/.fddup/queue.db` on first use.

---

## Usage

```
fddup scan   [flags] <path> [path...]   Scan trees, queue duplicates
fddup list   [flags]                    Show pending duplicates
fddup delete [flags]                    Delete files queued by a previous scan
fddup status [flags]                    Show queue counts by status
fddup reset  [flags]                    Empty the queue (no files touched)
fddup prune  [flags]                    Remove stale hash cache entries for missing files
```

### `fddup scan`

Walk one or more roots, find duplicates, and queue them for deletion.

| Flag          | Default                   | Description                                                          |
|---------------|---------------------------|----------------------------------------------------------------------|
| `-db PATH`    | `~/.fddup/queue.db`       | Database path. Created if missing.                                   |
| `-workers N`  | `4`                       | Concurrent hash workers. Use `1` for HDD; `ncpu` for SSD/NVMe.       |
| `-inv-db PATH`| *(unset — disabled)*      | Optional: consult [inv-lib](../inv-lib)'s shared inventory database at `PATH` for an already-known full-file hash before reading a file. See [inv.db integration](#invdb-integration). |

Examples:

```bash
# Scan one tree with the default 4 workers
fddup scan ~/photos

# Scan multiple trees, pin to one worker for HDD-friendly sequential I/O
fddup scan -workers 1 /Volumes/Backups /Volumes/Archive

# Aggressive parallelism for an NVMe drive
fddup scan -workers 16 ~/code

# Custom queue location
fddup scan -db /tmp/work.db ~/downloads

# Reuse hashes image-browser/site-scraper already computed for ~/Images
fddup scan -inv-db ~/.config/inventory-manager/db/inv.db ~/Images
```

### `fddup list`

Print every pending row, with the keeper for each. Useful as a final review
before `delete`.

```bash
fddup list
```

### `fddup delete`

Process the queue. Each row's outcome is written back to the database.

| Flag          | Description                                            |
|---------------|--------------------------------------------------------|
| `-db PATH`    | Database path                                          |
| `-yes`        | Skip the interactive `Type 'yes' to proceed:` prompt   |
| `-inv-db PATH`| Optional: remove a deleted (or already-missing) file's row from this [inv-lib](../inv-lib) database too. See [inv.db integration](#invdb-integration). |

```bash
# Interactive — prompts before deleting
fddup delete

# Non-interactive (scripts, cron)
fddup delete -yes

# Keep inv.db in sync with what fddup actually removed
fddup delete -yes -inv-db ~/.config/inventory-manager/db/inv.db
```

If interrupted (Ctrl-C, kill, crash), re-run `fddup delete`. Already-processed
rows are skipped because their status is no longer `pending`.

### `fddup status`

```bash
fddup status
```

```
STATUS     COUNT
pending    72
deleted    11
failed     1

Pending reclaim: 8553.46 MB
```

### `fddup reset`

Empty the queue. Files on disk are unaffected. Use after fixing skip rules
and re-scanning from a clean slate.

```bash
fddup reset            # prompts for confirmation
fddup reset -yes       # non-interactive
```

### `fddup prune`

Remove entries from the hash cache for files that no longer exist on disk.
The hash cache persists across scans to skip re-reading unchanged files (see
[Hash cache](#hash-cache) below). Over time it accumulates entries for files
that have since been moved or deleted. `prune` reclaims that space.

```bash
fddup prune
# Hash cache is clean — no stale entries found.
# or:
# Pruned 47 stale cache entries.
```

---

## How it works

### Three-stage filter

```
stage 1   walk all roots, group regular files by size
stage 2   hash the first 64 KB of each size-collision candidate
stage 3   full-hash survivors of stage 2
```

This pyramid is the single biggest performance win. Hashing is expensive;
size comparison is free. At 3 TB, only a small fraction of files share a size
with any other file, and most of those are eliminated by the cheap 64 KB head
hash before the full hash ever reads them.

A 64 KB head matters more than 4 KB on real workloads — many file formats
have shared headers in the first few KB but diverge by the 16 KB mark, and on
a spinning disk the cost of reading 64 KB is essentially the same as 4 KB
(filesystem readahead already pulls ~128 KB on a single seek).

### Concurrency

Stages 2 and 3 use a fan-out/fan-in worker pool:

```
            ┌──────────┐
feeder ──>  │   jobs   │  ──>  worker 1 ──┐
            └──────────┘       worker 2 ──┤
                               ...        ├──> results ──> aggregator
                               worker N ──┘
```

The feeder, workers, and closer are goroutines. The aggregator runs on the
calling goroutine, so the cluster map is touched by exactly one goroutine
and needs no lock.

The right `-workers` value depends on storage:

| Storage             | Recommended    | Why                                                          |
|---------------------|----------------|--------------------------------------------------------------|
| Spinning HDD        | `1`            | Random seeks across many parallel reads thrash the head      |
| SATA SSD            | `2–4`          | Helps modestly; the bus tops out around 550 MB/s             |
| NVMe                | `ncpu`         | Bottleneck shifts to BLAKE3 CPU; saturate cores              |
| NFS / SMB / sshfs   | `16–64`        | Latency-bound; many in-flight requests beat sequential       |

### Hash cache

After each file is hashed, the result is stored in a `hash_cache` table keyed
on `(path, size, mtime_ns)`. On subsequent scans:

- If the file's size and modification time match the cached record, the read is
  skipped entirely and the cached hash is returned immediately.
- If either value has changed, the entry is treated as stale and the file is
  re-hashed.

This has two practical effects:

1. **Interrupted scans resume cheaply.** If a stage 3 full-hash run is cut short
   at 60%, re-running `fddup scan` on the same tree will skip the files already
   hashed and only read the remainder.
2. **Repeated scans of unchanged trees are fast.** A second scan of 3 TB of
   photos that haven't moved completes in seconds instead of over an hour.

Cache entries accumulate over time as files are moved or deleted. Run
`fddup prune` periodically to remove entries for paths that no longer exist.

### inv.db integration

`fddup` is a general-purpose duplicate finder — it works on any directory
tree, not just image libraries — so it does not depend on
[`inv-lib`](../inv-lib) (the shared inventory database `image-browser` and
`site-scraper` write to) for its own scan-then-delete model. A plain `fddup
scan`/`fddup delete` never opens or even knows about `inv.db`.

`-inv-db PATH` is a purely optional, opt-in bridge to that database, for the
specific case of running `fddup` against a tree those other tools already
maintain (`~/Images`):

- **`fddup scan -inv-db PATH`** — before hashing a stage 3 candidate (the
  full-file BLAKE3 hash, the expensive read), fddup first checks `inv.db`'s
  `files` table for a hash already on record at that exact path, trusting
  it only if the file's size and modification time still match what
  `inv.db` has — a changed file is re-hashed, never assumed. A hit is also
  written into fddup's own `hash_cache`, so a later scan still benefits
  even without `-inv-db`. This avoids re-hashing bytes `image-browser scan`
  or `site-scraper` already hashed for the same file.
- **`fddup delete -inv-db PATH`** — after removing a file (or finding it
  already gone), fddup also removes its row from `inv.db` if one exists.
  Without this, a file `fddup` deletes leaves a dangling `files` row until
  some other tool's reconcile pass (`image-browser`'s `invdb.PruneMissing`,
  run by `move`/`size`) happens to stat it and notice it's gone.

Both are no-ops unless you pass `-inv-db` explicitly; neither direction
writes anything inv.db-related to fddup's own `queue.db`.

### Skip rules

`fddup` excludes things in three categories:

**Pruned during walk** (`internal/skip.Dirs`) — never visited at all:
- `.git`
- `node_modules`
- `venv`, `.venv`
- `target`

**Reported but not deleted** (`internal/skip.Names`) — duplicates show up
in the report tagged `[SKIPPED: false-positive filenames]`:
- `go.sum`, `go.mod`
- `.gitignore`, `.dockerignore`
- `.npmrc`
- `.python-version`, `.tool-versions`
- `package-lock.json`, `Cargo.lock`
- `CACHEDIR.TAG`

**Reported but not deleted, glob match** (`internal/skip.Globs`):
- `vars.*.yml`, `vars.*.yaml` (CloudFormation per-environment configs)

These are filenames where identical content across copies is **intentional**,
not waste — deleting them breaks per-project tooling. Each name on the list
exists because removing it actually broke something during testing.

### Keeper selection

When a duplicate cluster is found, the surviving copy is chosen by:

1. **Shortest absolute path wins.** This favors the "original" location over
   deeper "copy of X" paths.
2. **Lexicographic tiebreak** when path lengths match. The choice is
   deterministic: the same input produces the same plan across runs.

### What's never deleted

- 0-byte files (all empty files hash identically; not real duplicates)
- Symlinks, devices, sockets (only regular files are even considered)
- Files inside `skip.Dirs` (never walked)
- Clusters where every path matches `skip.Names` or `skip.Globs`

---

## Example session

```bash
# First scan finds duplicates and queues them. Also populates the hash cache.
$ fddup scan ~/photos
walk: 24193 files, 412.67 GB total
stage 1 -> 1842 candidates after size grouping
stage 2 head-hash: 1842 files in 4s
stage 2 -> 0 skipped, 12 short-circuit clusters, 187 files / 38.4 GB need full hash
stage 3 full-hash: 187 files in 1m23s

size=4194304 blake3=8d7d75...
  KEEP    /Users/me/photos/2024/img_4521.jpg
  QUEUED  /Users/me/photos/imports/2024-12/img_4521.jpg
  QUEUED  /Users/me/photos/_archive/img_4521 copy.jpg

...

Queued 73 duplicate(s) totalling 8553.46 MB. Run `fddup delete` to remove.

# Review what's about to be removed.
$ fddup list

# Check the queue summary.
$ fddup status
STATUS     COUNT
pending    73

Pending reclaim: 8553.46 MB

# Delete (interactive prompt).
$ fddup delete
About to delete 73 file(s) totalling 8553.46 MB.
Type 'yes' to proceed: yes

Deleted: 73  Missing: 0  Failed: 0  Reclaimed: 8553.46 MB

# Re-scan after adding new photos — unchanged files are served from cache,
# only new arrivals are hashed. Completes in seconds instead of over an hour.
$ fddup scan ~/photos
walk: 24201 files, 412.69 GB total
stage 1 -> 16 candidates after size grouping
stage 2 head-hash: 16 files in 0s
stage 2 -> 0 skipped, 0 short-circuit clusters, 8 files / 0.02 GB need full hash
stage 3 full-hash: 8 files in 0s
...

# Prune cache entries for files deleted since the last scan.
$ fddup prune
Pruned 73 stale cache entries.
```

If `fddup delete` is interrupted halfway through, re-running it picks up where
it left off — already-processed rows have moved out of `pending` status.

---

## Project layout

Standard Go project layout per [go.dev/doc/modules/layout](https://go.dev/doc/modules/layout):

```
.
├── cmd/fddup/         # binary entry point (main package, CLI dispatch)
│   └── main.go
├── internal/          # private packages, not importable by other modules
│   ├── scan/          # three-stage pipeline + worker pool
│   │   ├── scan.go
│   │   └── hash.go
│   ├── queue/         # SQLite-backed deletion queue
│   │   └── queue.go
│   ├── skip/          # skip rules (dirs, names, globs)
│   │   └── skip.go
│   └── invdb/         # optional -inv-db bridge to inv-lib (see "inv.db integration")
│       └── invdb.go
├── go.mod
├── go.sum
├── README.md
└── CLAUDE.md
```

The `internal/` boundary is enforced by the Go toolchain itself: code outside
this module physically cannot import `internal/...`. This keeps the package
APIs free to evolve without breaking external callers.

---

## Safety properties

- **Scan never modifies the filesystem.** Run as often as you want.
- **Delete is opt-in.** A standalone subcommand, gated by a confirmation prompt
  unless `-yes` is passed.
- **Delete is resumable.** Crash, Ctrl-C, kernel panic — re-run `fddup delete`
  and it picks up from the next `pending` row.
- **Per-row outcomes are recorded.** A `failed` row keeps its error message in
  the database so you can investigate without re-running the whole scan.
- **Re-scanning is idempotent.** `INSERT ... ON CONFLICT(path) DO NOTHING`
  means scanning the same tree twice doesn't double-queue anything.
- **Scan is resumable.** The hash cache stores results keyed on
  `(path, size, mtime_ns)`. An interrupted scan restarts from the first
  un-cached file rather than re-reading everything.
- **The queue itself is just SQLite.** If you want to inspect or amend it
  directly, `sqlite3 ~/.fddup/queue.db` works.

---

## Development

```bash
go build ./...           # build all packages
go vet ./...             # static analysis
go test ./...            # run tests (none yet)
go install ./cmd/fddup   # install to $GOPATH/bin
```

The codebase has no external test suite yet. Smoke-testing is done end-to-end:

```bash
mkdir -p /tmp/fddup-test/{a,b}
dd if=/dev/urandom of=/tmp/fddup-test/a/data.bin bs=1M count=2
cp /tmp/fddup-test/a/data.bin /tmp/fddup-test/b/copy.bin
go run ./cmd/fddup scan -db /tmp/fddup-test.db /tmp/fddup-test
go run ./cmd/fddup scan -db /tmp/fddup-test.db /tmp/fddup-test  # second scan uses cache
go run ./cmd/fddup list -db /tmp/fddup-test.db
go run ./cmd/fddup delete -db /tmp/fddup-test.db -yes
go run ./cmd/fddup prune -db /tmp/fddup-test.db                 # clean up deleted file's cache entry
```

---

## Database schema

```sql
CREATE TABLE scans (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    roots        TEXT NOT NULL
);

CREATE TABLE duplicates (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_id      INTEGER NOT NULL REFERENCES scans(id),
    hash         TEXT NOT NULL,
    size         INTEGER NOT NULL,
    path         TEXT NOT NULL,
    keeper       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    error        TEXT,
    processed_at DATETIME,
    UNIQUE(path)
);

CREATE TABLE hash_cache (
    path       TEXT    NOT NULL PRIMARY KEY,
    size       INTEGER NOT NULL,
    mtime_ns   INTEGER NOT NULL,
    head_hash  TEXT,
    full_hash  TEXT
);
```

`duplicates.status` is one of `pending` (queued), `deleted` (`os.Remove`
succeeded), `missing` (file already gone at delete time), or `failed`
(`os.Remove` returned an error — see the `error` column).

`hash_cache` stores BLAKE3 results between scans. A row is valid as long as
the file's `size` and `mtime_ns` match; any change invalidates the entry and
triggers a re-hash. `head_hash` and `full_hash` are stored independently so
stage 2 cache hits don't require a prior stage 3 run. Run `fddup prune` to
remove rows whose `path` no longer exists on disk.

---

## Dependencies

- [`github.com/zeebo/blake3`](https://github.com/lukechampine/blake3) —
  pure-Go BLAKE3 with NEON / AVX-2 / AVX-512 dispatch.
- [`modernc.org/sqlite`](https://gitlab.com/cznic/sqlite) — pure-Go SQLite,
  no CGo required.
- [`inv-lib`](../inv-lib) (sibling module, via `replace inv-lib => ../inv-lib`
  in `go.mod`) — only ever touched by `internal/invdb`, and only at runtime
  when `-inv-db` is passed. See [inv.db integration](#invdb-integration).

All are pure Go. `fddup` cross-compiles cleanly to anywhere Go targets.
