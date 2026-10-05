# site-scraper

A Go CLI (and library) that downloads images from a mix of website URLs and
direct image URLs, deduplicates them by content, and lands a clean set of
files in a destination directory alongside a JSON manifest describing the
outcome of every input.

- **CLI**: `cmd/site-scraper` — reads a newline-delimited list of URLs from a
  file and runs the whole pipeline end to end.
- **Library**: `scraper` — the reusable `Downloader` type the CLI is a thin
  wrapper around. Import it directly if you want the pipeline without the
  CLI's flag parsing.

## Contents

- [How it works](#how-it-works)
- [Install / build](#install--build)
- [CLI usage](#cli-usage)
- [The manifest](#the-manifest)
- [The inventory database](#the-inventory-database)
- [Library usage](#library-usage)
- [Security: SSRF hardening](#security-ssrf-hardening)
- [Concurrency and retries](#concurrency-and-retries)
- [Package layout](#package-layout)
- [Known limitations](#known-limitations)
- [Testing](#testing)

## How it works

`Downloader.Run` executes a fixed pipeline over the input URLs:

1. **Classify** — each input is probed (HEAD, falling back to GET on
   405/501) to determine whether its `Content-Type` is `image/*` (a direct
   image) or `text/html` (a page to scrape). Anything else is recorded as a
   failed entry.
2. **Discover** — every page is fetched and parsed for image candidates:
   - `<img src>` / `<img data-src>` (including lazy-loaded images)
   - `<img srcset>` / `<img data-srcset>` — every responsive candidate,
     descriptors (`480w`, `2x`, ...) stripped
   - `<picture><source srcset>` — art-direction / format-switching markup
   - `<a href>` links whose target itself turns out to be image content

   All of a page's candidates are gathered before any downloading starts.
3. **Download** — every discovered/direct image URL is fetched into a
   scratch temp directory, named from the URL path or a
   `Content-Disposition` filename if present.
4. **Deduplicate** — files are grouped by logical filename, then by size
   within a name group, then confirmed as true duplicates by a
   [BLAKE3](https://github.com/BLAKE3-team/BLAKE3) content hash. The first
   file seen in a matching group survives; the rest are recorded as
   `duplicate-of:<hash>` and left in the temp directory for inspection.
5. **Decode dimensions** — each survivor's pixel width/height is read via
   `image.DecodeConfig` (JPEG, PNG, GIF, and WebP).
6. **Move** — survivors are moved into the destination directory under a
   collision-free name (`-2`, `-3`, ... suffixes as needed), falling back to
   copy-then-remove if `tempDir` and `destDir` are on different filesystems.
7. **Manifest merge** — the run's outcome is atomically merged into
   `<destDir>/manifest.json`, upserting by source URL so re-running against
   the same destination updates existing entries instead of duplicating them.
   Each survivor moved in step 6 is also recorded as a row in the shared
   inventory database (see [The inventory database](#the-inventory-database)).

Concurrency is bounded and rate-limited throughout; a canceled or
deadline-exceeded context is propagated into every in-flight HTTP call.

## Install / build

Requires Go 1.26+ (see `go.mod`).

```bash
go build -o site-scraper ./cmd/site-scraper
```

or run directly with `go run`:

```bash
go run ./cmd/site-scraper -input urls.txt
```

## CLI usage

```
Usage:
  site-scraper -input urls.txt -temp-dir /tmp/site-scraper -dest-dir ./images

Downloads, deduplicates, and moves images referenced by the URLs in the
input file (a mix of page URLs and direct image URLs, one per line).

Options:
```

| Flag         | Default                                | Description |
|--------------|-----------------------------------------|-------------|
| `-input`     | *(none — required)*                     | Path to a file of URLs, one per line. Blank lines are skipped, and each line is whitespace-trimmed. |
| `-temp-dir`  | a fresh OS temp directory (`os.MkdirTemp`) | Scratch directory for in-progress downloads. The auto-created default is removed automatically at the end of a run *unless* the run failed or duplicates were found — in either case it's left behind for inspection. A directory you supply explicitly is never touched by cleanup. |
| `-dest-dir`  | `$HOME/Images/<year>/<month>/<day>/<HHMMSS>` | Directory the deduplicated survivors (and `manifest.json`) are moved into. Created if missing. |
| `-timeout`   | `0` (no deadline)                       | Overall run deadline (e.g. `5m`, `90s`). Must be `>= 0`. |
| `-query`     | `""` (disabled)                         | **Currently non-functional / WIP — do not use.** See [`-query`: known-broken](#-query-known-broken) below. |

### Example

```bash
cat > urls.txt <<EOF
https://example.com/gallery
https://example.com/photos/direct-image.jpg
EOF

go run ./cmd/site-scraper -input urls.txt -dest-dir ./out
```

Output:

- **stderr** — a one-line human-readable summary (`kept=N duplicate=N
  failed=N manifest=...`), plus a note if duplicates were retained in the
  temp directory for inspection.
- **stdout** — the full JSON manifest, so the two streams can be separated
  cleanly (`site-scraper ... > manifest.json`).

Exit codes: `0` success, `1` runtime error (network, filesystem, decode
failure, or `Run` returning an error — including cancellation/timeout), `2`
usage error (missing `-input`, `-dest-dir` explicitly cleared, or
`-timeout < 0`).

Interrupting with **Ctrl-C** (`SIGINT`) or a **`SIGTERM`** cancels the run's
context; the CLI reports it distinctly on stderr rather than a generic error.

### `-query`: known-broken

`cmd/site-scraper/fetch_all.go` adds a `-query <term>` flag intended to pull
additional image URLs from a search API (paginating in batches of 500 via
`fetchAllPics`/`fetchPics`, deduplicating by `g_url`) and append them to the
URLs read from `-input` before `Run` starts. **As shipped, it cannot work:**

- `main.go` calls `fetchAllPics("", *query)` with the API base URL hardcoded
  to `""`. `fetchPics` builds the request as `"" + "?offset=...&limit=...
  &lang=en&q=..."` and passes that to `http.Get`, which fails immediately
  with an unsupported-protocol-scheme error — there is no real API endpoint
  wired in.
- If it did succeed, the fetched URLs would be written via `writeLines` to a
  hardcoded path, `/Users/boomer/dev/go/site-scraper/input.txt` — not
  derived from `-input`, `-dest-dir`, or `$HOME`, and not a path that exists
  on most machines running this tool.

Passing `-query` today just fails fast with a `fetch all pics` error (exit
code `1`) before any download work happens. Fixing it needs a real
`apiURL` and a configurable (or removed) output path.

## The manifest

`<dest-dir>/manifest.json` holds one entry per input processed across every
run against that destination (re-runs upsert by `source_url`, they don't
duplicate):

```json
{
  "entries": [
    {
      "site": "https://example.com/gallery",
      "source_url": "https://example.com/photos/cat.jpg",
      "file": "cat.jpg",
      "hash": "b1946ac9...",
      "status": "kept",
      "original_file": "cat.jpg",
      "discovered_at": "2026-09-26T14:03:11Z",
      "width": 1024,
      "height": 768
    },
    {
      "site": "https://example.com/gallery",
      "source_url": "https://example.com/photos/cat-copy.jpg",
      "hash": "b1946ac9...",
      "status": "duplicate-of:b1946ac9...",
      "discovered_at": "2026-09-26T14:03:11Z"
    },
    {
      "site": "https://example.com/gallery",
      "source_url": "https://example.com/broken.png",
      "status": "download-failed:unexpected status 500 Internal Server Error",
      "error": "unexpected status 500 Internal Server Error",
      "discovered_at": "2026-09-26T14:03:11Z"
    }
  ]
}
```

| Field           | Present when                | Meaning |
|-----------------|------------------------------|---------|
| `site`          | always                       | The original input URL (page or direct image) this entry traces back to. |
| `source_url`    | always                       | The resolved image URL actually fetched (equals `site` for direct-image inputs). |
| `file`          | `status == "kept"`           | Final filename in `dest-dir`. |
| `original_file` | `status == "kept"`           | The filename before any collision disambiguation. |
| `hash`          | kept or duplicate            | BLAKE3 content hash (hex). |
| `status`        | always                       | `"kept"`, `"duplicate-of:<hash>"`, or `"download-failed:<reason>"`. |
| `error`         | failed only                  | The underlying error string. |
| `width`/`height`| kept, and decodable          | Pixel dimensions; omitted if decoding failed. |
| `discovered_at` | always                       | Timestamp of the run that produced this entry. |

## The inventory database

Alongside `manifest.json`, every run also opens (creating if needed) a
single, shared SQLite database at `~/.config/inventory-manager/db/inv.db`
(see `internal/db.DefaultPath`) — the same file `image-browser`'s `process`
and `scan` commands write to, so one inventory covers both tools regardless
of which `destDir` a given site-scraper run used. Override the location
(e.g. in tests) by setting `$INVENTORY_MANAGER_DB`; `DefaultPath` creates
that path's parent directory the same way it does for the real default.

```sql
sqlite3 ~/.config/inventory-manager/db/inv.db "select path, filesize, blake3 from files;"
```

Each kept survivor's absolute path, filename, extension, size, and BLAKE3
hash (reused from the dedup pass — no extra hashing) is inserted via
`internal/store.AddFile`; re-running against the same `destDir` is safe since
`path` is unique and a repeat insert is silently ignored rather than
duplicated or erroring — the existing row's ID is looked up instead so child
rows can still be attached to it.

When dimensions decode successfully, a matching row is also inserted into
the `images` child table (`file_id`, `width`, `height`, `format`) via
`internal/store.AddImage`:

```sql
sqlite3 ~/.config/inventory-manager/db/inv.db "select f.path, i.width, i.height, i.format from files f join images i on i.file_id = f.id;"
```

The schema (embedded in `internal/db`) also defines a `videos` child table
for type-specific metadata, but nothing in this tool populates it yet.

`inv.db` is purely additive: a failure to resolve or open its path aborts
the run (the shared database is assumed reachable), but a failure to insert
any one file's or image's row is only logged as a warning and does not fail
the rest of that run or affect `manifest.json`, which remains the
authoritative record of what happened to each input URL.

## Library usage

```go
import (
	"context"

	"main/scraper"
)

d, err := scraper.NewDownloader(
	scraper.WithTempDir("/tmp/site-scraper"),
	scraper.WithDestDir("./images"),
	scraper.WithConcurrency(16),           // default: 8
	scraper.WithHTTPClient(myClient),      // default: SSRF-hardened client
	scraper.WithRateLimiter(myLimiter),    // default: concurrency req/s, burst=concurrency
	scraper.WithLogger(myLogger),          // default: discarded — wire one up to see retry/rate-limit warnings
)
if err != nil {
	// WithTempDir/WithDestDir are required; missing either is an error.
}

manifest, err := d.Run(ctx, []string{
	"https://example.com/gallery",
	"https://example.com/photos/direct-image.jpg",
})
```

`Run` is safe to call multiple times against the same `Downloader`/`destDir`
— the manifest merge upserts by `source_url` rather than duplicating entries.

## Security: SSRF hardening

`scraper.NewDownloader`'s default HTTP client (`internal/httpx.NewClient`)
refuses to connect to non-public addresses. The check runs against the
**resolved remote address after connecting**, not the hostname before DNS
resolution — so it also covers HTTP redirect targets and closes the
DNS-rebinding TOCTOU gap a pre-resolve check would leave open. Blocked:

- Loopback, link-local, private, unspecified, and multicast ranges
  (`net.IP`'s built-in classifiers)
- RFC 6598 shared address space (CGNAT — used internally by many cloud
  NAT/load-balancer setups)
- RFC 6890 IETF protocol assignments, RFC 2544 benchmarking, and RFC 5737 /
  RFC 3849 documentation ranges
- RFC 1112 reserved (`240.0.0.0/4`) and RFC 6666 discard-only (`100::/64`)

This matters because the CLI's input file can contain arbitrary URLs; if
that list is ever less trusted than "chosen by the person running this
tool" (e.g. sourced from an API or another automated process), this stops a
crafted URL from resolving to an internal service or a cloud metadata
endpoint. Override with `scraper.WithHTTPClient` only if you understand the
tradeoff — `newTestDownloader` in the test suite does this deliberately
because `httptest` servers listen on loopback.

## Concurrency and retries

- All per-URL work (classify, discover, download) runs through a bounded
  worker pool (`concurrency` option, default `8`) that preserves input
  order in its results.
- A token-bucket rate limiter (default: `concurrency` requests/sec, burst
  `concurrency`) throttles outgoing requests independently of the worker
  bound.
- `internal/httpx.Do` retries HTTP 429 (honoring `Retry-After` when present)
  and transient network errors up to 4 times with jittered exponential
  backoff. A canceled context aborts immediately without consuming a retry.
- The HTTP transport deliberately caps `MaxConnsPerHost` below the worker
  concurrency, so a burst of workers queues on the connection pool rather
  than hammering a single host.

## Package layout

```
cmd/site-scraper/     CLI entry point (flag parsing, signal handling, summary output)
  fetch_all.go          -query's search-API pagination (fetchAllPics/fetchPics) — see "-query: known-broken" above
scraper/              Downloader, Option, Run — the pipeline described above
  classify.go           direct-image vs. page detection (HEAD/GET + Content-Type)
  discover.go           HTML parsing for image candidates (goquery)
  download.go           bounded-concurrency fetch into tempDir
  dedup.go              filename -> size -> BLAKE3 hash grouping/confirmation
  dimensions.go         image.DecodeConfig (+ WebP) for width/height
  move.go               tempDir -> destDir move, collision handling
  concurrency.go        runBounded: the generic worker-pool helper
  util.go               small shared helpers
internal/
  httpx/                SSRF-hardened client, retrying/rate-limited request helper
  manifest/             Entry, Manifest, atomic load/upsert/write of manifest.json
  textfile/             newline-delimited, whitespace-trimmed line reading
  db/                   DefaultPath (shared ~/.config/inventory-manager/db/inv.db
                        location) + Open, applying the embedded schema.sql
  store/                typed inserts (AddFile/AddFiles/AddImage) against inv.db
```

## Known limitations

- **Cross-name duplicates aren't detected.** Dedup only compares files that
  share a *logical filename* (then size, then hash). Two files with
  different names but byte-identical content are treated as distinct kept
  files — this is a deliberate scope limit, not a bug.
- **No recursive crawling.** Discovery only looks at the given page's own
  `<img>`/`<picture>`/linked-image content — it does not follow `<a>` links
  to other pages looking for more images.
- **No image transformation.** Bytes are downloaded and moved as-is; no
  resizing, re-encoding, or format conversion.
- **`-query` doesn't work.** See [`-query`: known-broken](#-query-known-broken).

## Testing

```bash
go test ./...
```

Tests use `httptest` servers and a non-SSRF-filtering client (loopback
addresses would otherwise be rejected by the production default). Fixtures
cover end-to-end runs, deduplication edge cases, responsive-image discovery
(`srcset`/`<picture>`), collision handling on move, and WebP dimension
decoding.
