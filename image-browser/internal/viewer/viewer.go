package viewer

import (
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"image-browser/internal/images"
	"image-browser/internal/trash"
)

const (
	ThumbnailSize float32 = 150
	ThumbnailGap  float32 = 2
)

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
	app           fyne.App
	win           fyne.Window
	root          string
	pattern       string
	images        []string
	index         int
	startGrid     bool
	groupDirs     bool
	trashEnabled  bool
	previewDir    string
	previewImages []string

	single fyne.CanvasObject
	grid   fyne.CanvasObject
	status *widget.Label
	image  *canvas.Image

	thumbCache *imageCache
	thumbSem   chan struct{}
	thumbDisk  *images.Cache // nil if the on-disk cache could not be created; thumbnails are then decoded without it

	fullCache      *imageCache
	fullSem        chan struct{}
	fullMu         sync.Mutex
	fullLoading    map[string]bool
	loadGeneration uint64

	reviewContinue func()
	reviewQuit     func()
	reviewTrash    func() error
	reviewMoveAll  func()

	browseRoot           string
	browseChoose         func()
	browseRefresh        func(string)
	browsePreviewChecked bool
	browseTogglePreview  func(bool)
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
		thumbDisk:    newThumbDiskCache(),
		fullCache:    newImageCache(fullImageCacheSize),
		fullSem:      make(chan struct{}, fullImageWorkers),
		fullLoading:  make(map[string]bool),
	}
	b.single = b.buildSingleView()
	b.installShortcuts()
	return b
}

// newThumbDiskCache creates the on-disk thumbnail cache used by
// loadThumbnailAsync, returning nil if it could not be created. Thumbnails
// are a convenience, not core functionality, so a cache failure (e.g. an
// unwritable cache dir) degrades to decoding thumbnails without it rather
// than making the viewer unusable.
//
// This is a package-level function, rather than inline in New(), because
// New()'s "images" parameter (the batch of image paths to display) shadows
// the image-browser/internal/images package import within New()'s body.
func newThumbDiskCache() *images.Cache {
	cache, err := images.NewCache(uint(ThumbnailSize))
	if err != nil {
		log.Printf("thumbnail disk cache unavailable, thumbnails will be decoded without it: %v", err)
		return nil
	}
	return cache
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

// SetBrowseControls enables the path entry / Choose Directory / Refresh /
// Preview subdirectories toolbar in place of the review toolbar. It is
// intended for `view` invocations with no search pattern, where there is no
// batch of matches to review — just a directory the user can navigate.
//
// root is shown (and editable) in the path entry; refresh is called with
// whatever the entry currently contains when the user presses Refresh or
// submits the entry, which is root itself unless they typed something else
// — so Refresh means "reload the shown path" whether or not it was edited.
//
// previewChecked reflects whether the current listing is the recursive,
// grouped-by-subdirectory preview (see openDirectoryPreview) or a flat
// single-directory listing; togglePreview is called with the checkbox's new
// state when the user flips it, and is responsible for reloading with the
// other kind of listing.
func (b *Browser) SetBrowseControls(root string, chooseDirectory func(), refresh func(string), previewChecked bool, togglePreview func(bool)) {
	b.browseRoot = root
	b.browseChoose = chooseDirectory
	b.browseRefresh = refresh
	b.browsePreviewChecked = previewChecked
	b.browseTogglePreview = togglePreview
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

// bottomToolbar returns the toolbar appropriate to how the Browser was
// configured: the browse toolbar (Choose Directory / Refresh) when
// SetBrowseControls was used, otherwise the review toolbar (Continue / Trash
// Batch / Move All / Quit). The two are mutually exclusive — a Browser is
// either browsing a directory or reviewing a batch of search matches, never
// both — so at most one of them returns non-nil.
func (b *Browser) bottomToolbar() fyne.CanvasObject {
	if b.browseChoose != nil || b.browseRefresh != nil {
		return b.browseToolbar()
	}
	return b.reviewToolbar()
}

// browseToolbar builds an editable path entry (pre-filled with browseRoot)
// alongside Choose Directory / Refresh / Preview subdirectories. Refresh —
// and submitting the entry directly, e.g. by pressing Return — both reload
// whatever path the entry currently holds, so leaving it untouched just
// reloads browseRoot, while typing a different path and hitting either one
// navigates there without going through the folder-picker dialog.
func (b *Browser) browseToolbar() fyne.CanvasObject {
	buttons := make([]fyne.CanvasObject, 0, 3)
	if b.browseChoose != nil {
		buttons = append(buttons, widget.NewButton("Choose Directory", b.browseChoose))
	}

	var pathEntry *widget.Entry
	if b.browseRefresh != nil {
		pathEntry = widget.NewEntry()
		pathEntry.SetText(b.browseRoot)

		refresh := b.browseRefresh
		doRefresh := func() { refresh(pathEntry.Text) }
		pathEntry.OnSubmitted = func(string) { doRefresh() }
		buttons = append(buttons, widget.NewButton("Refresh", doRefresh))
	}

	if b.browseTogglePreview != nil {
		preview := widget.NewCheck("Preview subdirectories", b.browseTogglePreview)
		// Set the field directly rather than via SetChecked: SetChecked
		// invokes OnChanged whenever the new value differs from the
		// widget's current (zero-value, unchecked) state, which would fire
		// togglePreview(true) as a side effect of merely rendering an
		// already-checked toolbar.
		preview.Checked = b.browsePreviewChecked
		buttons = append(buttons, preview)
	}

	buttonBar := container.NewHBox(buttons...)
	if pathEntry == nil {
		return container.NewCenter(buttonBar)
	}
	return container.NewBorder(nil, nil, nil, buttonBar, pathEntry)
}

type moveAllResult struct {
	moved      int
	duplicates int
	failures   []string
	movedPaths []string
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

				// Drop the images that did move so a retry of Move All only
				// reattempts the ones that actually failed, rather than
				// re-hitting already-moved sources and reporting bogus
				// "file not found" failures for them.
				b.removeImages(result.movedPaths)

				var sb strings.Builder
				fmt.Fprintf(&sb, "Moved %d images; skipped %d duplicates; %d failed.", result.moved, result.duplicates, len(result.failures))
				for _, failure := range result.failures {
					sb.WriteByte('\n')
					sb.WriteString(failure)
				}
				b.status.SetText(sb.String())
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
		result.movedPaths = append(result.movedPaths, src)
	}

	return result
}

func (b *Browser) Show() {
	if len(b.images) == 0 {
		if b.pattern == "" {
			b.status.SetText(fmt.Sprintf("No images found in %s", b.root))
		} else {
			b.status.SetText(fmt.Sprintf("No matches for '%s' under %s", b.pattern, b.root))
		}
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
	if controls := b.bottomToolbar(); controls != nil {
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
	if b.previewDir != "" {
		return b.buildDirectoryView()
	}
	if b.groupDirs {
		return b.buildGroupedGridView()
	}

	paths := b.gridPaths()
	grid := b.newThumbnailGrid(paths)

	header := widget.NewLabel(fmt.Sprintf("%d images", len(paths)))
	header.Alignment = fyne.TextAlignCenter

	return container.NewBorder(
		header,
		b.bottomToolbar(),
		nil,
		nil,
		container.NewVScroll(grid),
	)
}

// buildGroupedGridView renders one section per parent directory. Each section
// has a heading, an Open Folder button, and exactly one thumbnail row. The row
// shows only the thumbnails that fit in the available width; it never creates
// a nested horizontal scroll area.
func (b *Browser) buildGroupedGridView() fyne.CanvasObject {
	groups := b.groupedPaths()
	content := container.NewVBox()

	for _, group := range groups {
		dir := group.dir
		pathLabel := newClickableLabel(dir, fyne.TextStyle{Bold: true}, func() {
			b.copyText(dir)
		})
		count := widget.NewLabelWithStyle(
			fmt.Sprintf("  (%d images)", len(group.paths)),
			fyne.TextAlignLeading,
			fyne.TextStyle{Bold: true},
		)
		heading := container.NewHBox(pathLabel, count)

		open := widget.NewButton("Open Folder →", func(group imageGroup) func() {
			return func() { b.openDirectoryPreview(group.dir, group.paths) }
		}(group))
		header := container.NewBorder(nil, nil, nil, open, heading)

		row := container.New(&PreviewRowLayout{
			CellWidth:  ThumbnailSize,
			CellHeight: thumbnailCellHeight(),
			Gap:        ThumbnailGap,
		})

		for _, path := range group.paths {
			path := path
			t := newThumbnailTemplate(b.copyText)
			t.setPath(path, false)
			t.onTap = func() { b.openImage(path) }
			row.Add(t)
			b.loadThumbnailAsync(t, path)
		}

		content.Add(container.NewVBox(
			header,
			row,
			widget.NewSeparator(),
		))
	}

	return container.NewBorder(
		nil,
		b.bottomToolbar(),
		nil,
		nil,
		container.NewVScroll(content),
	)
}

type imageGroup struct {
	dir   string
	paths []string
}

func (b *Browser) groupedPaths() []imageGroup {
	paths := append([]string(nil), b.images...)
	sort.Slice(paths, func(i, j int) bool {
		di, dj := filepath.Dir(paths[i]), filepath.Dir(paths[j])
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})

	groups := make([]imageGroup, 0)
	for _, path := range paths {
		dir := filepath.Dir(path)
		if len(groups) == 0 || groups[len(groups)-1].dir != dir {
			groups = append(groups, imageGroup{dir: dir})
		}
		groups[len(groups)-1].paths = append(groups[len(groups)-1].paths, path)
	}
	return groups
}

// openDirectoryPreview drills into one directory using the images already
// discovered for that directory. No additional scan is needed.
func (b *Browser) openDirectoryPreview(dir string, paths []string) {
	b.previewDir = dir
	b.previewImages = append([]string(nil), paths...)
	b.showGrid()
}

// buildDirectoryView shows every image in the selected directory in the same
// responsive thumbnail grid used by the flat view. The only navigation control
// added is Back to Preview.
func (b *Browser) buildDirectoryView() fyne.CanvasObject {
	paths := append([]string(nil), b.previewImages...)
	grid := b.newThumbnailGrid(paths)

	back := widget.NewButton("← Back to Preview", func() {
		b.previewDir = ""
		b.previewImages = nil
		b.showGrid()
	})

	header := widget.NewLabel(fmt.Sprintf("%s   (%d images)", b.previewDir, len(paths)))
	header.Alignment = fyne.TextAlignCenter

	toolbar := container.NewBorder(nil, nil, back, nil, header)
	return container.NewBorder(
		toolbar,
		b.bottomToolbar(),
		nil,
		nil,
		container.NewVScroll(grid),
	)
}

func (b *Browser) gridPaths() []string {
	paths := append([]string(nil), b.images...)
	if !b.groupDirs {
		return paths
	}

	sort.Slice(paths, func(i, j int) bool {
		di, dj := filepath.Dir(paths[i]), filepath.Dir(paths[j])
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
	return paths
}

func (b *Browser) newThumbnailGrid(paths []string) *fyne.Container {
	objects := make([]fyne.CanvasObject, 0, len(paths))
	for _, path := range paths {
		path := path
		t := newThumbnailTemplate(b.copyText)
		t.setPath(path, false)
		t.onTap = func() { b.openImage(path) }
		objects = append(objects, t)
		b.loadThumbnailAsync(t, path)
	}

	return container.New(&ThumbnailGridLayout{
		CellWidth:  ThumbnailSize,
		CellHeight: thumbnailCellHeight(),
	}, objects...)
}

func thumbnailCellHeight() float32 {
	label := widget.NewLabel("Wg")
	return ThumbnailSize + label.MinSize().Height
}

func (b *Browser) loadThumbnailAsync(t *thumbnail, path string) {
	if img, ok := b.thumbCache.Get(path); ok {
		t.setImage(img)
		return
	}

	go func() {
		b.thumbSem <- struct{}{}
		img := loadThumbnail(b.thumbDisk, path, int(ThumbnailSize))
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

// clickableLabel is a label that copies its text to the clipboard when
// tapped, so a directory heading or thumbnail filename can be grabbed
// without needing to select and copy text manually.
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

type thumbnail struct {
	widget.BaseWidget
	path  string
	image *canvas.Image
	label *clickableLabel
	onTap func()
}

// newThumbnailTemplate creates a thumbnail cell. copyText, when non-nil, is
// invoked with the cell's current filename when its label (rather than the
// image or surrounding padding) is tapped directly — Fyne dispatches a tap
// to whichever tappable widget is deepest under the pointer, so tapping the
// label does not also fire the thumbnail's own onTap (which opens the
// viewer).
func newThumbnailTemplate(copyText func(string)) *thumbnail {
	t := &thumbnail{
		image: canvas.NewImageFromImage(image.NewRGBA(image.Rect(0, 0, 1, 1))),
	}
	t.label = newClickableLabel("Loading…", fyne.TextStyle{}, func() {
		if copyText != nil {
			copyText(filepath.Base(t.path))
		}
	})
	t.label.label.Alignment = fyne.TextAlignCenter
	t.label.label.Wrapping = fyne.TextWrapOff
	t.label.label.Truncation = fyne.TextTruncateEllipsis
	t.image.FillMode = canvas.ImageFillContain
	t.image.SetMinSize(fyne.NewSize(ThumbnailSize, ThumbnailSize))
	t.ExtendBaseWidget(t)
	return t
}

func (t *thumbnail) Tapped(*fyne.PointEvent) {
	if t.onTap != nil {
		t.onTap()
	}
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
	t.label.label.SetText(label)
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

// loadThumbnail returns a decoded thumbnail for path, sized to at most
// maxSize on its longest edge. When cache is non-nil, it is consulted
// first: a fresh on-disk thumbnail is already the right size, so decoding
// it directly skips re-resizing the full-size original. A cache miss or
// error falls back to decoding and resizing the original in memory.
func loadThumbnail(cache *images.Cache, path string, maxSize int) image.Image {
	if cache != nil {
		if cachedPath, err := cache.Generate(path); err == nil {
			if img := loadImage(cachedPath); img != nil {
				return img
			}
		}
	}

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

// removeImages drops paths from b.images and their cached decodes, clamping
// b.index so it still points at a valid entry (or stays at 0 if empty).
func (b *Browser) removeImages(paths []string) {
	if len(paths) == 0 {
		return
	}

	remove := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		remove[path] = struct{}{}
		b.thumbCache.Delete(path)
		b.fullCache.Delete(path)
	}

	kept := b.images[:0]
	for _, path := range b.images {
		if _, ok := remove[path]; ok {
			continue
		}
		kept = append(kept, path)
	}
	b.images = kept

	if b.index >= len(b.images) {
		b.index = max(len(b.images)-1, 0)
	}
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
