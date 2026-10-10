// Command inventory-manager opens a Fyne GUI for searching and browsing an
// image inventory database by metadata — tag and source site — displaying
// matches as a thumbnail grid with a native menu bar. File > Import Folder
// scans a directory tree and indexes its images (by content hash) into the
// database; everything else reads from, edits, or deletes from the
// database, never the filesystem. The database itself (schema, migration,
// CRUD) is owned by the inv-lib library, not this package.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"inventory-manager/internal/ui"

	invlib "inv-lib"

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
	dbPath := flag.String("db", "", "path to the inventory database (default: OS config dir)")
	flag.Parse()

	ctx := context.Background()

	store, err := openStore(ctx, *dbPath)
	if err != nil {
		log.Printf("inventory database unavailable: %v", err)
	}
	if store != nil {
		defer store.Close()
	}

	a := app.NewWithID("com.local.inventorymanager")
	w := a.NewWindow("Inventory Manager")

	browser := ui.NewBrowser()
	browser.SetWindow(w)
	browser.SetStore(store)
	w.SetContent(browser.Canvas())

	state := windowState{width: 1200, height: 850}
	w.Resize(fyne.NewSize(state.width, state.height))

	w.SetMainMenu(buildMainMenu(w, browser, &state))
	w.Show()

	if store == nil {
		dialog.ShowError(fmt.Errorf("inventory database unavailable: %w", err), w)
	} else {
		go func() {
			if err := browser.Search(ctx, ui.Filter{}); err != nil {
				log.Printf("load inventory: %v", err)
				fyne.Do(func() {
					dialog.ShowError(fmt.Errorf("could not load inventory: %w", err), w)
				})
			}
		}()
	}

	a.Run()
}

// openStore opens the inv-lib inventory database at dbPath, or, if dbPath
// is empty, the shared database at invlib.DefaultPath() — the same file
// site-scraper and image-browser read and write, so an item indexed by any
// of the three tools shows up in all of them. Schema creation and
// migration are handled by invlib.Open itself.
func openStore(ctx context.Context, dbPath string) (*invlib.DB, error) {
	if dbPath == "" {
		path, err := invlib.DefaultPath()
		if err != nil {
			return nil, fmt.Errorf("resolve default database path: %w", err)
		}
		dbPath = path
	}

	return invlib.Open(ctx, dbPath)
}

func buildMainMenu(w fyne.Window, browser *ui.Browser, state *windowState) *fyne.MainMenu {
	importFolder := fyne.NewMenuItem("Import Folder…", func() {
		browser.OpenImportDialog()
	})
	manageTags := fyne.NewMenuItem("Manage Tags…", func() {
		browser.OpenManageTagsDialog()
	})
	quit := fyne.NewMenuItem("Quit", w.Close)
	fileMenu := fyne.NewMenu("File", importFolder, manageTags, fyne.NewMenuItemSeparator(), quit)

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
			"a native Go/Fyne image inventory browser and database",
			widget.NewRichTextFromMarkdown(
				"Search the inventory database by tag or source site, browse "+
					"matches as a thumbnail grid, and click a thumbnail to open "+
					"a full-size viewer where you can edit metadata or delete "+
					"the record. Use File > Import Folder to scan a directory "+
					"and index its images, by content hash, into the database.",
			),
		),
		w,
	)
	aboutMenu := fyne.NewMenu("About", fyne.NewMenuItem("About", func() {
		aboutDialog.Show()
	}))

	return fyne.NewMainMenu(fileMenu, settingsMenu, aboutMenu)
}
