package ui

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"photo-browser/internal/filedialog"
	"photo-browser/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type Browser struct {
	win              fyne.Window
	root             *fyne.Container
	grid             *fyne.Container
	scroll           *container.Scroll
	path             *widget.Entry
	status           *widget.Label
	thumbSize        int
	items            []images.Item
	cache            *images.Cache
	lastOpenLocation fyne.ListableURI

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
		cache:     cache,
	}

	b.path = widget.NewEntry()
	b.path.SetText(dir)

	b.status = widget.NewLabel("")

	choose := widget.NewButton("Choose Directory", func() {
		b.OpenFolderDialog()
	})

	refresh := widget.NewButton("Refresh", func() {
		_ = b.LoadDirectory(context.Background(), b.path.Text)
	})

	sampleLabel := widget.NewLabel("Wg")
	cellHeight := float32(b.thumbSize) + theme.Padding() + sampleLabel.MinSize().Height

	b.grid = container.New(&ThumbnailGridLayout{
		CellWidth:  float32(b.thumbSize),
		CellHeight: cellHeight,
	})
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
		_ = b.LoadDirectory(context.Background(), lu.Path())
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
	b.mu.Unlock()

	fyne.Do(func() {
		b.path.SetText(dir)
		b.status.SetText(fmt.Sprintf("%d images", len(items)))
		b.rebuild()
	})
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

var _ = image.Point{}
var _ = filepath.Separator
var _ = os.ErrNotExist
var _ = runtime.GOOS
