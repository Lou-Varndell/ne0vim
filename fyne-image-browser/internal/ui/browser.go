package ui

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"fyne-image-browser/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type Browser struct {
	win       fyne.Window
	root      *fyne.Container
	grid      *fyne.Container
	scroll    *container.Scroll
	path      *widget.Entry
	status    *widget.Label
	thumbSize int
	columns   int
	items     []images.Item
	cache     *images.Cache

	mu sync.Mutex
}

type thumbnailWidget struct {
	widget.BaseWidget
	content fyne.CanvasObject
	onTap   func()
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
		columns:   7,
		cache:     cache,
	}

	b.path = widget.NewEntry()
	b.path.SetText(dir)

	b.status = widget.NewLabel("")

	choose := widget.NewButton("Choose Directory", func() {
		d := dialog.NewFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil || uri == nil {
				return
			}

			_ = b.LoadDirectory(uri.Path())
		}, b.win)
		d.Show()
	})

	refresh := widget.NewButton("Refresh", func() {
		_ = b.LoadDirectory(b.path.Text)
	})

	b.grid = container.NewGridWithColumns(b.columns)
	b.scroll = container.NewScroll(b.grid)

	toolbar := container.NewBorder(
		nil, nil,
		nil,
		container.NewHBox(choose, refresh),
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

func (b *Browser) LoadDirectory(dir string) error {
	items, err := images.Scan(dir)
	if err != nil {
		b.status.SetText(err.Error())
		return err
	}

	b.mu.Lock()
	b.items = items
	b.mu.Unlock()

	b.path.SetText(dir)
	b.status.SetText(fmt.Sprintf("%d images", len(items)))
	b.rebuild()
	return nil
}

func (b *Browser) rebuild() {
	b.grid.Objects = nil

	b.mu.Lock()
	items := append([]images.Item(nil), b.items...)
	b.mu.Unlock()

	for i, item := range items {
		b.grid.Add(b.thumbnail(item, i))
	}

	b.grid.Refresh()
	b.root.Refresh()
}

func (b *Browser) thumbnail(item images.Item, index int) fyne.CanvasObject {
	img := canvas.NewImageFromFile("")
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(
		fyne.NewSize(
			float32(b.thumbSize),
			float32(b.thumbSize),
		),
	)

	label := widget.NewLabel(item.Name)
	label.Alignment = fyne.TextAlignCenter
	label.Truncation = fyne.TextTruncateEllipsis

	card := container.NewVBox(img, label)

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

	return newThumbnailWidget(card, func() {
		b.openViewer(index)
	})
}

func (b *Browser) openViewer(index int) {
	b.mu.Lock()
	if index < 0 || index >= len(b.items) {
		b.mu.Unlock()
		return
	}
	items := append([]images.Item(nil), b.items...)
	b.mu.Unlock()

	v := newViewer(b.win, items, index)
	v.Show()
}

// Call this from resize handling if desired. It keeps the grid responsive.
func (b *Browser) SetGridColumns(columns int) {
	if columns < 1 {
		columns = 1
	}
	if columns > 12 {
		columns = 12
	}
	b.columns = columns
	b.rebuild()
}

func ColumnsForWidth(width float32, thumb float32) int {
	n := int(width / thumb)
	if n < 1 {
		n = 1
	}
	return n
}

var _ = image.Point{}
var _ = filepath.Separator
var _ = os.ErrNotExist
var _ = runtime.GOOS
var _ = layout.NewSpacer
