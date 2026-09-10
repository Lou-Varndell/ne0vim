# Fyne Image Browser

A native Go/Fyne image browser with:

- directory selection
- thumbnail disk cache under the OS user cache directory
- background thumbnail generation
- responsive thumbnail grid
- double-click/click thumbnail viewer
- previous/next navigation
- keyboard navigation
- external "Open" support
- JPEG, PNG, GIF, WebP, BMP, TIFF

## Build

    go mod tidy
    go build -o image-browser ./cmd/image-browser

## Run

    ./image-browser -dir ~/Images

The cache is automatically invalidated when the source image is newer than
the cached thumbnail.

## Notes

The example uses `github.com/nfnt/resize` for thumbnail generation. If you
want maximum throughput for a very large library, replace this with a
worker-pool thumbnail pipeline and a stronger cache key based on path,
size, and modification time or BLAKE3.
