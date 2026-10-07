# Inventory Manager

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
- every scanned image indexed by BLAKE3 content hash into a local SQLite
  inventory database (`internal/inventory`) — the foundation for the
  inventory management and database features this application will grow

## Build

    go mod tidy
    go build -o inventory-manager ./cmd/inventory-manager

## Run

    ./inventory-manager -root ~/Images

The thumbnail cache is automatically invalidated when the source image is
newer than the cached thumbnail. Use File > Open Folder, or the toolbar's
Choose Directory button, to browse a different folder — both open the same
picker.

The inventory database lives at `$XDG_CONFIG_HOME/inventory-manager/inventory.db`
(or the platform equivalent of `os.UserConfigDir()`), and is populated as
directories are scanned. It records each image's path, BLAKE3 hash, size,
and dimensions, which is enough to detect duplicates by content
(`internal/inventory.Store.DuplicatesOf`) ahead of future organize/dedupe
features.
