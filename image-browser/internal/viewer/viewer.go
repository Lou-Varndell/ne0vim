package viewer

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"sync"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"image-browser/internal/images"
	"image-browser/internal/trash"
)

const ThumbnailSize float32 = 400

const (
	thumbnailWorkers   = 4
	thumbnailCacheSize = 256
	fullImageWorkers   = 2
	fullImageCacheSize = 3
)

// Browser is the Fyne equivalent of the original PySide ImageBrowser.
// The single-image view displays images at their original pixel dimensions
// (1:1), inside a scroll container.
type Browser struct {
	app          fyne.App
	win          fyne.Window
	root         string
	pattern      string
	images       []string
	index        int
	startGrid    bool
	groupDirs    bool
	trashEnabled bool

	single fyne.CanvasObject
	grid   fyne.CanvasObject
	status *widget.Label
	image  *canvas.Image

	thumbCache *imageCache
	thumbSem   chan struct{}

	fullCache      *imageCache
	fullSem        chan struct{}
	fullMu         sync.Mutex
	fullLoading    map[string]bool
	loadGeneration uint64

	reviewContinue func()
	reviewQuit     func()
	reviewTrash    func() error
	reviewMoveAll  func()
}

func New(app fyne.App, win fyne.Window, root, pattern string, images []string, startGrid, groupDirs bool) *Browser {
	b := &Browser{
		app:          app,
		win:          win,
		root:         root,
		pattern:      pattern,
		images:       append([]string(nil), images...),
		startGrid:    startGrid,
		groupDirs:    groupDirs,
		trashEnabled: true,
		thumbCache:   newImageCache(thumbnailCacheSize),
		thumbSem:     make(chan struct{}, thumbnailWorkers),
		fullCache:    newImageCache(fullImageCacheSize),
		fullSem:      make(chan struct{}, fullImageWorkers),
		fullLoading:  make(map[string]bool),
	}
	b.single = b.buildSingleView()
	b.installShortcuts()
	return b
}

// SetTrashEnabled controls whether the browser's trash shortcuts are active.
func (b *Browser) SetTrashEnabled(enabled bool) {
	b.trashEnabled = enabled
}

// SetReviewControls adds controls for commands that review a sequence of
// image batches. Continue and Quit are handled by the caller; Trash is
// optional and is intended for a whole reviewed batch.
func (b *Browser) SetReviewControls(continueFunc, quitFunc func(), trashFunc func() error) {
	b.reviewContinue = continueFunc
	b.reviewQuit = quitFunc
	b.reviewTrash = trashFunc
	b.single = b.buildSingleView()
}

// SetMoveAll enables the Move All review control for destination. The control
// is intentionally opt-in so normal view sessions are unchanged.
func (b *Browser) SetMoveAll(destination string, done func()) {
	b.reviewMoveAll = func() {
		b.confirmMoveAll(destination, done)
	}
	b.single = b.buildSingleView()
}

func (b *Browser) finishReview(quit bool) {
	if quit {
		if b.reviewQuit != nil {
			b.reviewQuit()
		}
		return
	}
	if b.reviewContinue != nil {
		b.reviewContinue()
	}
}

func (b *Browser) reviewToolbar() fyne.CanvasObject {
	if b.reviewContinue == nil && b.reviewQuit == nil && b.reviewTrash == nil && b.reviewMoveAll == nil {
		return nil
	}

	buttons := make([]fyne.CanvasObject, 0, 4)
	if b.reviewContinue != nil {
		buttons = append(buttons, widget.NewButton("Continue", func() {
			b.finishReview(false)
		}))
	}
	if b.reviewTrash != nil {
		buttons = append(buttons, widget.NewButton("Trash Batch", func() {
			if err := b.reviewTrash(); err != nil {
				b.status.SetText("Failed to trash batch:\n" + err.Error())
				return
			}
			b.finishReview(false)
		}))
	}

	if b.reviewMoveAll != nil {
		buttons = append(buttons, widget.NewButton("Move All", func() {
			b.reviewMoveAll()
		}))
	}
	if b.reviewQuit != nil {
		buttons = append(buttons, widget.NewButton("Quit", func() {
			b.finishReview(true)
		}))
	}

	return container.NewCenter(container.NewHBox(buttons...))
}

type moveAllResult struct {
	moved      int
	duplicates int
	failures   []string
}

func (b *Browser) confirmMoveAll(destination string, done func()) {
	message := fmt.Sprintf("Move all %d matching images to:\n%s", len(b.images), destination)
	d := dialog.NewConfirm("Move All", message, func(confirm bool) {
		if !confirm {
			return
		}
		paths := append([]string(nil), b.images...)
		b.status.SetText(fmt.Sprintf("Moving %d images…", len(paths)))
		go func() {
			result := moveAllImages(paths, destination)
			fyne.Do(func() {
				if len(result.failures) == 0 {
					if result.duplicates == 0 {
						b.status.SetText(fmt.Sprintf("Moved %d images.", result.moved))
					} else {
						b.status.SetText(fmt.Sprintf("Moved %d images; skipped %d duplicates.", result.moved, result.duplicates))
					}
					if done != nil {
						done()
					}
					return
				}

				message := fmt.Sprintf("Moved %d images; skipped %d duplicates; %d failed.", result.moved, result.duplicates, len(result.failures))
				for _, failure := range result.failures {
					message += "\n" + failure
				}
				b.status.SetText(message)
			})
		}()
	}, b.win)
	d.SetConfirmText("Move All")
	d.SetDismissText("Cancel")
	d.Show()
}

func moveAllImages(paths []string, destination string) moveAllResult {
	result := moveAllResult{}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		result.failures = append(result.failures, fmt.Sprintf("destination: %v", err))
		return result
	}

	for _, src := range paths {
		dest, duplicate, err := images.ResolveDestination(src, destination)
		if err != nil {
			result.failures = append(result.failures, fmt.Sprintf("%s: %v", src, err))
			continue
		}
		if duplicate {
			result.duplicates++
			continue
		}
		if err := images.Move(src, dest); err != nil {
			result.failures = append(result.failures, fmt.Sprintf("%s: %v", src, err))
			continue
		}
		result.moved++
	}

	return result
}

func (b *Browser) Show() {
	if len(b.images) == 0 {
		b.status.SetText(fmt.Sprintf("No matches for '%s' under %s", b.pattern, b.root))
		b.win.SetContent(b.single)
		return
	}
	if b.startGrid {
		b.showGrid()
	} else {
		b.showImage()
	}
}

func (b *Browser) buildSingleView() fyne.CanvasObject {
	b.image = canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	b.image.FillMode = canvas.ImageFillOriginal
	b.image.SetMinSize(fyne.NewSize(1, 1))

	imageScroll := container.NewScroll(container.NewCenter(b.image))

	b.status = widget.NewLabel("")
	b.status.Alignment = fyne.TextAlignCenter
	b.status.Wrapping = fyne.TextWrapWord

	gridButton := widget.NewButton("← Grid", func() {
		b.showGrid()
	})
	copyButton := widget.NewButton("Copy Path", func() {
		b.copyCurrentPath()
	})

	footer := container.NewVBox(b.status)
	if controls := b.reviewToolbar(); controls != nil {
		footer.Add(controls)
	}

	return container.NewBorder(
		container.NewHBox(gridButton, copyButton),
		footer,
		nil,
		nil,
		imageScroll,
	)
}

func (b *Browser) buildGridView() fyne.CanvasObject {
	// Keep the grid as a single virtualized GridWrap.  Creating one GridWrap
	// per directory and stacking them caused Fyne's layout to reserve space
	// for multiple virtualized grids, which could appear as an empty second pane.
	paths := b.gridPaths()
	grid := b.newThumbnailGrid(paths)

	var title string
	if b.groupDirs {
		dirs := make(map[string]struct{})
		for _, p := range paths {
			dirs[filepath.Dir(p)] = struct{}{}
		}
		title = fmt.Sprintf("%d images — %d directories", len(paths), len(dirs))
	} else {
		title = fmt.Sprintf("%d images", len(paths))
	}

	header := widget.NewLabel(title)
	header.Alignment = fyne.TextAlignCenter
	return container.NewBorder(header, b.reviewToolbar(), nil, nil, grid)
}

func (b *Browser) gridPaths() []string {
	paths := append([]string(nil), b.images...)
	if !b.groupDirs {
		return paths
	}

	// Preserve directory grouping order from the Python implementation while
	// keeping one flat list for the virtualized grid.
	sort.Slice(paths, func(i, j int) bool {
		di, dj := filepath.Dir(paths[i]), filepath.Dir(paths[j])
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	return paths
}

func (b *Browser) newThumbnailGrid(paths []string) *widget.GridWrap {
	grid := widget.NewGridWrap(
		func() int {
			return len(paths)
		},
		func() fyne.CanvasObject {
			return newThumbnailTemplate()
		},
		func(id widget.GridWrapItemID, item fyne.CanvasObject) {
			if id < 0 || id >= len(paths) {
				return
			}
			t, ok := item.(*thumbnail)
			if !ok {
				// newThumbnailTemplate is the only factory passed to
				// NewGridWrap above, so it always produces *thumbnail.
				panic(fmt.Sprintf("grid cell has unexpected type %T", item))
			}
			t.setPath(paths[id], b.groupDirs)
			b.loadThumbnailAsync(t, paths[id])
		},
	)

	grid.OnSelected = func(id widget.GridWrapItemID) {
		if id >= 0 && id < len(paths) {
			b.openImage(paths[id])
		}
	}

	return grid
}

func (b *Browser) loadThumbnailAsync(t *thumbnail, path string) {
	if img, ok := b.thumbCache.Get(path); ok {
		t.setImage(img)
		return
	}

	go func() {
		b.thumbSem <- struct{}{}
		img := loadThumbnail(path, int(ThumbnailSize))
		<-b.thumbSem

		if img == nil {
			return
		}
		b.thumbCache.Put(path, img)

		// All Fyne object/widget mutations from this worker goroutine are
		// explicitly marshalled back to Fyne's main goroutine.
		fyne.Do(func() {
			if t.path != path {
				return // the pooled cell has already been reused for another image
			}
			t.setImage(img)
		})
	}()
}

type thumbnail struct {
	widget.BaseWidget
	path  string
	image *canvas.Image
	label *widget.Label
}

func newThumbnailTemplate() *thumbnail {
	t := &thumbnail{
		image: canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 1, 1))),
		label: widget.NewLabel("Loading…"),
	}
	t.image.FillMode = canvas.ImageFillContain
	t.image.SetMinSize(fyne.NewSize(ThumbnailSize, ThumbnailSize))
	t.label.Alignment = fyne.TextAlignCenter
	t.label.Wrapping = fyne.TextWrapWord
	t.ExtendBaseWidget(t)
	return t
}

func (t *thumbnail) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewVBox(t.image, t.label))
}

func (t *thumbnail) setPath(path string, showDir bool) {
	t.path = path
	label := filepath.Base(path)
	if showDir {
		label += "\n" + filepath.Dir(path)
	}
	t.label.SetText(label)
	t.image.Image = image.NewRGBA(image.Rect(0, 0, 1, 1))
	t.image.Refresh()
	t.Refresh()
}

func (t *thumbnail) setImage(img image.Image) {
	t.image.Image = img
	t.image.Refresh()
}

func (b *Browser) openImage(path string) {
	for i, p := range b.images {
		if p == path {
			b.index = i
			break
		}
	}
	b.showImage()
}

func (b *Browser) showGrid() {
	if len(b.images) == 0 {
		return
	}
	b.grid = b.buildGridView()
	b.win.SetContent(b.grid)
}

func (b *Browser) showImage() {
	if len(b.images) == 0 {
		b.image.Image = nil
		b.image.Refresh()
		b.status.SetText("No images remaining.")
		b.win.SetContent(b.single)
		return
	}

	path := b.images[b.index]
	b.win.SetContent(b.single)

	// Never decode a full-resolution image on the Fyne/UI goroutine. A large
	// JPEG/PNG can take seconds to decode and will otherwise make the entire
	// window appear frozen.
	if img, ok := b.fullCache.Get(path); ok {
		b.displayFullImage(path, img)
		b.preloadNeighbors()
		return
	}

	b.image.Image = image.NewRGBA(image.Rect(0, 0, 1, 1))
	b.image.Refresh()
	b.status.SetText(fmt.Sprintf("%d/%d   Loading…\n%s", b.index+1, len(b.images), path))

	generation := b.nextLoadGeneration()
	b.loadFullImageAsync(path, generation, true)
	b.preloadNeighbors()
}

func (b *Browser) displayFullImage(path string, img image.Image) {
	if len(b.images) == 0 || b.images[b.index] != path {
		return
	}

	b.image.Image = img
	b.image.FillMode = canvas.ImageFillOriginal
	b.image.Refresh()

	width, height := img.Bounds().Dx(), img.Bounds().Dy()
	b.status.SetText(fmt.Sprintf("%d/%d   %dx%d   1:1\n%s", b.index+1, len(b.images), width, height, path))
}

func (b *Browser) nextLoadGeneration() uint64 {
	b.fullMu.Lock()
	defer b.fullMu.Unlock()
	b.loadGeneration++
	return b.loadGeneration
}

func (b *Browser) currentLoadGeneration() uint64 {
	b.fullMu.Lock()
	defer b.fullMu.Unlock()
	return b.loadGeneration
}

func (b *Browser) loadFullImageAsync(path string, generation uint64, display bool) {
	b.fullMu.Lock()
	if b.fullLoading[path] {
		b.fullMu.Unlock()
		return
	}
	b.fullLoading[path] = true
	b.fullMu.Unlock()

	go func() {
		b.fullSem <- struct{}{}
		img := loadImage(path)
		<-b.fullSem

		b.fullMu.Lock()
		delete(b.fullLoading, path)
		b.fullMu.Unlock()

		if img == nil {
			if !display {
				return
			}
			fyne.Do(func() {
				if generation != b.currentLoadGeneration() || len(b.images) == 0 || b.images[b.index] != path {
					return
				}
				b.image.Image = nil
				b.image.Refresh()
				b.status.SetText(fmt.Sprintf("%d/%d   unable to load\n%s", b.index+1, len(b.images), path))
			})
			return
		}

		b.fullCache.Put(path, img)

		if !display {
			return
		}

		fyne.Do(func() {
			if generation != b.currentLoadGeneration() || len(b.images) == 0 || b.images[b.index] != path {
				return
			}
			b.displayFullImage(path, img)
		})
	}()
}

func (b *Browser) preloadNeighbors() {
	if len(b.images) == 0 {
		return
	}

	generation := b.currentLoadGeneration()
	if b.index+1 < len(b.images) {
		b.loadFullImageAsync(b.images[b.index+1], generation, false)
	}
	if b.index > 0 {
		b.loadFullImageAsync(b.images[b.index-1], generation, false)
	}
}

func loadThumbnail(path string, maxSize int) image.Image {
	img := loadImage(path)
	if img == nil {
		return nil
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= maxSize && h <= maxSize {
		return img
	}

	width, height := w, h
	if width >= height {
		height = max(1, h*maxSize/w)
		width = maxSize
	} else {
		width = max(1, w*maxSize/h)
		height = maxSize
	}

	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)
	return dst
}

func loadImage(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return img
}

func (b *Browser) toggleGrid() {
	if b.win.Content() == b.grid {
		b.showImage()
		return
	}
	b.showGrid()
}

// inImageView reports whether the single-image (1:1) view is currently on
// screen. Navigation and trash shortcuts are only meaningful in that view —
// b.index has no relationship to anything visible while the grid is shown.
func (b *Browser) inImageView() bool {
	return b.win.Content() == b.single
}

func (b *Browser) nextImage() {
	if !b.inImageView() || b.index >= len(b.images)-1 {
		return
	}
	b.index++
	b.showImage()
}

func (b *Browser) previousImage() {
	if !b.inImageView() || b.index <= 0 {
		return
	}
	b.index--
	b.showImage()
}

func (b *Browser) trashCurrentImage() {
	if !b.trashEnabled || !b.inImageView() || len(b.images) == 0 {
		return
	}
	path := b.images[b.index]
	if err := trash.Move(path); err != nil {
		b.status.SetText("Failed to trash:\n" + err.Error())
		return
	}

	b.thumbCache.Delete(path)
	b.fullCache.Delete(path)
	b.images = append(b.images[:b.index], b.images[b.index+1:]...)
	if len(b.images) == 0 {
		b.showImage()
		return
	}
	if b.index >= len(b.images) {
		b.index = len(b.images) - 1
	}
	b.showImage()
}

func (b *Browser) copyText(text string) {
	b.app.Clipboard().SetContent(text)
}

func (b *Browser) copyCurrentPath() {
	if len(b.images) > 0 {
		b.copyText(b.images[b.index])
	}
}

func (b *Browser) handleKey(event *fyne.KeyEvent) {
	switch event.Name {
	case fyne.KeyRight:
		b.nextImage()
	case fyne.KeyLeft:
		b.previousImage()
	case fyne.KeyDelete, fyne.KeyBackspace:
		b.trashCurrentImage()
	case fyne.KeyEscape:
		if b.reviewContinue != nil {
			b.finishReview(false)
		} else {
			b.win.Close()
		}
	}
}

func (b *Browser) installShortcuts() {
	canvas := b.win.Canvas()
	canvas.SetOnTypedRune(func(r rune) {
		switch r {
		case 't', 'T':
			b.trashCurrentImage()
		case 'g', 'G':
			b.toggleGrid()
		case 'q', 'Q':
			if b.reviewQuit != nil {
				b.finishReview(true)
			} else {
				b.win.Close()
			}
		}
	})
	canvas.SetOnTypedKey(b.handleKey)
}

// imageCache is a fixed-capacity, concurrency-safe image cache. Eviction is
// FIFO by insertion order, not LRU: a Get does not refresh a key's position,
// so a frequently re-viewed image can still be evicted once max distinct
// keys have been inserted after it.
type imageCache struct {
	mu    sync.RWMutex
	items map[string]image.Image
	order []string
	max   int
}

func newImageCache(max int) *imageCache {
	return &imageCache{items: make(map[string]image.Image), max: max}
}

func (c *imageCache) Get(path string) (image.Image, bool) {
	c.mu.RLock()
	img, ok := c.items[path]
	c.mu.RUnlock()
	return img, ok
}

func (c *imageCache) Delete(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, path)
}

func (c *imageCache) Put(path string, img image.Image) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[path]; exists {
		return
	}
	c.items[path] = img
	c.order = append(c.order, path)
	if len(c.order) <= c.max {
		return
	}
	old := c.order[0]
	c.order = c.order[1:]
	delete(c.items, old)
}
