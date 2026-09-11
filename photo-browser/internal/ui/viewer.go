package ui

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"photo-browser/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

type viewer struct {
	win       fyne.Window
	items     []images.Item
	index     int
	image     *canvas.Image
	info      *widget.Label
	container *fyne.Container
}

func newViewer(parent fyne.Window, items []images.Item, index int) *viewer {
	v := &viewer{
		items: items,
		index: index,
	}

	v.image = canvas.NewImageFromFile(items[index].Path)
	v.image.FillMode = canvas.ImageFillContain

	v.info = widget.NewLabel("")
	prev := widget.NewButton("← Previous", func() { v.move(-1) })
	next := widget.NewButton("Next →", func() { v.move(1) })
	open := widget.NewButton("Open", func() { openExternal(items[v.index].Path) })

	toolbar := container.NewBorder(nil, nil, prev, container.NewHBox(open, next), v.info)
	v.container = container.NewBorder(toolbar, nil, nil, nil, v.image)

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
			openExternal(v.items[v.index].Path)
		case fyne.KeyDelete, fyne.KeyBackspace:
			if err := v.moveCurrentToTrash(); err != nil {
				fmt.Printf("failed to move image to Trash: %v\n", err)
			}
		}
	})

	_ = parent
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
	if runtime.GOOS != "darwin" || len(v.items) == 0 {
		return nil
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
	if w, h, err := imageDimensions(item.Path); err == nil {
		parts = append(parts, fmt.Sprintf("%d x %d", w, h))
	}

	v.info.SetText(strings.Join(parts, "    "))
}

func imageDimensions(path string) (width, height int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
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

func openExternal(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
