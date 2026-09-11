// Command photo-browser opens a Fyne GUI for browsing image thumbnails in a
// directory, with a native menu bar and folder picker.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"photo-browser/internal/ui"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

// windowState tracks the window size to restore when leaving fullscreen,
// since Fyne does not remember it automatically.
type windowState struct {
	fullscreen bool
	width      float32
	height     float32
}

func main() {
	dir := flag.String("dir", ".", "directory to browse")
	flag.Parse()

	a := app.NewWithID("com.local.photobrowser")
	w := a.NewWindow("Photo Browser")

	browser := ui.NewBrowser(*dir)
	browser.SetWindow(w)
	w.SetContent(browser.Canvas())

	state := windowState{width: 1200, height: 850}
	w.Resize(fyne.NewSize(state.width, state.height))

	w.SetMainMenu(buildMainMenu(w, browser, &state))
	w.Show()

	go func() {
		if err := browser.LoadDirectory(context.Background(), *dir); err != nil {
			log.Printf("load directory: %v", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("could not load %q: %w", *dir, err), w)
			})
		}
	}()

	a.Run()
}

func buildMainMenu(w fyne.Window, browser *ui.Browser, state *windowState) *fyne.MainMenu {
	openFolder := fyne.NewMenuItem("Open Folder", func() {
		browser.OpenFolderDialog()
	})
	quit := fyne.NewMenuItem("Quit", w.Close)
	fileMenu := fyne.NewMenu("File", openFolder, fyne.NewMenuItemSeparator(), quit)

	fullscreen := fyne.NewMenuItem("Fullscreen Mode", func() {
		state.fullscreen = !state.fullscreen
		w.SetFullScreen(state.fullscreen)
		if !state.fullscreen {
			w.Resize(fyne.NewSize(state.width, state.height))
		}
	})
	settingsMenu := fyne.NewMenu("Settings", fullscreen)

	aboutDialog := dialog.NewCustom(
		"About",
		"Close",
		widget.NewCard(
			"Photo Browser",
			"a native Go/Fyne image browser",
			widget.NewRichTextFromMarkdown(
				"Browse a folder as a thumbnail grid, click a thumbnail for "+
					"a full-size viewer with previous/next and keyboard navigation.",
			),
		),
		w,
	)
	aboutMenu := fyne.NewMenu("About", fyne.NewMenuItem("About", func() {
		aboutDialog.Show()
	}))

	return fyne.NewMainMenu(fileMenu, settingsMenu, aboutMenu)
}
