package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"inventory-manager/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// errTrashUnsupported is returned by moveCurrentToTrash on platforms other
// than macOS, where there is no equivalent of "tell Finder to delete".
var errTrashUnsupported = errors.New("moving to Trash is only supported on macOS")

type viewer struct {
	win       fyne.Window
	parent    fyne.Window
	items     []images.Item
	index     int
	image     *canvas.Image
	imageTap  *thumbnailWidget
	info      *widget.Label
	container *fyne.Container
}

func newViewer(parent fyne.Window, items []images.Item, index int) *viewer {
	v := &viewer{
		parent: parent,
		// Defensive copy: when opened from a subdirectory preview row, items
		// aliases Browser.previews[i].items. moveCurrentToTrash mutates
		// v.items in place, which would otherwise corrupt the browser's
		// preview state without going through its mutex.
		items: append([]images.Item(nil), items...),
		index: index,
	}

	v.image = canvas.NewImageFromFile(items[index].Path)
	v.image.FillMode = canvas.ImageFillContain
	// Wrapped so the image itself is clickable to open a full-resolution
	// (1:1) view, without changing how v.image is updated elsewhere (move,
	// moveCurrentToTrash still set v.image.File/Refresh directly).
	v.imageTap = newThumbnailWidget(v.image, func() { v.showFullResolution() })

	v.info = widget.NewLabel("")
	prev := widget.NewButton("← Previous", func() { v.move(-1) })
	next := widget.NewButton("Next →", func() { v.move(1) })
	open := widget.NewButton("Open", func() {
		if err := openExternal(v.items[v.index].Path); err != nil {
			dialog.ShowError(err, v.win)
		}
	})
	backToGrid := widget.NewButton("← Back to Grid", func() {
		v.win.Close()
		v.parent.RequestFocus()
	})
	copyPath := widget.NewButton("Copy Path", func() {
		fyne.CurrentApp().Clipboard().SetContent(v.items[v.index].Path)
	})

	toolbar := container.NewBorder(nil, nil, prev, container.NewHBox(backToGrid, copyPath, open, next), v.info)
	v.container = container.NewBorder(
		toolbar,
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
			if err := openExternal(v.items[v.index].Path); err != nil {
				dialog.ShowError(err, v.win)
			}
		case fyne.KeyDelete, fyne.KeyBackspace:
			if err := v.moveCurrentToTrash(); err != nil {
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
	n := len(v.items)
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

	v.image.File = v.items[v.index].Path
	v.image.Resource = nil
	v.image.Refresh()
	v.update()
}

func (v *viewer) moveCurrentToTrash() error {
	if len(v.items) == 0 {
		return nil
	}
	if runtime.GOOS != "darwin" {
		return errTrashUnsupported
	}

	path := v.items[v.index].Path

	script := `
on run argv
	set theFile to POSIX file (item 1 of argv) as alias
	tell application "Finder" to delete theFile
end run
`

	cmd := exec.Command("osascript", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}

	v.items = append(v.items[:v.index], v.items[v.index+1:]...)

	if len(v.items) == 0 {
		v.win.Close()
		return nil
	}

	if v.index >= len(v.items) {
		v.index = len(v.items) - 1
	}

	v.image.File = v.items[v.index].Path
	v.image.Resource = nil
	v.image.Refresh()
	v.update()

	return nil
}

func (v *viewer) update() {
	item := v.items[v.index]
	parts := []string{fmt.Sprintf("%d / %d", v.index+1, len(v.items)), item.Name}

	if info, err := os.Stat(item.Path); err == nil {
		parts = append(parts, formatBytes(info.Size()))
	}
	if w, h, err := images.Dimensions(item.Path); err == nil {
		parts = append(parts, fmt.Sprintf("%d x %d", w, h))
	}

	v.info.SetText(strings.Join(parts, "    "))
}

// showFullResolution opens the current image in a new window at its native
// pixel dimensions (1:1, no scaling), in a scrollable viewport in case the
// image is larger than the screen.
func (v *viewer) showFullResolution() {
	item := v.items[v.index]

	full := canvas.NewImageFromFile(item.Path)
	full.FillMode = canvas.ImageFillOriginal

	fullWin := fyne.CurrentApp().NewWindow(fmt.Sprintf("Full Resolution — %s", item.Name))
	fullWin.SetContent(container.NewScroll(full))

	// Clamped so the window stays large enough to be usable for small
	// images (icons, tiny screenshots) but never bigger than fits
	// comfortably on screen for huge ones.
	const minWinDim, maxWinDim = 300, 1600
	winWidth, winHeight := float32(1100), float32(850)
	if w, h, err := images.Dimensions(item.Path); err == nil {
		winWidth = float32(max(min(w, maxWinDim), minWinDim))
		winHeight = float32(max(min(h, maxWinDim), minWinDim))
	}
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
