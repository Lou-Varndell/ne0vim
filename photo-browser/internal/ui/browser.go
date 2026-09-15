package ui

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"photo-browser/internal/filedialog"
	"photo-browser/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// previewThumbCount caps how many thumbnails are shown per subdirectory in
// the recursive preview, so glancing at a directory of many large
// subdirectories stays fast and uncluttered.
const previewThumbCount = 10

// dirPreview holds one subdirectory's full image listing for the recursive
// preview. items is the complete scan (not just the previewThumbCount shown),
// so tapping a previewed thumbnail can open the viewer with correct
// prev/next navigation across the whole subdirectory.
type dirPreview struct {
	path  string
	items []images.Item
}

// Browser is the main window's content: a toolbar plus a scrollable
// thumbnail grid (or, in preview mode, one thumbnail row per subdirectory).
type Browser struct {
	win              fyne.Window
	root             *fyne.Container
	grid             *fyne.Container
	previewBox       *fyne.Container
	scroll           *container.Scroll
	path             *widget.Entry
	status           *widget.Label
	previewToggle    *widget.Check
	backBtn          *widget.Button
	thumbSize        int
	thumbCellHeight  float32 // thumbnail + label height, shared by the flat grid and preview rows
	items            []images.Item
	previews         []dirPreview
	previewMode      bool
	previewRoot      string // dir to return to when backBtn is tapped; "" if not drilled in
	cache            *images.Cache // nil if the on-disk cache could not be created; thumbnails are then skipped
	thumbSem         chan struct{} // bounds concurrent thumbnail decode/resize goroutines
	lastOpenLocation fyne.ListableURI

	mu             sync.Mutex
	loadGeneration int                // bumped on every LoadDirectory/LoadPreview call; lets a superseded load discard its results
	loadCancel     context.CancelFunc // cancels whichever load is currently in flight
}

type thumbnailWidget struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
}

type clickableLabel struct {
	widget.BaseWidget
	label *widget.Label
	onTap func()
}

func newClickableLabel(text string, style fyne.TextStyle, onTap func()) *clickableLabel {
	l := &clickableLabel{
		label: widget.NewLabelWithStyle(text, fyne.TextAlignLeading, style),
		onTap: onTap,
	}
	l.ExtendBaseWidget(l)
	return l
}

func (l *clickableLabel) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(l.label)
}

func (l *clickableLabel) Tapped(*fyne.PointEvent) {
	if l.onTap != nil {
		l.onTap()
	}
}

func newThumbnailWidget(
	content fyne.CanvasObject,
	onTap func(),
) *thumbnailWidget {
	w := &thumbnailWidget{
		content: content,
		onTap:   onTap,
	}

	w.ExtendBaseWidget(w)
	return w
}

func (w *thumbnailWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(w.content)
}

func (w *thumbnailWidget) Tapped(*fyne.PointEvent) {
	if w.onTap != nil {
		w.onTap()
	}
}

// NewBrowser builds a Browser that will show dir once LoadDirectory or
// LoadPreview is called on it; it does not scan dir itself.
func NewBrowser(dir string) *Browser {
	cache, err := images.NewCache(150)
	if err != nil {
		// Thumbnails are a convenience, not core functionality, so a cache
		// failure (e.g. an unwritable cache dir) degrades to no thumbnails
		// rather than making the app unusable.
		log.Printf("thumbnail cache unavailable, thumbnails will be skipped: %v", err)
	}

	b := &Browser{
		thumbSize: 150,
		cache:     cache,
		thumbSem:  make(chan struct{}, runtime.GOMAXPROCS(0)),
	}

	b.path = widget.NewEntry()
	b.path.SetText(dir)

	b.status = widget.NewLabel("")

	choose := widget.NewButton("Choose Directory", func() {
		b.OpenFolderDialog()
	})

	refresh := widget.NewButton("Refresh", func() {
		if b.currentlyPreviewing() {
			b.loadPreviewAsync(b.path.Text)
		} else {
			b.loadDirectoryAsync(b.path.Text)
		}
	})

	b.previewToggle = widget.NewCheck("Preview subdirectories", func(checked bool) {
		b.clearBack()
		if checked {
			b.loadPreviewAsync(b.path.Text)
		} else {
			b.loadDirectoryAsync(b.path.Text)
		}
	})

	b.backBtn = widget.NewButton("← Back to Preview", func() {
		root := b.previewRoot
		b.clearBack()
		b.loadPreviewAsync(root)
	})
	b.backBtn.Hide()

	sampleLabel := widget.NewLabel("Wg")
	b.thumbCellHeight = float32(b.thumbSize) + theme.Padding() + sampleLabel.MinSize().Height

	b.grid = container.New(&ThumbnailGridLayout{
		CellWidth:  float32(b.thumbSize),
		CellHeight: b.thumbCellHeight,
	})
	b.previewBox = container.NewVBox()
	// Vertical-only: a preview row wider than the window must not make the
	// whole page horizontally draggable too (that would drag headings
	// out of view along with it).
	b.scroll = container.NewVScroll(b.grid)

	toolbar := container.NewBorder(
		nil, nil,
		nil,
		container.NewHBox(b.backBtn, b.previewToggle, choose, refresh),
		b.path,
	)

	b.root = container.NewBorder(toolbar, b.status, nil, nil, b.scroll)
	return b
}

// SetWindow associates w with the browser, so dialogs it opens (folder
// picker, error dialogs) are anchored to it.
func (b *Browser) SetWindow(w fyne.Window) {
	b.win = w
}

// Canvas returns the browser's root content, suitable for w.SetContent.
func (b *Browser) Canvas() fyne.CanvasObject {
	return b.root
}

// InitialSize returns the window size the browser is designed to open at.
func (b *Browser) InitialSize() fyne.Size {
	return fyne.NewSize(1200, 850)
}

// OpenFolderDialog shows the shared folder-picker dialog (breadcrumbs,
// type-ahead, hidden-file toggle, go-to-path) and loads whatever directory
// the user selects. It is the single implementation behind every entry
// point that lets the user choose a folder, so they all behave identically.
func (b *Browser) OpenFolderDialog() {
	d := filedialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil || lu == nil {
			return
		}

		b.lastOpenLocation = lu
		b.clearBack()
		if b.previewToggle.Checked {
			b.loadPreviewAsync(lu.Path())
		} else {
			b.loadDirectoryAsync(lu.Path())
		}
	}, b.win)

	if b.lastOpenLocation != nil {
		d.SetLocation(b.lastOpenLocation)
	}

	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// beginLoad cancels whichever load is currently in flight, derives a fresh
// cancellable context from parent, and bumps the generation counter. Every
// call to LoadDirectory/LoadPreview goes through this — whether invoked
// directly (main.go's startup load) or via loadDirectoryAsync/loadPreviewAsync
// — so two overlapping scans (e.g. the user hits Refresh, then immediately
// picks a different folder) can never race to apply stale results, and a
// long-running recursive walk actually stops when superseded.
func (b *Browser) beginLoad(parent context.Context) (context.Context, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.loadCancel != nil {
		b.loadCancel()
	}
	ctx, cancel := context.WithCancel(parent)
	b.loadCancel = cancel
	b.loadGeneration++
	return ctx, b.loadGeneration
}

func (b *Browser) isCurrentLoad(generation int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return generation == b.loadGeneration
}

// loadDirectoryAsync runs LoadDirectory on a background goroutine so the
// caller (a UI callback running on the Fyne event loop) never blocks on a
// slow filesystem scan.
func (b *Browser) loadDirectoryAsync(dir string) {
	go func() {
		if err := b.LoadDirectory(context.Background(), dir); err != nil {
			log.Printf("load directory %q: %v", dir, err)
		}
	}()
}

// loadPreviewAsync is the LoadPreview equivalent of loadDirectoryAsync.
func (b *Browser) loadPreviewAsync(dir string) {
	go func() {
		if err := b.LoadPreview(context.Background(), dir); err != nil {
			log.Printf("load preview %q: %v", dir, err)
		}
	}()
}

// LoadDirectory scans dir for images and updates the browser's UI with the
// result. It performs the (potentially slow) directory scan on the calling
// goroutine, but marshals every widget update through fyne.Do, so it is
// safe to call from either a UI callback or a goroutine the caller started.
// If a newer LoadDirectory/LoadPreview call supersedes this one before it
// finishes, its result is discarded instead of overwriting the newer data.
func (b *Browser) LoadDirectory(ctx context.Context, dir string) error {
	ctx, generation := b.beginLoad(ctx)

	items, err := images.Scan(ctx, dir)
	if err != nil {
		if b.isCurrentLoad(generation) {
			fyne.Do(func() {
				b.status.SetText(err.Error())
			})
		}
		return err
	}

	b.mu.Lock()
	if generation != b.loadGeneration {
		b.mu.Unlock()
		return nil
	}
	b.items = items
	b.previewMode = false
	b.mu.Unlock()

	fyne.Do(func() {
		if !b.isCurrentLoad(generation) {
			return
		}
		b.path.SetText(dir)
		b.status.SetText(fmt.Sprintf("%d images", len(items)))
		b.rebuild()
	})
	return nil
}

// LoadPreview walks dir's entire subdirectory tree (not dir's own images)
// and shows one heading + capped thumbnail row per subdirectory, at any
// depth, that directly contains images. Subdirectories with no images of
// their own get no row but are still descended into, so a listing's photos
// nested several levels down (e.g. dir/A/B/C) still surface as a row for
// C. Like LoadDirectory, the scan runs on the calling goroutine and widget
// updates are marshalled through fyne.Do.
func (b *Browser) LoadPreview(ctx context.Context, dir string) error {
	ctx, generation := b.beginLoad(ctx)

	var previews []dirPreview
	total := 0

	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !d.IsDir() || path == dir {
			return nil
		}

		items, err := images.Scan(ctx, path)
		if err != nil || len(items) == 0 {
			return nil
		}
		previews = append(previews, dirPreview{path: path, items: items})
		total += len(items)
		return nil
	})
	if walkErr != nil {
		if b.isCurrentLoad(generation) {
			fyne.Do(func() {
				b.status.SetText(walkErr.Error())
			})
		}
		return walkErr
	}

	sort.Slice(previews, func(i, j int) bool {
		return strings.ToLower(previews[i].path) < strings.ToLower(previews[j].path)
	})

	b.mu.Lock()
	if generation != b.loadGeneration {
		b.mu.Unlock()
		return nil
	}
	b.previews = previews
	b.previewMode = true
	b.mu.Unlock()

	fyne.Do(func() {
		if !b.isCurrentLoad(generation) {
			return
		}
		b.path.SetText(dir)
		b.status.SetText(fmt.Sprintf("%d subdirectories, %d images", len(previews), total))
		b.rebuildPreview()
	})
	return nil
}

// drillInto leaves the recursive preview and shows dir's own images as a
// normal flat grid, remembering the preview root so backBtn can return to it.
func (b *Browser) drillInto(dir string) {
	b.previewRoot = b.path.Text
	b.backBtn.Show()
	b.loadDirectoryAsync(dir)
}

// clearBack discards any pending "return to preview" target. Called whenever
// the user starts a fresh navigation (choosing a folder, toggling preview
// mode) rather than drilling in from a preview.
func (b *Browser) clearBack() {
	b.previewRoot = ""
	b.backBtn.Hide()
}

func (b *Browser) currentlyPreviewing() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.previewMode
}

func (b *Browser) rebuild() {
	b.scroll.Content = b.grid
	b.grid.Objects = nil

	b.mu.Lock()
	items := append([]images.Item(nil), b.items...)
	b.mu.Unlock()

	for i, item := range items {
		index := i
		b.grid.Add(b.thumbnail(item, func() { b.openViewer(items, index) }))
	}

	b.grid.Refresh()
	b.scroll.Refresh()
	b.root.Refresh()
}

// rebuildPreview renders the current b.previews as one section per
// subdirectory: a heading (full path + image count), an "Open Folder"
// button that drills in via drillInto, and a row of up to previewThumbCount
// thumbnails. Tapping a thumbnail opens the viewer against that
// subdirectory's *complete* item list, so prev/next still covers every
// image in the subdirectory, not just the previewed subset.
//
// The row uses PreviewRowLayout rather than an HBox or its own HScroll:
// fyne's Scroll widget consumes every wheel/trackpad event that reaches it
// regardless of direction, so a scrollable row nested under the page's
// vertical scroll would swallow scroll input before it ever reached the
// outer container, making the page look stuck. PreviewRowLayout instead
// shows however many of the up-to-previewThumbCount thumbnails fit at the
// row's current width and hides the rest, adapting live as the window is
// resized.
func (b *Browser) rebuildPreview() {
	b.scroll.Content = b.previewBox
	b.previewBox.Objects = nil

	b.mu.Lock()
	previews := append([]dirPreview(nil), b.previews...)
	b.mu.Unlock()

	if len(previews) == 0 {
		b.previewBox.Add(widget.NewLabel("No subdirectories with images found."))
	}

	for _, dp := range previews {
		pathLabel := newClickableLabel(dp.path, fyne.TextStyle{Bold: true}, func() {
			fyne.CurrentApp().Clipboard().SetContent(dp.path)
		})

		count := widget.NewLabelWithStyle(
			fmt.Sprintf("  (%d images)", len(dp.items)),
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		)

		heading := container.NewHBox(pathLabel, count)

		open := widget.NewButton("Open Folder →", func() { b.drillInto(dp.path) })
		header := container.NewBorder(nil, nil, nil, open, heading)

		row := container.New(&PreviewRowLayout{
			CellWidth:  float32(b.thumbSize),
			CellHeight: b.thumbCellHeight,
		})
		shown := min(len(dp.items), previewThumbCount)
		for i := range shown {
			item := dp.items[i]
			index := i
			row.Add(b.thumbnail(item, func() { b.openViewer(dp.items, index) }))
		}

		b.previewBox.Add(container.NewVBox(header, row, widget.NewSeparator()))
	}

	b.previewBox.Refresh()
	b.scroll.Refresh()
	b.root.Refresh()
}

func (b *Browser) thumbnail(item images.Item, onTap func()) fyne.CanvasObject {
	img := canvas.NewImageFromFile("")
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(
		fyne.NewSize(
			float32(b.thumbSize),
			float32(b.thumbSize),
		),
	)

	clickLabel := newClickableLabel(item.Name, fyne.TextStyle{}, func() {
		fyne.CurrentApp().Clipboard().SetContent(item.Name)
	})
	clickLabel.label.Alignment = fyne.TextAlignCenter
	clickLabel.label.Truncation = fyne.TextTruncateEllipsis

	card := container.NewVBox(img, clickLabel)

	if b.cache != nil {
		go func() {
			// Bound how many thumbnails decode/resize concurrently: without
			// this, a directory with thousands of images would spawn
			// thousands of simultaneous goroutines competing for CPU and
			// memory.
			b.thumbSem <- struct{}{}
			defer func() { <-b.thumbSem }()

			cachedPath, err := b.cache.Generate(item.Path)
			if err != nil {
				log.Printf("generate thumbnail for %q: %v", item.Path, err)
				return
			}

			fyne.Do(func() {
				img.File = cachedPath
				img.Refresh()
			})
		}()
	}

	return newThumbnailWidget(card, onTap)
}

func (b *Browser) openViewer(items []images.Item, index int) {
	if index < 0 || index >= len(items) {
		return
	}

	v := newViewer(b.win, items, index)
	v.Show()
}
