package ui

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"inventory-manager/internal/filedialog"
	"inventory-manager/internal/images"

	invlib "inv-lib"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// imgPerDir caps how many thumbnails generate concurrently within a single
// grid render, so a large result set doesn't spawn one goroutine per
// image.
const imgPerDir = 10

// pageSize caps how many records a single Search loads and renders, so
// opening the browser (or running a filter) against a database of
// thousands of images never has to build thousands of Fyne widgets or
// queue thousands of thumbnail-generation goroutines at once. Browsing a
// result set larger than one page is done via the Prev/Next page
// controls rather than by raising this.
const pageSize = 200

// Browser is the main window's content: a metadata filter bar plus a
// scrollable thumbnail grid of inventory records matching the current
// filter. It never scans the filesystem itself — OpenImportDialog is the
// only path that reads a directory, and only to index it into the store.
type Browser struct {
	win    fyne.Window
	root   *fyne.Container
	grid   *fyne.Container
	scroll *container.Scroll
	status *widget.Label

	tagFilter  *widget.Entry
	siteFilter *widget.Entry
	pathFilter *widget.Entry

	pageLabel   *widget.Label
	prevPageBtn *widget.Button
	nextPageBtn *widget.Button

	thumbSize       int
	thumbCellHeight float32 // thumbnail + label height
	records         []imageRecord
	cache           *images.Cache // nil if the on-disk cache could not be created; thumbnails are then skipped
	store           *invlib.DB
	thumbSem        chan struct{} // bounds concurrent thumbnail decode/resize and indexing goroutines
	lastImportLoc   fyne.ListableURI

	mu             sync.Mutex
	loadGeneration int                // bumped on every Search call; lets a superseded search discard its results
	loadCancel     context.CancelFunc // cancels whichever search is currently in flight
	filter         Filter             // filter of the most recently completed Search, reused by page navigation
	page           int                // 0-based page of the most recently completed Search
	total          int                // total records filter matches, from the most recently completed Search
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

// NewBrowser builds a Browser showing no results until Search is called on
// it (main.go kicks off an unfiltered Search on startup).
func NewBrowser() *Browser {
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

	b.tagFilter = widget.NewEntry()
	b.tagFilter.SetPlaceHolder("Tag contains…")
	b.siteFilter = widget.NewEntry()
	b.siteFilter.SetPlaceHolder("Site contains…")
	b.pathFilter = widget.NewEntry()
	b.pathFilter.SetPlaceHolder("Path contains…")
	for _, e := range []*widget.Entry{b.tagFilter, b.siteFilter, b.pathFilter} {
		e.OnSubmitted = func(string) { b.searchAsync() }
	}

	b.status = widget.NewLabel("")

	search := widget.NewButton("Search", func() { b.searchAsync() })
	search.Importance = widget.HighImportance

	clear := widget.NewButton("Clear", func() {
		b.tagFilter.SetText("")
		b.siteFilter.SetText("")
		b.pathFilter.SetText("")
		b.searchAsync()
	})

	importBtn := widget.NewButton("Import Folder…", func() {
		b.OpenImportDialog()
	})

	manageTagsBtn := widget.NewButton("Manage Tags…", func() {
		b.OpenManageTagsDialog()
	})

	refreshThumbsBtn := widget.NewButton("Refresh Thumbnails", func() {
		b.rebuild(true)
	})

	filters := container.NewGridWithColumns(3, b.tagFilter, b.siteFilter, b.pathFilter)
	toolbar := container.NewBorder(
		nil, nil, nil,
		container.NewHBox(search, clear, importBtn, manageTagsBtn, refreshThumbsBtn),
		filters,
	)

	b.pageLabel = widget.NewLabel("")
	b.prevPageBtn = widget.NewButton("← Page", func() { b.changePageAsync(-1) })
	b.nextPageBtn = widget.NewButton("Page →", func() { b.changePageAsync(1) })
	b.prevPageBtn.Disable()
	b.nextPageBtn.Disable()
	pageNav := container.NewHBox(b.prevPageBtn, b.pageLabel, b.nextPageBtn)
	footer := container.NewBorder(nil, nil, nil, pageNav, b.status)

	sampleLabel := widget.NewLabel("Wg")
	b.thumbCellHeight = float32(b.thumbSize) + theme.Padding() + sampleLabel.MinSize().Height

	b.grid = container.New(&ThumbnailGridLayout{
		CellWidth:  float32(b.thumbSize),
		CellHeight: b.thumbCellHeight,
	})
	b.scroll = container.NewVScroll(b.grid)

	b.root = container.NewBorder(toolbar, footer, nil, nil, b.scroll)
	return b
}

// SetWindow associates w with the browser, so dialogs it opens (import
// folder picker, error dialogs) are anchored to it.
func (b *Browser) SetWindow(w fyne.Window) {
	b.win = w
}

// SetStore associates an inventory database with the browser. Search and
// Import are no-ops without one.
func (b *Browser) SetStore(store *invlib.DB) {
	b.store = store
}

// Canvas returns the browser's root content, suitable for w.SetContent.
func (b *Browser) Canvas() fyne.CanvasObject {
	return b.root
}

// InitialSize returns the window size the browser is designed to open at.
func (b *Browser) InitialSize() fyne.Size {
	return fyne.NewSize(1200, 850)
}

// OpenImportDialog shows the folder picker and imports every image found
// under the chosen directory (recursively) into the inventory database.
func (b *Browser) OpenImportDialog() {
	d := filedialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil || lu == nil {
			return
		}

		b.lastImportLoc = lu
		b.importAsync(lu.Path())
	}, b.win)

	if b.lastImportLoc != nil {
		d.SetLocation(b.lastImportLoc)
	}

	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// beginLoad cancels whichever search is currently in flight, derives a
// fresh cancellable context from parent, and bumps the generation counter.
// Every call to Search goes through this — whether invoked directly
// (main.go's startup search) or via searchAsync — so two overlapping
// searches (e.g. the user edits a filter and hits Search twice quickly)
// can never race to apply stale results.
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

// searchAsync runs Search on a background goroutine at page 1, built from
// the current filter entries, so a slow query never blocks the UI thread.
// It is for actions that change which filter is active (the Search/Clear
// buttons, submitting a filter entry) — anywhere the active filter isn't
// changing should use refreshAsync instead, to avoid surprising the user
// by resetting their page back to 1.
func (b *Browser) searchAsync() {
	filter := Filter{
		Tag:  strings.TrimSpace(b.tagFilter.Text),
		Site: strings.TrimSpace(b.siteFilter.Text),
		Path: strings.TrimSpace(b.pathFilter.Text),
	}
	go func() {
		if err := b.Search(context.Background(), filter, 0); err != nil {
			log.Printf("search inventory: %v", err)
		}
	}()
}

// refreshAsync re-runs the most recently completed Search's filter at its
// same page, for refreshing the grid after a change that doesn't alter
// which filter is active (editing or deleting a record via the viewer) —
// unlike searchAsync, it never resets the page back to 1.
func (b *Browser) refreshAsync() {
	b.mu.Lock()
	filter, page := b.filter, b.page
	b.mu.Unlock()
	go func() {
		if err := b.Search(context.Background(), filter, page); err != nil {
			log.Printf("search inventory: %v", err)
		}
	}()
}

// changePageAsync re-runs the most recently completed Search's filter at
// page+delta. It is a no-op if the resulting page would be negative or
// past the last page — Prev/Next are also disabled in that case (see
// updatePageControls), but changePageAsync guards independently since it
// can be invoked while a previous search is still in flight.
func (b *Browser) changePageAsync(delta int) {
	b.mu.Lock()
	filter, page, total := b.filter, b.page, b.total
	b.mu.Unlock()

	page += delta
	if page < 0 || page*pageSize >= max(total, 1) {
		return
	}

	go func() {
		if err := b.Search(context.Background(), filter, page); err != nil {
			log.Printf("search inventory: %v", err)
		}
	}()
}

// importAsync runs Import on a background goroutine so the caller (a UI
// callback running on the Fyne event loop) never blocks on a slow
// filesystem walk or hashing pass.
func (b *Browser) importAsync(dir string) {
	go func() {
		if err := b.Import(context.Background(), dir); err != nil {
			log.Printf("import %q: %v", dir, err)
		}
	}()
}

// Search queries the store for page's records (0-based, pageSize per
// page) matching filter and updates the browser's grid and page controls
// with the result. It is safe to call from either a UI callback or a
// goroutine the caller started; widget updates are marshalled through
// fyne.Do. If a newer Search/Import call supersedes this one before it
// finishes, its result is discarded instead of overwriting newer data.
func (b *Browser) Search(ctx context.Context, filter Filter, page int) error {
	ctx, generation := b.beginLoad(ctx)

	if b.store == nil {
		if b.isCurrentLoad(generation) {
			fyne.Do(func() {
				b.status.SetText("No inventory database available.")
				b.updatePageControls(0, 0)
			})
		}
		return nil
	}

	if page < 0 {
		page = 0
	}

	records, total, err := searchRecords(ctx, b.store, filter, pageSize, page*pageSize)
	if err != nil {
		if b.isCurrentLoad(generation) {
			fyne.Do(func() { b.status.SetText(err.Error()) })
		}
		return err
	}

	b.mu.Lock()
	if generation != b.loadGeneration {
		b.mu.Unlock()
		return nil
	}
	// A metadata-only edit (site/tags) in the viewer calls refreshAsync,
	// which re-runs this same filter/page — but site/tags aren't shown in
	// the grid, so unless the set of files or their order actually
	// changed, there's nothing for the grid to redraw. Skipping rebuild in
	// that case avoids tearing down and regenerating every thumbnail on
	// the page for a change that's visually invisible to it.
	gridUnchanged := sameFileSequence(b.records, records)
	b.records = records
	b.filter = filter
	b.page = page
	b.total = total
	b.mu.Unlock()

	fyne.Do(func() {
		if !b.isCurrentLoad(generation) {
			return
		}
		b.updatePageControls(page, total)
		if !gridUnchanged {
			b.rebuild(false)
		}
	})
	return nil
}

// sameFileSequence reports whether a and b identify the same files in the
// same order.
func sameFileSequence(a, b []imageRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].FileID != b[i].FileID {
			return false
		}
	}
	return true
}

// updatePageControls sets the page label (e.g. "Page 3 of 24 (4,712
// images)") and enables/disables Prev/Next for the given page/total.
// Must be called on the Fyne event loop.
func (b *Browser) updatePageControls(page, total int) {
	totalPages := max((total+pageSize-1)/pageSize, 1)
	b.pageLabel.SetText(fmt.Sprintf("Page %d of %d (%d images)", page+1, totalPages, total))

	if page > 0 {
		b.prevPageBtn.Enable()
	} else {
		b.prevPageBtn.Disable()
	}
	if page+1 < totalPages {
		b.nextPageBtn.Enable()
	} else {
		b.nextPageBtn.Disable()
	}
}

// Import recursively scans dir for images, indexes every one found into
// the inventory database (preserving any existing metadata — see
// indexOne), then re-runs the current filter so newly imported files that
// match it appear immediately. It is a no-op if no store is configured.
func (b *Browser) Import(ctx context.Context, dir string) error {
	if b.store == nil {
		fyne.Do(func() { b.status.SetText("No inventory database available; cannot import.") })
		return nil
	}

	fyne.Do(func() { b.status.SetText(fmt.Sprintf("Scanning %s…", dir)) })

	items, err := images.ScanRecursive(ctx, dir)
	if err != nil {
		fyne.Do(func() { b.status.SetText(err.Error()) })
		return err
	}

	fyne.Do(func() { b.status.SetText(fmt.Sprintf("Indexing %d images…", len(items))) })
	b.indexAll(ctx, items)

	b.searchAsync()
	return nil
}

// indexAll indexes every item into the store, bounded by thumbSem so a
// large import doesn't spawn one goroutine per file. It blocks until every
// item is indexed, so the caller can reliably refresh the grid afterward.
// Indexing is best-effort: a failure for one file is logged and does not
// affect the others.
func (b *Browser) indexAll(ctx context.Context, items []images.Item) {
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func(item images.Item) {
			defer wg.Done()
			b.thumbSem <- struct{}{}
			defer func() { <-b.thumbSem }()

			if err := b.indexOne(ctx, item); err != nil {
				log.Printf("index %q: %v", item.Path, err)
			}
		}(item)
	}
	wg.Wait()
}

// indexOne records (or re-records) item's file-level metadata — size,
// hash, modified time — and its 1:1 image row (width/height), without
// touching the file's origin or tags. For a file already in the database,
// it fetches the existing row and updates it in place rather than
// constructing a new one, so columns indexOne doesn't know about
// (origin_id, created_at, status, error, ...) are preserved exactly as
// re-importing an already-tagged file must not disturb its metadata.
func (b *Browser) indexOne(ctx context.Context, item images.Item) error {
	info, err := os.Stat(item.Path)
	if err != nil {
		return err
	}

	hash, err := images.Hash(item.Path)
	if err != nil {
		return err
	}

	width, height, err := images.Dimensions(item.Path)
	if err != nil {
		return err
	}

	file, err := b.store.GetFileByPath(ctx, item.Path)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	modTime := info.ModTime().UTC()
	if file == nil {
		file = &invlib.File{
			Path:       item.Path,
			Filename:   filepath.Base(item.Path),
			Filesize:   info.Size(),
			Hash:       &hash,
			ModifiedAt: &modTime,
		}
		if err := b.store.CreateFile(ctx, file); err != nil {
			return err
		}
	} else {
		file.Filesize = info.Size()
		file.Hash = &hash
		file.ModifiedAt = &modTime
		if err := b.store.UpdateFile(ctx, file); err != nil {
			return err
		}
		if err := b.store.TouchScanned(ctx, file.ID); err != nil {
			return err
		}
	}

	return b.store.UpsertImage(ctx, &invlib.Image{FileID: file.ID, Width: &width, Height: &height})
}

// rebuild renders b.records as a flat grid. Thumbnail generation for the
// whole result set is bounded to imgPerDir concurrent images.
func (b *Browser) rebuild(force bool) {
	b.grid.Objects = nil

	b.mu.Lock()
	records := append([]imageRecord(nil), b.records...)
	b.mu.Unlock()

	imgs := make([]*canvas.Image, len(records))
	for i, rec := range records {
		index := i
		card, img := b.newThumbnailCard(rec, func() { b.openViewer(records, index) })
		imgs[i] = img
		b.grid.Add(card)
	}

	b.grid.Refresh()
	b.scroll.Refresh()
	b.root.Refresh()

	go b.generateThumbnails(records, imgs, force)
}

// newThumbnailCard builds a thumbnail widget for rec, without starting
// thumbnail generation. It returns the widget plus the canvas.Image it
// wraps, so the caller can drive generation separately (via
// generateThumbnails) once every widget in a batch has been built.
func (b *Browser) newThumbnailCard(rec imageRecord, onTap func()) (fyne.CanvasObject, *canvas.Image) {
	img := canvas.NewImageFromFile("")
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(
		fyne.NewSize(
			float32(b.thumbSize),
			float32(b.thumbSize),
		),
	)

	name := filepath.Base(rec.Path)
	clickLabel := newClickableLabel(name, fyne.TextStyle{}, func() {
		fyne.CurrentApp().Clipboard().SetContent(rec.Path)
	})
	clickLabel.label.Alignment = fyne.TextAlignCenter
	clickLabel.label.Truncation = fyne.TextTruncateEllipsis

	card := container.NewVBox(img, clickLabel)
	return newThumbnailWidget(card, onTap), img
}

// generateThumbnails fills in imgs[i] with records[i]'s cached thumbnail,
// bounding concurrent decode/resize work to imgPerDir — without this, a
// large result set would spawn one goroutine per image competing for CPU
// and memory. force forces regeneration of thumbnails that already exist;
// it is a no-op if no cache is configured.
func (b *Browser) generateThumbnails(records []imageRecord, imgs []*canvas.Image, force bool) {
	if b.cache == nil {
		return
	}

	sem := make(chan struct{}, imgPerDir)
	var wg sync.WaitGroup
	for i, rec := range records {
		img := imgs[i]
		wg.Add(1)
		go func(path string, img *canvas.Image) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cachedPath, err := b.cache.Generate(path, force)
			if err != nil {
				log.Printf("generate thumbnail for %q: %v", path, err)
				return
			}

			fyne.Do(func() {
				img.File = cachedPath
				img.Refresh()
			})
		}(rec.Path, img)
	}
	wg.Wait()
}

func (b *Browser) openViewer(records []imageRecord, index int) {
	if index < 0 || index >= len(records) {
		return
	}

	v := newViewer(b.win, b.store, records, index, func() { b.refreshAsync() })
	v.Show()
}
