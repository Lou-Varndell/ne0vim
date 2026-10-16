// Package scan implements fddup's three-stage duplicate detection pipeline.
//
// Pipeline:
//
//	stage 1  walk all roots, group regular files by size
//	stage 2  hash the first 64 KB of each size-collision candidate
//	stage 3  full-hash survivors of stage 2
//
// Stages 2 and 3 use the same fan-out/fan-in worker pool topology:
//
//	feeder ──> jobs ──> [worker]*N ──> results ──> aggregator
//
// The aggregator runs on the calling goroutine, so the cluster map is only
// touched by one goroutine and needs no lock.
//
// Idempotency is delegated to the queue: scan never modifies files; it
// writes pending deletions and returns. Re-running on the same trees adds
// missed duplicates but doesn't double-queue (see queue.QueueDuplicates).
package scan

import (
	"context"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"fddup/internal/queue"

	"fddup/internal/skip"
)

// Config is the immutable input to a scan run. Workers is exposed because
// the optimal value is storage-dependent: 1 for HDD, ncpu for SSD/NVMe,
// 16-64 for high-latency network mounts.
type Config struct {
	Roots   []string
	Workers int

	// InvDBLookup, when set, is consulted during stage 3 (full-file hash)
	// before falling back to this package's own hash_cache. Wiring one up
	// (see internal/invdb) is the caller's job via -inv-db — this package
	// has no inv-lib dependency of its own, so fddup's core scan-then-
	// delete model stays usable without it.
	InvDBLookup InvDBLookup
}

// InvDBLookup resolves a file's full-content hash from an external source
// (inv-lib's shared inventory database) before fddup falls back to its own
// hash_cache or a fresh read. ok must be false unless the implementation
// can also confirm size and modTime still match what it has on record — a
// stale hash fed into a duplicate decision is worse than no hash at all.
type InvDBLookup func(path string, size int64, modTime time.Time) (hash [32]byte, ok bool, err error)

// fileInfo is a candidate file passed between stages.
type fileInfo struct {
	path    string
	size    int64
	mtimeNs int64
}

// hashed is a file with its computed hash, produced by hash workers.
type hashed struct {
	path string
	size int64
	hash [32]byte
}

// clusterKey is what defines a duplicate cluster: same size AND same hash.
// Using a struct (not a string concatenation) keeps the hot path
// allocation-free.
type clusterKey struct {
	size int64
	hash [32]byte
}

// Run executes the full scan and queues found duplicates in the database.
// Progress is written to stderr; the report is written to stdout.
func Run(ctx context.Context, q *queue.DB, cfg Config) error {
	scanID, err := q.StartScan(strings.Join(cfg.Roots, ","))
	if err != nil {
		return err
	}

	// Stage 1.
	bySize, err := walk(ctx, cfg.Roots)
	if err != nil {
		return fmt.Errorf("stage 1: %w", err)
	}

	// Build the candidate list. 0-byte files are dropped: all empty files
	// hash identically and aren't real duplicates.
	var candidates []fileInfo
	for size, files := range bySize {
		if size == 0 || len(files) < 2 {
			continue
		}
		candidates = append(candidates, files...)
	}
	// Path-sorted candidate ordering keeps reads roughly inode-adjacent on
	// HDDs. On SSD/NVMe it doesn't matter; the cost is negligible either way.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	fmt.Fprintf(os.Stderr, "stage 1 -> %d candidates after size grouping\n", len(candidates))

	if len(candidates) == 0 {
		fmt.Println("No duplicate candidates found.")
		return q.CompleteScan(scanID)
	}

	// Build a lookup from path -> fileInfo so we can recover mtime after
	// stage 2 collapses candidates into path-only cluster maps.
	fiByPath := make(map[string]fileInfo, len(candidates))
	for _, fi := range candidates {
		fiByPath[fi.path] = fi
	}

	// Stage 2. inv.db only ever has a whole-file hash on record, so it has
	// nothing to offer the head-hash stage — pass a nil lookup.
	headClusters, err := hashWorkers(ctx, candidates, cfg.Workers, q, hashHead, "stage 2 head-hash", 0, nil)
	if err != nil {
		return fmt.Errorf("stage 2: %w", err)
	}

	// Decide each cluster's stage-3 fate.
	var (
		skipped     [][]string
		shortPaths  [][]string
		shortKeys   []clusterKey
		needFull    []fileInfo
		bytesToRead int64
	)
	for k, paths := range headClusters {
		if len(paths) < 2 {
			continue
		}
		if skip.MatchAll(paths) {
			skipped = append(skipped, paths)
			continue
		}
		if k.size <= HeadSize {
			// Head hash already covers the entire file — promote without
			// re-reading.
			shortKeys = append(shortKeys, k)
			shortPaths = append(shortPaths, paths)
			continue
		}
		for _, p := range paths {
			fi := fiByPath[p]
			needFull = append(needFull, fi)
			bytesToRead += k.size
		}
	}
	sort.Slice(needFull, func(i, j int) bool { return needFull[i].path < needFull[j].path })

	fmt.Fprintf(os.Stderr,
		"stage 2 -> %d skipped, %d short-circuit clusters, %d files / %.2f GB need full hash\n",
		len(skipped), len(shortKeys), len(needFull), float64(bytesToRead)/(1<<30))

	// Stage 3.
	fullClusters, err := hashWorkers(ctx, needFull, cfg.Workers, q, hashFull, "stage 3 full-hash", bytesToRead, cfg.InvDBLookup)
	if err != nil {
		return fmt.Errorf("stage 3: %w", err)
	}

	// Merge stage-2 short-circuits into the cluster map (their head hash
	// IS their full hash).
	for i, k := range shortKeys {
		fullClusters[k] = append(fullClusters[k], shortPaths[i]...)
	}

	return finalize(q, scanID, fullClusters, skipped)
}

// walk recursively walks each root, applying skip.Dirs, and groups regular
// files by size. Symlinks, devices, sockets, and 0-byte files are skipped
// here. Errors on individual files are logged; the walk continues.
func walk(ctx context.Context, roots []string) (map[int64][]fileInfo, error) {
	bySize := make(map[int64][]fileInfo)
	var totalFiles, totalBytes int64
	last := time.Now()

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			// Cooperative cancellation — checked once per entry so a Ctrl-C
			// during a long walk doesn't leave the caller hanging.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return nil
			}
			if d.IsDir() {
				if skip.Dirs[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return nil
			}
			fi := fileInfo{path: path, size: info.Size(), mtimeNs: info.ModTime().UnixNano()}
			bySize[fi.size] = append(bySize[fi.size], fi)
			totalBytes += fi.size
			totalFiles++
			if time.Since(last) > time.Second {
				fmt.Fprintf(os.Stderr, "\rwalk: %d files, %.2f GB     ",
					totalFiles, float64(totalBytes)/(1<<30))
				last = time.Now()
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(os.Stderr, "\rwalk: %d files, %.2f GB total\n",
		totalFiles, float64(totalBytes)/(1<<30))
	return bySize, nil
}

// hashWorkers is the shared engine for stages 2 and 3. The hashFn parameter
// is either hashHead or hashFull; everything else (channels, progress,
// error handling) is identical.
//
// q is used for cache lookups and stores — cache hits skip the file read
// entirely, which makes interrupted scans fast to resume. invLookup is
// consulted ahead of q (see cachedHash) and is nil for stage 2.
//
// totalBytes > 0 enables MB/s rate display (only meaningful in stage 3
// where we know up front how much data we'll read).
func hashWorkers(
	ctx context.Context,
	candidates []fileInfo,
	workers int,
	q *queue.DB,
	hashFn func(string) ([32]byte, error),
	label string,
	totalBytes int64,
	invLookup InvDBLookup,
) (map[clusterKey][]string, error) {
	if len(candidates) == 0 {
		return map[clusterKey][]string{}, nil
	}

	isHead := label == "stage 2 head-hash"

	jobs := make(chan fileInfo, workers*2)
	results := make(chan hashed, workers*2)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for fi := range jobs {
				h, cacheHit, err := cachedHash(q, fi, isHead, hashFn, invLookup)
				if err != nil {
					fmt.Fprintf(os.Stderr, "\n%s: %s: %v\n", label, fi.path, err)
					continue
				}
				_ = cacheHit // used only for future instrumentation
				select {
				case results <- hashed{fi.path, fi.size, h}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Feeder.
	go func() {
		defer close(jobs)
		for _, c := range candidates {
			select {
			case jobs <- c:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Closer.
	go func() {
		wg.Wait()
		close(results)
	}()

	// Aggregator runs on the caller's goroutine — no lock needed.
	clusters := make(map[clusterKey][]string)
	start := time.Now()
	last := start
	var done int
	var bytesHashed int64

	for r := range results {
		k := clusterKey{r.size, r.hash}
		clusters[k] = append(clusters[k], r.path)
		done++
		bytesHashed += r.size
		if time.Since(last) > time.Second {
			if totalBytes > 0 {
				rate := float64(bytesHashed) / time.Since(start).Seconds() / (1 << 20)
				fmt.Fprintf(os.Stderr, "\r%s: %.2f / %.2f GB (%.1f MB/s, %d files)     ",
					label, float64(bytesHashed)/(1<<30), float64(totalBytes)/(1<<30), rate, done)
			} else {
				fmt.Fprintf(os.Stderr, "\r%s: %d / %d files     ",
					label, done, len(candidates))
			}
			last = time.Now()
		}
	}
	fmt.Fprintf(os.Stderr, "\r%s: %d files in %s\n",
		label, done, time.Since(start).Round(time.Second))

	if err := ctx.Err(); err != nil {
		return clusters, err
	}
	return clusters, nil
}

// cachedHash returns the hash for fi, using the cache when valid. On a miss
// it calls hashFn, stores the result, and returns it. cacheHit is true when
// the read was skipped.
//
// invLookup, when non-nil, is consulted before this package's own
// hash_cache: a hit there means some other inv-lib consumer (image-browser,
// site-scraper) already hashed these exact bytes, so fddup never needs to
// read the file at all. A hit is also written into hash_cache, so a later
// scan without -inv-db (or after the inv.db record goes stale) doesn't
// lose it.
func cachedHash(q *queue.DB, fi fileInfo, isHead bool, hashFn func(string) ([32]byte, error), invLookup InvDBLookup) (h [32]byte, cacheHit bool, err error) {
	if !isHead && invLookup != nil {
		if ih, ok, lookupErr := invLookup(fi.path, fi.size, time.Unix(0, fi.mtimeNs)); lookupErr != nil {
			fmt.Fprintf(os.Stderr, "\ninv-db lookup %s: %v\n", fi.path, lookupErr)
		} else if ok {
			if err := q.SetCachedHash(fi.path, fi.size, fi.mtimeNs, nil, &ih); err != nil {
				fmt.Fprintf(os.Stderr, "\ncache store %s: %v\n", fi.path, err)
			}
			return ih, true, nil
		}
	}

	cached, err := q.GetCachedHash(fi.path, fi.size, fi.mtimeNs)
	if err != nil {
		return h, false, err
	}
	if cached != nil {
		if isHead && cached.HasHead {
			return cached.HeadHash, true, nil
		}
		if !isHead && cached.HasFull {
			return cached.FullHash, true, nil
		}
	}

	h, err = hashFn(fi.path)
	if err != nil {
		return h, false, err
	}

	if isHead {
		err = q.SetCachedHash(fi.path, fi.size, fi.mtimeNs, &h, nil)
	} else {
		err = q.SetCachedHash(fi.path, fi.size, fi.mtimeNs, nil, &h)
	}
	if err != nil {
		// Cache write failure is non-fatal — the hash is still valid.
		fmt.Fprintf(os.Stderr, "\ncache store %s: %v\n", fi.path, err)
		err = nil
	}
	return h, false, nil
}

// finalize prints the report and queues real duplicates in the database.
// Skip-listed clusters are reported but not queued.
func finalize(q *queue.DB, scanID int64, clusters map[clusterKey][]string, skipped [][]string) error {
	for _, paths := range skipped {
		fmt.Printf("[SKIPPED: false-positive filenames]\n")
		for _, p := range paths {
			fmt.Printf("  - %s\n", p)
		}
		fmt.Println()
	}

	keys := make([]clusterKey, 0, len(clusters))
	for k, paths := range clusters {
		if len(paths) >= 2 {
			keys = append(keys, k)
		}
	}
	// Biggest-reclaim clusters first.
	sort.Slice(keys, func(i, j int) bool { return keys[i].size > keys[j].size })

	var totalQueued int
	var totalBytes int64

	for _, k := range keys {
		paths := clusters[k]
		// Final skip check covers clusters that became "all-skippable" after
		// stage-2 short-circuit promotion bypassed the earlier filter.
		if skip.MatchAll(paths) {
			fmt.Printf("[SKIPPED: false-positive filenames]\n")
			for _, p := range paths {
				fmt.Printf("  - %s\n", p)
			}
			fmt.Println()
			continue
		}

		keeper := pickKeeper(paths)
		var dupes []string
		for _, p := range paths {
			if p != keeper {
				dupes = append(dupes, p)
			}
		}

		hexHash := hex.EncodeToString(k.hash[:])
		if err := q.QueueDuplicates(scanID, hexHash, k.size, keeper, dupes); err != nil {
			return fmt.Errorf("queue: %w", err)
		}

		fmt.Printf("size=%d blake3=%s\n", k.size, hexHash)
		fmt.Printf("  KEEP    %s\n", keeper)
		for _, p := range dupes {
			fmt.Printf("  QUEUED  %s\n", p)
			totalQueued++
			totalBytes += k.size
		}
		fmt.Println()
	}

	fmt.Printf("Queued %d duplicate(s) totalling %.2f MB. Run `fddup delete` to remove.\n",
		totalQueued, float64(totalBytes)/(1<<20))
	return q.CompleteScan(scanID)
}
