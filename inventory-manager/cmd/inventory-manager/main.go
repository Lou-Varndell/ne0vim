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
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"sync"

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

	// Cancelled from app.Lifecycle's OnStopped hook below, so the startup
	// search goroutine is told to give up as soon as the window closes
	// instead of racing the deferred store.Close() below it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, resolvedDBPath, err := openStore(ctx, *dbPath)
	if err != nil {
		log.Printf("inventory database unavailable: %v", err)
	}
	if store != nil {
		defer store.Close()
	}

	// Deferred after store.Close() above, so it runs first (defers are
	// LIFO): the startup search goroutine must finish before the store it
	// reads from is closed out from under it.
	var wg sync.WaitGroup
	defer wg.Wait()

	a := app.NewWithID("com.local.inventorymanager")
	a.Lifecycle().SetOnStopped(cancel)
	w := a.NewWindow("Inventory Manager")

	browser := ui.NewBrowser()
	browser.SetWindow(w)
	browser.SetStore(store)
	browser.SetDBPath(resolvedDBPath)
	w.SetContent(browser.Canvas())

	state := windowState{width: 1200, height: 850}
	w.Resize(fyne.NewSize(state.width, state.height))

	w.SetMainMenu(buildMainMenu(w, browser, &state))
	w.Show()

	if store == nil {
		dialog.ShowError(fmt.Errorf("inventory database unavailable: %w", err), w)
	} else {
		wg.Go(func() {
			err := browser.Search(ctx, ui.Filter{}, 0)
			if err == nil || errors.Is(err, context.Canceled) {
				return
			}
			log.Printf("load inventory: %v", err)
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("could not load inventory: %w", err), w)
			})
		})
	}

	a.Run()
}

// openStore opens the inv-lib inventory database at dbPath, or, if dbPath
// is empty, the shared database at invlib.DefaultPath() — the same file
// site-scraper and image-browser read and write, so an item indexed by any
// of the three tools shows up in all of them. Schema creation and
// migration are handled by invlib.Open itself. The resolved path is
// returned alongside the store (even on error, if resolution itself
// succeeded) so the caller can hand it to Browser.SetDBPath for the
// Advanced Search window's read-only connection — dbPath itself may be
// empty, but the actual file the store was opened against never is.
func openStore(ctx context.Context, dbPath string) (*invlib.DB, string, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		path, err := invlib.DefaultPath()
		if err != nil {
			return nil, "", fmt.Errorf("resolve default database path: %w", err)
		}
		dbPath = path
	}

	store, err := invlib.Open(ctx, dbPath)
	return store, dbPath, err
}

// buildMainMenu assembles the File, Settings, and About menus. File-menu
// actions that touch the database (Import, Manage Tags, Refresh) are safe
// to wire up unconditionally even when browser has no store: ui.Browser
// no-ops with a "no inventory database available" message in that case
// rather than panicking.
func buildMainMenu(w fyne.Window, browser *ui.Browser, state *windowState) *fyne.MainMenu {
	importFolder := fyne.NewMenuItem("Import Folder…", func() {
		browser.OpenImportDialog()
	})
	manageTags := fyne.NewMenuItem("Manage Tags…", func() {
		browser.OpenManageTagsDialog()
	})
	advancedSearch := fyne.NewMenuItem("Advanced Search…", func() {
		browser.OpenAdvancedSearchDialog()
	})
	refresh := fyne.NewMenuItem("Refresh", func() {
		browser.Refresh()
	})
	refresh.Shortcut = ui.RefreshShortcut()
	deselectAll := fyne.NewMenuItem("Deselect All", func() {
		browser.DeselectAll()
	})
	quit := fyne.NewMenuItem("Quit", w.Close)
	fileMenu := fyne.NewMenu("File", importFolder, manageTags, advancedSearch, refresh, deselectAll, fyne.NewMenuItemSeparator(), quit)

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
