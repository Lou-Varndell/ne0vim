# Implementation Plan: `scraper` package

Source spec: `prompt.md`. This is a straight execution plan against that
finalized spec — no open design questions remain.

## Package layout

```
tmp/site-scraper/
  go.mod
  scraper/
    scraper.go       # Downloader, Option, NewDownloader, Run (orchestration)
    classify.go       # classifyInput: page vs direct-image URL via HEAD/Content-Type
    discover.go       # goquery-based discovery: <img>, linked images, URL resolution
    download.go        # bounded-concurrency fetch into tempDir
    dedup.go           # filename -> size -> blake3 grouping/confirmation
    dimensions.go       # image.DecodeConfig (+ webp) for Width/Height
    move.go             # tempDir -> destDir move, collision disambiguation, cleanup
    manifest.go         # Entry, Manifest, load/upsert/write manifest.json
    scraper_test.go
    dedup_test.go
    manifest_test.go
    move_test.go
    testdata/            # fixture HTML pages + small test images for httptest server
```

Single exported package `scraper`; internal helpers stay unexported in the
same package (no need for an `internal/` split at this size).

## Step-by-step

1. **go.mod deps** — add `github.com/PuerkitoBio/goquery`, `lukechampine.com/blake3`,
   `golang.org/x/image/webp`. `go mod tidy`.

2. **Types & options** (`scraper.go`)
   - `Downloader` struct: `tempDir`, `destDir string`; `concurrency int`;
     `httpClient *http.Client`.
   - `Option func(*Downloader)`; `WithTempDir`, `WithDestDir`,
     `WithConcurrency`, `WithHTTPClient`.
   - `NewDownloader(opts ...Option) (*Downloader, error)`: apply opts,
     default `concurrency` (8) and `httpClient` (`http.Client{Timeout: ...}`)
     if unset, validate `tempDir`/`destDir` are non-empty and creatable
     (`os.MkdirAll`), return error otherwise.

3. **Manifest types** (`manifest.go`)
   - `Entry` struct exactly as specified in `prompt.md`.
   - `Manifest struct { Entries []Entry }` (or `map[string]Entry` keyed by
     `SourceURL` internally, sliced to `Entries` on return — map makes the
     upsert step trivial).
   - `loadManifest(path string) (map[string]Entry, error)` — missing file is
     not an error, returns empty map.
   - `writeManifest(path string, entries map[string]Entry) error` — marshal
     sorted by `SourceURL` for stable diffs, write via temp-file+rename for
     atomicity.

4. **Classify** (`classify.go`)
   - `classifyInput(ctx, client, rawURL string) (isImage bool, err error)`:
     issue HEAD (fall back to GET if HEAD unsupported/405), inspect
     `Content-Type`; `image/*` → direct image, `text/html` → page, anything
     else → error entry (`download-failed:unsupported-content-type`).

5. **Discovery** (`discover.go`)
   - `discoverImageURLs(ctx, client, pageURL string) ([]string, error)`:
     GET page, `goquery.NewDocumentFromReader`, collect `img[src]`,
     `img[data-src]`, and `a[href]` whose target resolves to `image/*` via
     classify. Resolve every collected URL against the page's own URL with
     `net/url.Parse` + `ResolveReference`. Dedup the URL list itself before
     returning (avoid downloading the same URL twice from one page).

6. **Orchestration skeleton** (`scraper.go: Run`)
   - Phase 1: classify every input URL concurrently (bounded); split into
     `directImages []string` and `pages []string`, recording classify
     failures directly into the manifest map with
     `Site = input, Status = "download-failed:<reason>"`.
   - Phase 2: discover images from `pages` concurrently (bounded); merge
     into one `[]work{Site, SourceURL}` list alongside `directImages`
     (`Site == SourceURL` for those). This is the hard boundary the spec
     calls out — discovery must fully complete before phase 3 starts.
   - Phase 3: download phase (`download.go`) — bounded worker pool over the
     merged work list, each writing to `tempDir/<original-filename-from-url>`
     with a collision-safe temp name if two different SourceURLs would
     produce the same temp filename (append a short counter suffix
     internally; this is independent of the destDir disambiguation in
     step 8). Record per-item outcome (`downloaded` w/ local path+size, or
     `download-failed:<reason>`) in the manifest map.
   - Phase 4: dedup (`dedup.go`) over successfully-downloaded temp files.
   - Phase 5: dimensions (`dimensions.go`) over dedup survivors.
   - Phase 6: move (`move.go`) survivors to `destDir` with disambiguation;
     update `File`/`Original`/`Status="kept"` in manifest map.
   - Phase 7: cleanup — remove tempDir copies of moved files only.
   - Phase 8: `loadManifest` existing `destDir/manifest.json`, merge (this
     run's entries win on `SourceURL` collision), `writeManifest`, build
     `*Manifest` from the merged map, return.

7. **Dedup** (`dedup.go`)
   - `groupByName(files []downloadedFile) map[string][]downloadedFile`
   - `groupBySize(group []downloadedFile) map[int64][]downloadedFile`
   - `confirmDuplicates(group []downloadedFile) (kept downloadedFile, dupes []downloadedFile)`
     — blake3 hash each file in the size-group; first-seen hash wins, rest
     marked `duplicate-of:<hash>`.
   - Pure functions, no I/O side effects beyond reading file bytes for
     hashing — easy to unit test with `t.TempDir()` fixtures.

8. **Dimensions** (`dimensions.go`)
   - `decodeDimensions(path string) (w, h int, ok bool)`: open file,
     `image.DecodeConfig`; blank-import `_ "golang.org/x/image/webp"` and
     stdlib `_ "image/jpeg"`, `_ "image/png"`, `_ "image/gif"` at package
     level so the format registry is populated. Swallow decode errors,
     return `ok=false`.

9. **Move** (`move.go`)
   - `resolveDestName(destDir, wantName string) string`: `os.Stat` loop
     appending `-2`, `-3`, ... before the extension until no collision.
   - `moveToDestination(...)`: `os.Rename` (same-filesystem fast path);
     document that cross-filesystem tempDir/destDir configs would need
     copy+remove — add that fallback only if `os.Rename` returns
     `LinkError`/`EXDEV`, otherwise keep it simple.

10. **Concurrency helper** — small internal worker-pool (`errgroup` from
    `golang.org/x/sync` or a hand-rolled semaphore channel) reused across
    classify/discover/download phases with `WithConcurrency` as the bound.

11. **Tests**
    - `httptest.Server` serving fixture HTML (with `<img>` + linked images)
      and fixture image bytes (a couple of tiny real PNGs/JPEGs with known
      dimensions, one duplicate pair, one webp) — drives an integration-style
      test of the full `Run` pipeline.
    - `dedup_test.go`: unit tests for each of the three grouping stages in
      isolation, plus the documented cross-name-duplicate miss.
    - `move_test.go`: collision disambiguation (`-2`, `-3`), never-overwrite.
    - `manifest_test.go`: load-missing-file, upsert-by-SourceURL across two
      simulated runs, atomic write.
    - One test asserting a single failed download (mock 500) doesn't abort
      the batch and produces the right `download-failed` entry.

## Order of implementation

Manifest types → classify → discover → dedup → dimensions → move →
orchestration (`Run`) wiring it all together → concurrency pass → tests
throughout (write each unit's test alongside it, integration test last).

## Risks / things to watch during implementation

- `os.Rename` across the tempDir/destDir boundary fails with `EXDEV` if
  they're on different filesystems/mounts — confirm intended deployment
  keeps both under the same volume, or add the copy+remove fallback.
- `golang.org/x/image/webp` decode-only support in the stdlib `image`
  registry — confirm blank-import registration is sufficient for
  `image.DecodeConfig` (it is, but worth a smoke test with a real .webp
  fixture).
- goquery discovery of `data-src` covers common lazy-load patterns but not
  every framework's convention (e.g. `srcset`); explicitly out of scope
  unless the user asks to extend it.
