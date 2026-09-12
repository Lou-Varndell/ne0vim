package ui

import (
	"context"
	"fmt"
	"image"
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
	cache            *images.Cache
	lastOpenLocation fyne.ListableURI

	mu sync.Mutex
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

func NewBrowser(dir string) *Browser {
	cache, _ := images.NewCache(150)

	b := &Browser{
		thumbSize: 150,
		cache:     cache,
	}

	b.path = widget.NewEntry()
	b.path.SetText(dir)

	b.status = widget.NewLabel("")

	choose := widget.NewButton("Choose Directory", func() {
		b.OpenFolderDialog()
	})

	refresh := widget.NewButton("Refresh", func() {
		if b.currentlyPreviewing() {
			_ = b.LoadPreview(context.Background(), b.path.Text)
		} else {
			_ = b.LoadDirectory(context.Background(), b.path.Text)
		}
	})

	b.previewToggle = widget.NewCheck("Preview subdirectories", func(checked bool) {
		b.clearBack()
		if checked {
			_ = b.LoadPreview(context.Background(), b.path.Text)
		} else {
			_ = b.LoadDirectory(context.Background(), b.path.Text)
		}
	})

	b.backBtn = widget.NewButton("← Back to Preview", func() {
		root := b.previewRoot
		b.clearBack()
		_ = b.LoadPreview(context.Background(), root)
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

func (b *Browser) SetWindow(w fyne.Window) {
	b.win = w
}

func (b *Browser) Canvas() fyne.CanvasObject {
	return b.root
}

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
			_ = b.LoadPreview(context.Background(), lu.Path())
		} else {
			_ = b.LoadDirectory(context.Background(), lu.Path())
		}
	}, b.win)

	if b.lastOpenLocation != nil {
		d.SetLocation(b.lastOpenLocation)
	}

	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// LoadDirectory scans dir for images and updates the browser's UI with the
// result. It performs the (potentially slow) directory scan on the calling
// goroutine, but marshals every widget update through fyne.Do, so it is
// safe to call from either a UI callback or a goroutine the caller started.
func (b *Browser) LoadDirectory(ctx context.Context, dir string) error {
	items, err := images.Scan(ctx, dir)
	if err != nil {
		fyne.Do(func() {
			b.status.SetText(err.Error())
		})
		return err
	}

	b.mu.Lock()
	b.items = items
	b.previewMode = false
	b.mu.Unlock()

	fyne.Do(func() {
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
		fyne.Do(func() {
			b.status.SetText(walkErr.Error())
		})
		return walkErr
	}

	sort.Slice(previews, func(i, j int) bool {
		return strings.ToLower(previews[i].path) < strings.ToLower(previews[j].path)
	})

	b.mu.Lock()
	b.previews = previews
	b.previewMode = true
	b.mu.Unlock()

	fyne.Do(func() {
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
	_ = b.LoadDirectory(context.Background(), dir)
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

	go func() {
		cachedPath, err := b.cache.Generate(item.Path)
		if err != nil {
			return
		}

		fyne.Do(func() {
			img.File = cachedPath
			img.Refresh()
		})
	}()

	return newThumbnailWidget(card, onTap)
}

func (b *Browser) openViewer(items []images.Item, index int) {
	if index < 0 || index >= len(items) {
		return
	}

	v := newViewer(b.win, items, index)
	v.Show()
}

var _ = image.Point{}
var _ = runtime.GOOS
