package viewer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"image-browser/internal/filedialog"
	"image-browser/internal/paths"
)

// resolveMoveAllDestination trims, expands a leading "~", and resolves input
// to an absolute path for use as a Move All destination.
//
// It deliberately does not touch the filesystem beyond a single read-only
// stat: moveAllImages' own os.MkdirAll creates the destination lazily the
// first time a move actually runs, so this only rejects paths that are
// certain to fail regardless — an existing path that isn't a directory.
// Anything else (permissions, deeper collisions) surfaces later through
// moveAllImages' existing failure reporting, exactly as it already does for
// a bad CLI-supplied -move-all value.
func resolveMoveAllDestination(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", errors.New("destination is required")
	}

	resolved, err := paths.Abs(trimmed)
	if err != nil {
		return "", err
	}

	if info, err := os.Stat(resolved); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}

	return resolved, nil
}

// nearestExistingDir walks up from path until it finds a directory that
// exists, falling back to the filesystem root if none of path's ancestors
// do. It lets the Browse helper inside changeMoveAllDestination's dialog
// anchor on a partially-typed, not-yet-existing path.
func nearestExistingDir(path string) string {
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return path
		}
		path = parent
	}
}

// changeMoveAllDestination opens a dialog that lets the user redirect Move
// All to a different destination mid-session — including one that doesn't
// exist yet. Confirming the dialog never touches disk itself; see
// resolveMoveAllDestination.
func (b *Browser) changeMoveAllDestination() {
	entry := widget.NewEntry()
	entry.SetText(b.moveAllDest)

	var pop *widget.PopUp
	close := func() {
		if pop != nil {
			pop.Hide()
		}
	}

	browse := widget.NewButton("Browse...", func() {
		start := nearestExistingDir(entry.Text)

		d := filedialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
			if err != nil || lu == nil {
				return
			}
			// Only fills the field — the user still has to confirm this
			// dialog, so they can append a new subfolder name to whatever
			// they just browsed to.
			entry.SetText(lu.Path())
		}, b.win)

		if listable, err := storage.ListerForURI(storage.NewFileURI(start)); err == nil {
			d.SetLocation(listable)
		}
		d.Resize(fyne.NewSize(900, 600))
		d.Show()
	})

	cancel := widget.NewButton("Cancel", close)
	ok := widget.NewButton("OK", nil)
	ok.Importance = widget.HighImportance

	confirm := func() {
		resolved, err := resolveMoveAllDestination(entry.Text)
		if err != nil {
			entry.SetValidationError(err)
			return
		}

		close()
		b.moveAllDest = resolved

		if info, err := os.Stat(resolved); err == nil && info.IsDir() {
			b.status.SetText(fmt.Sprintf("Move All destination set to:\n%s", resolved))
		} else {
			b.status.SetText(fmt.Sprintf(
				"Move All destination set to:\n%s\n(will be created when you click Move All)",
				resolved,
			))
		}
	}
	ok.OnTapped = confirm
	entry.OnSubmitted = func(string) { confirm() }

	content := container.NewVBox(
		widget.NewLabel("Move All destination:"),
		entry,
		browse,
	)
	buttons := container.NewHBox(cancel, ok)
	box := container.NewBorder(nil, buttons, nil, nil, content)

	pop = widget.NewModalPopUp(box, b.win.Canvas())
	pop.Resize(fyne.NewSize(600, 220))
	pop.Show()
	b.win.Canvas().Focus(entry)
}
