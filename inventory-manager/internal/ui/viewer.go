package ui

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	invlib "inv-lib"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type viewer struct {
	win      fyne.Window
	parent   fyne.Window
	store    *invlib.DB
	records  []imageRecord
	index    int
	onChange func()

	image    *canvas.Image
	imageTap *thumbnailWidget
	info     *widget.Label

	siteEntry  *widget.Entry
	tagsEntry  *widget.Entry
	saveStatus *widget.Label

	container *fyne.Container
}

// newViewer opens a full-size viewer for records[index], with
// previous/next navigation across records and inline editing of each
// record's site and tags metadata. onChange is called after a successful
// metadata save or record deletion, so the caller (Browser) can refresh
// its grid to reflect the change; it may be nil. store may be nil, in
// which case metadata editing and deletion are disabled.
func newViewer(parent fyne.Window, store *invlib.DB, records []imageRecord, index int, onChange func()) *viewer {
	v := &viewer{
		parent: parent,
		store:  store,
		// Defensive copy: deleteCurrent mutates v.records in place, which
		// would otherwise corrupt the browser's grid state without going
		// through its mutex.
		records:  append([]imageRecord(nil), records...),
		index:    index,
		onChange: onChange,
	}

	v.image = canvas.NewImageFromFile(v.records[index].Path)
	v.image.FillMode = canvas.ImageFillContain
	// Wrapped so the image itself is clickable to open a full-resolution
	// (1:1) view, without changing how v.image is updated elsewhere (move
	// still sets v.image.File/Refresh directly).
	v.imageTap = newThumbnailWidget(v.image, func() { v.showFullResolution() })

	v.info = widget.NewLabel("")

	v.siteEntry = widget.NewEntry()
	v.siteEntry.SetPlaceHolder("Source site")
	v.tagsEntry = widget.NewEntry()
	v.tagsEntry.SetPlaceHolder("comma, separated, tags")
	v.saveStatus = widget.NewLabel("")

	save := widget.NewButton("Save Metadata", func() { v.saveMetadata() })
	deleteRecord := widget.NewButton("Delete Record", func() { v.deleteCurrent() })
	deleteRecord.Importance = widget.DangerImportance

	metadata := container.NewVBox(
		widget.NewLabel("Site:"), v.siteEntry,
		widget.NewLabel("Tags:"), v.tagsEntry,
		container.NewHBox(save, deleteRecord, v.saveStatus),
	)

	prev := widget.NewButton("← Previous", func() { v.move(-1) })
	next := widget.NewButton("Next →", func() { v.move(1) })
	open := widget.NewButton("Open", func() {
		if err := openExternal(v.records[v.index].Path); err != nil {
			dialog.ShowError(err, v.win)
		}
	})
	backToGrid := widget.NewButton("← Back to Grid", func() {
		v.win.Close()
		v.parent.RequestFocus()
	})
	copyPath := widget.NewButton("Copy Path", func() {
		fyne.CurrentApp().Clipboard().SetContent(v.records[v.index].Path)
	})

	toolbar := container.NewBorder(nil, nil, prev, container.NewHBox(backToGrid, copyPath, open, next), v.info)
	v.container = container.NewBorder(
		container.NewVBox(toolbar, metadata),
		nil, nil, nil,
		v.imageTap,
	)

	v.win = fyne.CurrentApp().NewWindow("Image Viewer")
	v.win.Resize(fyne.NewSize(1100, 850))
	v.win.SetContent(v.container)
	v.update()

	// Keyboard navigation: the entry widget receives key events reliably
	// when the viewer has focus.
	v.win.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		switch ev.Name {
		case fyne.KeyLeft, fyne.KeyUp:
			v.move(-1)
		case fyne.KeyRight, fyne.KeyDown:
			v.move(1)
		case fyne.KeyEscape:
			v.win.Close()
		case fyne.KeyReturn, fyne.KeySpace:
			if err := openExternal(v.records[v.index].Path); err != nil {
				dialog.ShowError(err, v.win)
			}
		}
	})

	return v
}

func (v *viewer) Show() {
	v.win.Show()
}

func (v *viewer) move(delta int) {
	n := len(v.records)
	if n == 0 {
		return
	}

	v.index += delta
	if v.index < 0 {
		v.index = n - 1
	}
	if v.index >= n {
		v.index = 0
	}

	v.image.File = v.records[v.index].Path
	v.image.Resource = nil
	v.image.Refresh()
	v.update()
}

// saveMetadata persists the site and tags entries for the current record
// to the inventory database and notifies onChange so the browser's grid
// (and whatever filter it's applying) picks up the change.
func (v *viewer) saveMetadata() {
	if v.store == nil {
		return
	}

	rec := &v.records[v.index]
	site := strings.TrimSpace(v.siteEntry.Text)
	tags := splitTags(v.tagsEntry.Text)
	ctx := context.Background()

	if err := setSite(ctx, v.store, rec.FileID, site); err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	if err := setTags(ctx, v.store, rec.FileID, tags); err != nil {
		dialog.ShowError(err, v.win)
		return
	}

	rec.Site = site
	rec.Tags = tags

	v.saveStatus.SetText("Saved.")
	if v.onChange != nil {
		v.onChange()
	}
}

// deleteCurrent removes the current record from the inventory database
// (the underlying file on disk is left untouched), advances to the next
// record, and notifies onChange so the browser's grid stays in sync.
func (v *viewer) deleteCurrent() {
	if len(v.records) == 0 || v.store == nil {
		return
	}

	fileID := v.records[v.index].FileID
	if err := v.store.DeleteFile(context.Background(), fileID); err != nil {
		dialog.ShowError(err, v.win)
		return
	}

	v.records = append(v.records[:v.index], v.records[v.index+1:]...)
	if v.onChange != nil {
		v.onChange()
	}

	if len(v.records) == 0 {
		v.win.Close()
		return
	}
	if v.index >= len(v.records) {
		v.index = len(v.records) - 1
	}

	v.image.File = v.records[v.index].Path
	v.image.Resource = nil
	v.image.Refresh()
	v.update()
}

func splitTags(raw string) []string {
	parts := strings.Split(raw, ",")
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

func (v *viewer) update() {
	rec := v.records[v.index]
	parts := []string{
		fmt.Sprintf("%d / %d", v.index+1, len(v.records)),
		filepath.Base(rec.Path),
		formatBytes(rec.Size),
		fmt.Sprintf("%d x %d", rec.Width, rec.Height),
	}
	v.info.SetText(strings.Join(parts, "    "))

	v.siteEntry.SetText(rec.Site)
	v.tagsEntry.SetText(strings.Join(rec.Tags, ", "))
	v.saveStatus.SetText("")
}

// showFullResolution opens the current image in a new window at its native
// pixel dimensions (1:1, no scaling), in a scrollable viewport in case the
// image is larger than the screen.
func (v *viewer) showFullResolution() {
	rec := v.records[v.index]

	full := canvas.NewImageFromFile(rec.Path)
	full.FillMode = canvas.ImageFillOriginal

	fullWin := fyne.CurrentApp().NewWindow(fmt.Sprintf("Full Resolution — %s", filepath.Base(rec.Path)))
	fullWin.SetContent(container.NewScroll(full))

	// Clamped so the window stays large enough to be usable for small
	// images (icons, tiny screenshots) but never bigger than fits
	// comfortably on screen for huge ones.
	const minWinDim, maxWinDim = 300, 1600
	winWidth := float32(max(min(rec.Width, maxWinDim), minWinDim))
	winHeight := float32(max(min(rec.Height, maxWinDim), minWinDim))
	fullWin.Resize(fyne.NewSize(winWidth, winHeight))

	fullWin.Show()
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n >= div*unit && exp < 4 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGT"[exp])
}

// openExternal launches path in the platform's default viewer/handler.
func openExternal(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	return nil
}
