package main

import (
	"image/color"
	"slices"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
	"go-photo/internal/filedialog"
)

type systemData struct {
	folderURIs   []fyne.URI
	fullscreen   bool
	uriIndex     int
	windowWidth  float64
	windowHeight float64
}

var floating_index = 0
var lastOpenLocation fyne.ListableURI

func main() {
	myApp := app.New()
	myWindow := myApp.NewWindow("goPhotos")

	sysData := systemData{}
	sysData.fullscreen = false
	sysData.windowWidth = 1280.0
	sysData.windowHeight = 720.0

	// Define menu items
	fileOpenDialog := fyne.NewMenuItem("Open File", func() {
		file_dialog := filedialog.NewFileOpen(func(uc fyne.URIReadCloser, err error) {
			if uc == nil || err != nil {
				return
			}
			defer uc.Close()

			// Remember the directory containing the selected file.
			parent, err := storage.Parent(uc.URI())
			if err == nil {
				lastOpenLocation, err = storage.ListerForURI(parent)
				if err != nil {
					lastOpenLocation = nil
				}
			}

			image := canvas.NewImageFromURI(uc.URI())
			image.FillMode = canvas.ImageFillContain
			myWindow.SetContent(image)
		}, myWindow)

		file_dialog.SetFilter(
			storage.NewExtensionFileFilter([]string{".jpg", ".png"}),
		)

		if lastOpenLocation != nil {
			file_dialog.SetLocation(lastOpenLocation)
		}

		file_dialog.Resize(fyne.NewSize(900, 600))
		file_dialog.Show()
	})

	folderOpenDialog := fyne.NewMenuItem("Open Folder", func() {
		folder_dialog := filedialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if lu == nil || err != nil {
				return
			}
			lastOpenLocation = lu
			dat, _ := lu.List()
			var dtx []fyne.URI
			for i := range dat {
				data := dat[i]
				if slices.Contains([]string{".jpg", ".png"}, data.Extension()) {
					dtx = append(dtx, data)
				}
			}
			sysData.folderURIs = dtx
			folderImageLoad(myWindow, sysData, 0)
		}, myWindow)

		if lastOpenLocation != nil {
			folder_dialog.SetLocation(lastOpenLocation)
		}

		folder_dialog.Resize(fyne.NewSize(900, 600))
		folder_dialog.Show()
	})

	quitItem := fyne.NewMenuItem("Quit", myWindow.Close)

	fileMenu := fyne.NewMenu("File", fileOpenDialog, folderOpenDialog, fyne.NewMenuItemSeparator(), quitItem)

	aboutDialog := dialog.NewCustom(
		"About",
		"Close",
		widget.NewCard(
			"goPhotos",
			"golang photo viewer",
			widget.NewRichTextFromMarkdown(`
		golang photo viewer, written in golang using fyne GUI framework.`),
		),
		myWindow,
	)

	aboutMenuItem := fyne.NewMenuItem("About", func() {
		aboutDialog.Show()
	})

	settingsMenu := fyne.NewMenu("Settings", fyne.NewMenuItem("Fullscreen Mode", func() {
		sysData.fullscreen = !sysData.fullscreen
		myWindow.SetFullScreen(sysData.fullscreen)

		if len(sysData.folderURIs) > 0 {
			folderImageLoad(myWindow, sysData, floating_index)
		}
		if !sysData.fullscreen {
			myWindow.Resize(fyne.NewSize(float32(sysData.windowWidth), float32(sysData.windowHeight)))
		}
	}))

	aboutMenu := fyne.NewMenu("About", aboutMenuItem)

	mainmm := fyne.NewMainMenu(fileMenu, settingsMenu, aboutMenu)
	myWindow.SetMainMenu(mainmm)

	text4 := canvas.NewText("Use File > Open File, to open a single file, and File > Open Folder, to open all the files in the folder.", color.Black)
	centered := container.New(layout.NewHBoxLayout(), layout.NewSpacer(), text4, layout.NewSpacer())

	myWindow.SetContent(centered)
	myWindow.Resize(fyne.NewSize(1280, 720))
	myWindow.ShowAndRun()
}

func folderImageLoad(mainWindow fyne.Window, d systemData, index int) {
	d.uriIndex = index
	floating_index = index
	if index < 0 || index >= len(d.folderURIs) {
		return
	}

	image := canvas.NewImageFromURI(d.folderURIs[index])
	image.FillMode = canvas.ImageFillContain
	renderWidth := d.windowWidth
	renderHeight := d.windowHeight

	if mainWindow.Canvas().Size().Width > float32(d.windowWidth) {
		renderWidth = float64(mainWindow.Canvas().Size().Width - 10.0)
	}
	if mainWindow.Canvas().Size().Height > float32(d.windowHeight) {
		renderHeight = float64(mainWindow.Canvas().Size().Height - 80.0)
	}
	image.SetMinSize(fyne.NewSize(float32(renderWidth), float32(renderHeight)))
	image.ScaleMode = canvas.ImageScaleFastest

	prevImage := widget.NewButton("<< Previous", func() {
		folderImageLoad(mainWindow, d, index-1)
	})
	nextImage := widget.NewButton("Next >>", func() {
		folderImageLoad(mainWindow, d, index+1)
	})

	mainWindow.Canvas().SetOnTypedKey(func(k *fyne.KeyEvent) {
		switch k.Name {
		case fyne.KeyLeft:
			folderImageLoad(mainWindow, d, index-1)
		case fyne.KeyRight:
			folderImageLoad(mainWindow, d, index+1)
		}
	})

	fileName := canvas.NewText(d.folderURIs[index].Name(), color.Black)
	content := container.New(layout.NewHBoxLayout(), prevImage, layout.NewSpacer(), fileName, layout.NewSpacer(), nextImage)
	centered := container.New(layout.NewHBoxLayout(), image)
	vbox := container.New(layout.NewVBoxLayout(), centered, content)
	mainWindow.SetContent(vbox)
}
