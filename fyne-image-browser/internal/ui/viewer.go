package ui

import (
	"fmt"
	"image"
	"os"
	"os/exec"
	"runtime"

	"fyne-image-browser/internal/images"

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

func (v *viewer) update() {
	item := v.items[v.index]
	info, err := os.Stat(item.Path)
	if err == nil {
		v.info.SetText(fmt.Sprintf("%d / %d    %s    %s",
			v.index+1, len(v.items), item.Name, formatBytes(info.Size())))
	} else {
		v.info.SetText(fmt.Sprintf("%d / %d    %s", v.index+1, len(v.items), item.Name))
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

var _ = image.Point{}
