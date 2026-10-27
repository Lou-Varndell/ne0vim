package ui

import (
	"context"
	"fmt"
	"inventory-manager/internal/images"
	"inventory-manager/internal/trash"
	"os"
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
	tagsEntry  *tagInput
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
	v.tagsEntry = newTagInput(store)
	v.saveStatus = widget.NewLabel("")

	save := widget.NewButton("Save Metadata", func() { v.saveMetadata() })
	deleteRecord := widget.NewButton("Delete Record", func() { v.deleteCurrent() })
	deleteRecord.Importance = widget.DangerImportance

	// Left: metadata controls.
	// Right: image viewer.
	metadataPanel := container.NewVBox(
		widget.NewLabel("Site:"),
		v.siteEntry,
		widget.NewLabel("Tags:"),
		v.tagsEntry.CanvasObject(),
		container.NewHBox(save, deleteRecord),
		v.saveStatus,
	)

	prev := widget.NewButton("← Previous", func() { v.move(-1) })
	next := widget.NewButton("Next →", func() { v.move(1) })
	open := widget.NewButton("Open", func() {
		if err := openExternal(v.records[v.index].Path); err != nil {
			dialog.ShowError(err, v.win)
		}
	})
	backToGrid := widget.NewButton("← Back to Grid", func() { v.backToGrid() })
	copyPath := widget.NewButton("Copy Path", func() {
		fyne.CurrentApp().Clipboard().SetContent(v.records[v.index].Path)
	})

	toolbar := container.NewBorder(
		nil,
		nil,
		prev,
		container.NewHBox(backToGrid, copyPath, open, next),
		v.info,
	)

	content := container.NewHSplit(metadataPanel, v.imageTap)
	content.SetOffset(0.28) // Metadata gets 28% of the width.

	v.container = container.NewBorder(
		toolbar,
		nil,
		nil,
		nil,
		content,
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
		case fyne.KeyDelete, fyne.KeyBackspace:
			v.trashCurrent()
		}
	})
	v.win.Canvas().SetOnTypedRune(func(r rune) {
		switch r {
		case 't', 'T':
			v.trashCurrent()
		case 'g', 'G':
			v.backToGrid()
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
	tags := splitTags(v.tagsEntry.Text())
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
	v.applySiteEditable(site)

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

	v.removeCurrent()
}

// trashCurrent moves the current record's file to the OS trash and removes
// its database record, keeping the two in sync (unlike deleteCurrent, which
// leaves the file on disk). Advances to the next record and notifies
// onChange, same as deleteCurrent.
func (v *viewer) trashCurrent() {
	if len(v.records) == 0 || v.store == nil {
		return
	}

	rec := v.records[v.index]
	if err := trash.Move(rec.Path); err != nil {
		dialog.ShowError(err, v.win)
		return
	}
	if err := v.store.DeleteFile(context.Background(), rec.FileID); err != nil {
		dialog.ShowError(err, v.win)
		return
	}

	v.removeCurrent()
}

// removeCurrent drops the current record from the in-memory list, advances
// to a valid index, and refreshes the display. Shared by deleteCurrent and
// trashCurrent after they've each handled their own deletion side effect.
func (v *viewer) removeCurrent() {
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

// backToGrid closes the viewer and returns keyboard focus to the main
// browser window — the keyboard equivalent of the "Back to Grid" button.
func (v *viewer) backToGrid() {
	v.win.Close()
	v.parent.RequestFocus()
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
	v.applySiteEditable(rec.Site)
	v.tagsEntry.SetTextSilently(strings.Join(rec.Tags, ", "))
	v.saveStatus.SetText("")
}

// applySiteEditable enables siteEntry only while site is unset. Site is
// normally captured by the scraper (or set once manually); once it has a
// value, disabling the field prevents a stray edit here from overwriting
// that provenance — it only accepts input to fill in a site that's
// currently missing.
func (v *viewer) applySiteEditable(site string) {
	if site == "" {
		v.siteEntry.Enable()
	} else {
		v.siteEntry.Disable()
	}
}

// showFullResolution opens the current image in a new window at its native
// pixel dimensions (1:1, no scaling), in a scrollable viewport in case the
// image is larger than the screen.
func (v *viewer) showFullResolution() {
	rec := v.records[v.index]
	f, err := os.Open(rec.Path)
	if err != nil {
		fmt.Println(err)
	}
	defer f.Close()

	// Check if the image is an animated WebP
	isAnimated, err := invlib.IsAnimatedWebP(f)
	if err != nil {
		fmt.Println(err)
	}

	if isAnimated {
		err := images.PlayAnimation(rec.Path)
		if err != nil {

		}
	} else {

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
