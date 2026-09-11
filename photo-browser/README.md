# Photo Browser

A native Go/Fyne image browser combining a menu-driven folder picker with a
responsive thumbnail grid, with:

- File/Settings/About menu bar
- folder picker with breadcrumbs, type-ahead search, hidden-file toggle
  (Cmd+Shift+.), and go-to-path (Cmd+Shift+G)
- thumbnail disk cache under the OS user cache directory
- background thumbnail generation
- responsive thumbnail grid (column count adapts to window width)
- click thumbnail to open the full-size viewer
- previous/next navigation
- keyboard navigation
- external "Open" support
- fullscreen mode
- JPEG, PNG, GIF, WebP, BMP, TIFF

## Build

    go mod tidy
    go build -o photo-browser ./cmd/photo-browser

## Run

    ./photo-browser -dir ~/Images

The cache is automatically invalidated when the source image is newer than
the cached thumbnail. Use File > Open Folder, or the toolbar's Choose
Directory button, to browse a different folder — both open the same picker.

## Notes

The example uses `github.com/nfnt/resize` for thumbnail generation. If you
want maximum throughput for a very large library, replace this with a
worker-pool thumbnail pipeline and a stronger cache key based on path,
size, and modification time or BLAKE3.
