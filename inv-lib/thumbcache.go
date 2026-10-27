package invlib

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/nfnt/resize"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// ErrAnimatedWebP is returned by (*ThumbCache).Generate for an animated
// WebP source whose first frame could not be extracted with ffmpeg (most
// commonly because ffmpeg is not installed); the underlying cause is
// wrapped alongside it. The Go WebP decoder cannot read animated files at
// all, so ffmpeg is the only way to thumbnail one.
var ErrAnimatedWebP = fmt.Errorf("animated WebP is not supported")

// ThumbCache stores JPEG thumbnails on disk, in a single directory tree
// shared by every inv-lib consumer, keyed by the BLAKE3 content hash of
// the source image (see HashFile) rather than its path. Because the key is
// the source's own content hash — the same hash stored in files.hash —
// every caller that generates a thumbnail for the same image, from any
// program, lands on the same cache entry: there is nothing to invalidate,
// and a moved or renamed source still finds its existing thumbnail.
type ThumbCache struct {
	// Dir is the root directory thumbnails are stored under.
	Dir string
	// Size is the longest-edge pixel size thumbnails are generated at.
	Size uint
}

// DefaultThumbCacheDir returns the directory thumbnails of the given size
// are cached in by default: a dedicated "inv-lib/thumbnails/<size>"
// subdirectory of the OS's per-user cache directory (e.g.
// ~/Library/Caches on macOS, $XDG_CACHE_HOME or ~/.cache on Linux).
func DefaultThumbCacheDir(size uint) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(base, "inv-lib", "thumbnails", fmt.Sprintf("%d", size)), nil
}

// NewThumbCache creates (or reuses) an on-disk thumbnail cache rooted at
// DefaultThumbCacheDir, sized for thumbnails up to size pixels on their
// longest edge. The cache directory tree is created lazily by Generate, so
// NewThumbCache itself never touches disk beyond resolving the path.
func NewThumbCache(size uint) (*ThumbCache, error) {
	dir, err := DefaultThumbCacheDir(size)
	if err != nil {
		return nil, err
	}
	return &ThumbCache{Dir: dir, Size: size}, nil
}

// thumbnailPath returns where the thumbnail for a source image with
// content hash hash is (or would be) stored, sharded into a two-hex-
// character subdirectory the way git shards its object store, which keeps
// any one directory from accumulating too many entries.
func (c *ThumbCache) thumbnailPath(hash string) string {
	return filepath.Join(c.Dir, hash[:2], hash+".jpg")
}

// Path returns the on-disk cache path a thumbnail for path would be (or
// is) stored at, whether or not it has been generated yet. Since the
// cache key is path's own content hash (see HashFile), this reads path to
// compute it — a missing or unreadable file surfaces here rather than
// later in Generate.
func (c *ThumbCache) Path(path string) (string, error) {
	hash, err := HashFile(path)
	if err != nil {
		return "", fmt.Errorf("hash %q: %w", path, err)
	}
	return c.thumbnailPath(hash), nil
}

// Generate returns the path to a cached thumbnail for path, generating one
// first if it doesn't already exist. If a thumbnail already exists, it is
// reused as-is unless force is true — Generate never re-checks the source
// file's mtime on its own, so routine loads (opening a directory, paging
// through results, re-opening a viewer) never pay to regenerate thumbnails
// that are already there.
func (c *ThumbCache) Generate(path string, force bool) (string, error) {
	dst, err := c.Path(path)
	if err != nil {
		return "", err
	}

	if !force {
		if _, err := os.Stat(dst); err == nil {
			return dst, nil
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	isAnimated, err := IsAnimatedWebP(f)
	if err != nil {
		return "", fmt.Errorf("check animated WebP: %w", err)
	}

	var src image.Image
	if isAnimated {
		src, err = AnimatedWebPFirstFrame(path)
		if err != nil {
			return "", err
		}
	} else {
		src, _, err = image.Decode(f)
		if err != nil {
			return "", fmt.Errorf("decode %q: %w", path, err)
		}
	}

	thumb := resize.Thumbnail(c.Size, c.Size, src, resize.Lanczos3)

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create cache dir %q: %w", dir, err)
	}

	// A temp file unique per call (rather than a name derived from dst)
	// means two concurrent Generate calls for the same path — even from
	// different processes sharing this cache directory — never share, and
	// truncate, each other's file; the rename below is what makes the
	// final write atomic.
	out, err := os.CreateTemp(dir, filepath.Base(dst)+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmp := out.Name()

	err = jpeg.Encode(out, thumb, &jpeg.Options{Quality: 88})
	closeErr := out.Close()
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("encode %q: %w", path, err)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("close temp file: %w", closeErr)
	}

	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("rename temp file: %w", err)
	}
	return dst, nil
}

// WarmAllResult summarizes a WarmAll run.
type WarmAllResult struct {
	// Total is how many image files WarmAll found in db (excluding any
	// already marked missing).
	Total int
	// Generated is how many of those didn't have a cached thumbnail yet
	// and now do.
	Generated int
	// Skipped is how many already had a cached thumbnail.
	Skipped int
	// Failed is how many could not be thumbnailed (a missing source file,
	// an undecodable image, or an animated WebP).
	Failed int
}

// WarmAll ensures every image file row in db has a thumbnail in cache. If
// force is false, it only generates thumbnails for files that don't have
// one yet, so repeat runs only do the work a previous run left undone —
// the same property that makes it safe to run this unconditionally after
// a bulk import, rather than only on first view. If force is true, every
// file's thumbnail is regenerated unconditionally (see
// (*ThumbCache).Generate's own force parameter) — the mode a caller wants
// after switching cache to a different Size, where "already cached" at
// the new size can't be trusted to mean what it used to.
//
// Files are processed concurrently, concurrency-wide; a value <= 0 uses
// runtime.GOMAXPROCS(0). If progress is non-nil, it is called once per
// file, in no particular order, after that file's outcome is known. err
// is non-nil only when generating that file's thumbnail failed; WarmAll
// continues with the rest of the set regardless, recording the failure
// in the returned WarmAllResult rather than aborting.
//
// WarmAll returns as soon as ctx is cancelled, along with the partial
// WarmAllResult and ctx.Err() — files already in flight still finish,
// but no new ones are started.
func WarmAll(ctx context.Context, db *DB, cache *ThumbCache, concurrency int, force bool, progress func(done, total int, path string, err error)) (WarmAllResult, error) {
	files, err := db.ListImageFiles(ctx)
	if err != nil {
		return WarmAllResult{}, fmt.Errorf("warm all: %w", err)
	}

	if concurrency <= 0 {
		concurrency = runtime.GOMAXPROCS(0)
	}

	result := WarmAllResult{Total: len(files)}

	var (
		mu   sync.Mutex
		done int
	)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

filesLoop:
	for _, f := range files {
		select {
		case <-ctx.Done():
			break filesLoop
		default:
		}

		wg.Add(1)
		go func(f *File) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			alreadyCached := false
			if !force {
				if dst, pathErr := cache.Path(f.Path); pathErr == nil {
					if _, statErr := os.Stat(dst); statErr == nil {
						alreadyCached = true
					}
				}
			}

			var genErr error
			if !alreadyCached {
				_, genErr = cache.Generate(f.Path, force)
			}

			mu.Lock()
			done++
			switch {
			case genErr != nil:
				result.Failed++
			case alreadyCached:
				result.Skipped++
			default:
				result.Generated++
			}
			if progress != nil {
				progress(done, result.Total, f.Path, genErr)
			}
			mu.Unlock()
		}(f)
	}
	wg.Wait()

	return result, ctx.Err()
}

// AnimatedWebPFirstFrame decodes the first frame of the animated WebP at
// path by piping it through ffmpeg. If ffmpeg is missing or fails, the
// returned error wraps ErrAnimatedWebP (test with errors.Is) so callers can
// treat it as "no thumbnail available" rather than a broken file.
func AnimatedWebPFirstFrame(path string) (image.Image, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg not found in PATH: %v", ErrAnimatedWebP, err)
	}

	cmd := exec.Command(bin, "-loglevel", "error", "-i", path,
		"-frames:v", "1", "-f", "image2pipe", "-c:v", "png", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%w: ffmpeg first frame of %q: %v: %s",
			ErrAnimatedWebP, path, err, bytes.TrimSpace(stderr.Bytes()))
	}

	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("%w: decode ffmpeg output for %q: %v", ErrAnimatedWebP, path, err)
	}
	return img, nil
}

// IsAnimatedWebP reports whether f is an animated WebP file. It leaves f
// positioned at the beginning for subsequent decoding.
func IsAnimatedWebP(f *os.File) (bool, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	defer f.Seek(0, io.SeekStart)

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return false, err
	}

	if string(header[0:4]) != "RIFF" ||
		string(header[8:12]) != "WEBP" {
		return false, nil
	}

	riffSize := int64(binary.LittleEndian.Uint32(header[4:8]))
	remaining := riffSize - 4 // WEBP identifier is included in RIFF size.

	for remaining >= 8 {
		chunk := make([]byte, 8)
		if _, err := io.ReadFull(f, chunk); err != nil {
			return false, err
		}

		chunkSize := int64(binary.LittleEndian.Uint32(chunk[4:8]))
		if chunkSize > remaining-8 {
			return false, fmt.Errorf("invalid WebP chunk size")
		}

		remaining -= 8

		if string(chunk[0:4]) == "VP8X" {
			if chunkSize < 1 {
				return false, fmt.Errorf("invalid VP8X chunk")
			}

			var flags [1]byte
			if _, err := io.ReadFull(f, flags[:]); err != nil {
				return false, err
			}

			// VP8X animation flag is bit 1 (0x02).
			return flags[0]&0x02 != 0, nil
		}

		// WebP chunks are padded to an even byte boundary.
		skip := chunkSize + chunkSize%2
		if _, err := f.Seek(skip, io.SeekCurrent); err != nil {
			return false, err
		}
		remaining -= skip
	}

	return false, nil
}
