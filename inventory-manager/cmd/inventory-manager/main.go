// Command inventory-manager opens a Fyne GUI for browsing image thumbnails
// in a directory, with a native menu bar and folder picker. Every directory
// it scans is also indexed into a local SQLite inventory database, which is
// the foundation for the inventory management and database features this
// application will grow.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"inventory-manager/internal/inventory"
	"inventory-manager/internal/ui"

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
	root := flag.String("root", ".", "directory to browse")
	flag.Parse()

	store, err := openStore()
	if err != nil {
		// The inventory database is groundwork for future features, not
		// something the browsing UI depends on today, so a failure to open
		// it degrades to "no indexing" rather than making the app unusable.
		log.Printf("inventory database unavailable, indexing will be skipped: %v", err)
	}
	if store != nil {
		defer store.Close()
	}

	a := app.NewWithID("com.local.inventorymanager")
	w := a.NewWindow("Inventory Manager")

	browser := ui.NewBrowser(*root)
	browser.SetWindow(w)
	browser.SetStore(store)
	w.SetContent(browser.Canvas())

	state := windowState{width: 1200, height: 850}
	w.Resize(fyne.NewSize(state.width, state.height))

	w.SetMainMenu(buildMainMenu(w, browser, &state))
	w.Show()

	go func() {
		if err := browser.LoadDirectory(context.Background(), *root, false); err != nil {
			log.Printf("load directory: %v", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("could not load %q: %w", *root, err), w)
			})
		}
	}()

	a.Run()
}

// openStore opens the SQLite inventory database under the user's config
// directory, creating the directory and database file on first run.
func openStore() (*inventory.Store, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user config dir: %w", err)
	}

	dir := filepath.Join(configDir, "inventory-manager")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create config dir %q: %w", dir, err)
	}

	return inventory.Open(filepath.Join(dir, "inventory.db"))
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
			"Inventory Manager",
			"a native Go/Fyne image browser and inventory database",
			widget.NewRichTextFromMarkdown(
				"Browse a folder as a thumbnail grid, click a thumbnail for "+
					"a full-size viewer with previous/next and keyboard "+
					"navigation. Every scanned image is indexed by content "+
					"hash into a local SQLite inventory database.",
			),
		),
		w,
	)
	aboutMenu := fyne.NewMenu("About", fyne.NewMenuItem("About", func() {
		aboutDialog.Show()
	}))

	return fyne.NewMainMenu(fileMenu, settingsMenu, aboutMenu)
}
