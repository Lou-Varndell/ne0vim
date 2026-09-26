# Prompt (draft — tweak freely)

```markdown
# Task: Go image-scraper library (goquery-based)

## Goal
A Go library that downloads images from one or more website URLs and/or direct
image URLs, deduplicates them, and lands a clean, deduplicated set of files in
a destination directory plus a manifest of what was kept/dropped.

## Non-goals
- No CLI entrypoint — library API only.
- No recursive crawling beyond the given page + directly linked images on that
  page (i.e. don't follow <a> links to other pages looking for more images).
- No image transformation/resizing — bytes are downloaded and moved as-is.

## Packages
- `github.com/PuerkitoBio/goquery` — HTML parsing/selection
- `lukechampine.com/blake3` — content hashing for dedup
- `golang.org/x/image/webp` — WebP dimension decoding (register via blank
  import so `image.DecodeConfig` can read WebP alongside stdlib jpeg/png/gif)

## Public API (idiomatic Go: functional options)

\`\`\`go
package scraper

type Downloader struct { /* ... */ }

// NewDownloader constructs a Downloader. tempDir and destDir are required
// via options; sensible defaults apply to concurrency.
func NewDownloader(opts ...Option) (*Downloader, error)

type Option func(*Downloader)

func WithTempDir(path string) Option
func WithDestDir(path string) Option
func WithConcurrency(n int) Option        // default: e.g. 8
func WithHTTPClient(c *http.Client) Option // for timeouts/transport control

// Run accepts a mix of page URLs and direct image URLs, downloads
// everything, deduplicates, and moves the survivors to destDir.
// Returns a Manifest describing the outcome.
func (d *Downloader) Run(ctx context.Context, urls []string) (*Manifest, error)
\`\`\`

## Pipeline (execute in this order)

1. **Classify inputs** — for each input URL, determine if it's a direct image
   URL (check `Content-Type` via HEAD/GET, not just file extension) or an
   HTML page to scrape.
2. **Discovery (page URLs only)** — fetch HTML with goquery, collect *all*
   image URLs before downloading anything:
   - `<img src>` / `<img data-src>` on the page
   - images linked via `<a href>` pointing at image content (same
     content-type check as above)
   - resolve all discovered URLs to absolute form against the page's base URL
3. **Download** — download all discovered + direct image URLs into `tempDir`,
   with bounded concurrency (`WithConcurrency`). Use `ctx` for cancellation;
   one failed download must not abort the batch — record the failure and
   continue.
4. **Deduplicate** — see algorithm below. Runs entirely within `tempDir`
   before anything is moved.
5. **Decode dimensions** — for each surviving (non-duplicate, downloaded)
   file, run `image.DecodeConfig` (stdlib jpeg/png/gif + `golang.org/x/image/
   webp` registered via blank import) to populate `Width`/`Height`. A decode
   failure or unsupported format is not fatal — leave `Width`/`Height` as 0
   and continue.
6. **Move to destination** — move surviving files from `tempDir` to `destDir`,
   preserving each file's original name (from the URL path / `Content-
   Disposition`). On a filename collision in `destDir` (either from this run
   or a prior run), disambiguate by appending `-2`, `-3`, etc. before the
   extension — never overwrite an existing file. Record the pre-collision
   name in `Original` and the final on-disk name in `File`.
7. **Cleanup `tempDir`** — delete the temp copies of every file that was
   successfully moved to `destDir`. Leave failed downloads and dropped
   duplicates in `tempDir` for post-run inspection.
8. **Manifest** — merge this run's entries into `<destDir>/manifest.json`
   (append/update by `SourceURL`; create the file if absent), and return the
   full `*Manifest` from `Run`.

## Deduplication algorithm

Progressive, cheapest-check-first comparison, run only within a single
`tempDir` batch:

1. Group downloaded files by filename.
2. Within a name-group, sub-group by file size.
3. Within a name+size group, compute the blake3 hash of each file and treat
   equal hashes as true duplicates — keep the first, drop the rest.

**Explicit limitation to flag to the user**: this only detects duplicates
that share a filename. Two images with different filenames but identical
byte content will NOT be deduplicated by this algorithm. If cross-name
content dedup is needed later, that requires hashing every file regardless
of name — call this out rather than silently expanding scope.

## Manifest

One record per file that entered `tempDir`. Returned as a Go struct from
`Run` **and** persisted to `<destDir>/manifest.json`: on each `Run`, load the
existing file if present, upsert entries by `SourceURL` (an entry from a
prior run with the same `SourceURL` is replaced, not duplicated), and
rewrite the file.

~~~go
type Entry struct {
	Site         string    `json:"site"`          // input URL passed to Run (page or direct image)
	SourceURL    string    `json:"source_url"`    // resolved image URL (== Site for direct-image inputs)
	File         string    `json:"file,omitempty"`         // final on-disk name in destDir (empty if dropped)
	Original     string    `json:"original_file,omitempty"` // name before collision disambiguation
	Hash         string    `json:"hash,omitempty"`
	Status       string    `json:"status"` // kept | duplicate-of:<hash> | download-failed:<reason>
	Error        string    `json:"error,omitempty"`
	DiscoveredAt time.Time `json:"discovered_at"`

	Width  int `json:"width,omitempty"`  // 0 if dimensions couldn't be decoded
	Height int `json:"height,omitempty"`
}
~~~

## Error handling
- A single bad URL, failed download, or unparseable page must not fail the
  whole batch — collect per-URL errors into the manifest/result, and only
  return a top-level `error` from `Run` for setup failures (bad tempDir/
  destDir, context cancellation).

## Acceptance criteria
- [ ] Given N page URLs and M direct image URLs, all discoverable images are
      found before any download starts (discovery and download are separate
      phases, not interleaved).
- [ ] Downloads run with bounded concurrency, configurable via
      `WithConcurrency`; default concurrency is respected when unset.
- [ ] All files land in `tempDir` before any move to `destDir` occurs.
- [ ] Two identical files with the same original filename in the same run
      are deduplicated (only one survives), verified via blake3 equality.
- [ ] Two files with the same name but different content are both kept.
- [ ] Filename collisions against pre-existing files in `destDir` are
      disambiguated, never overwritten.
- [ ] `Run` returns a `*Manifest` with one entry per discovered file and an
      accurate status for each, and the same data is written to
      `<destDir>/manifest.json`.
- [ ] Running `Run` twice against the same `destDir` upserts `manifest.json`
      by `SourceURL` instead of duplicating or losing prior entries.
- [ ] Successfully moved files are removed from `tempDir`; failed downloads
      and dropped duplicates remain in `tempDir` after `Run` returns.
- [ ] Decodable images (jpeg/png/gif/webp) get non-zero `Width`/`Height` in
      their manifest entry; an undecodable file gets `Width=0, Height=0`
      without failing the run.
- [ ] A single failed download (4xx/5xx/timeout) does not abort the rest of
      the batch.
- [ ] Unit tests cover: dedup at each of the three stages, filename collision
      handling, manifest upsert across two `Run` calls, and partial-failure
      behavior (mocked HTTP server).

## Deliverable
A single Go module exporting `scraper.NewDownloader(...Option) (*Downloader, error)`
and `(*Downloader).Run(ctx, urls) (*Manifest, error)` as described above.
```
