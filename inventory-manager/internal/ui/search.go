package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	invlib "inv-lib"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// discoveredAtLayout is the date format the Discovered From/To fields
// accept and the format origins.discovered_at is compared against —
// matching the same YYYY-MM-DD shape image-browser's own
// `move -discoveredat` flag already uses for the identical column.
const discoveredAtLayout = "2006-01-02"

// advancedSearchConfig configures showAdvancedSearchWindow.
type advancedSearchConfig struct {
	// dbPath is the filesystem path to the inv-lib database to open a
	// short-lived, read-only connection against when the SQL box is used.
	dbPath string

	// initial pre-fills the window's Tag/Site/Path fields — normally the
	// toolbar's current values, so the window picks up where the toolbar
	// left off instead of starting those three blank while the rest of
	// the window starts empty.
	initial Filter

	// onSearch is called with the Filter the window produced, once
	// Search succeeds (a validation or SQL error is shown inline in the
	// window instead, and onSearch is not called).
	onSearch func(Filter)

	// onClear is called after the window's own fields have all been
	// reset to blank, when Clear is clicked.
	onClear func()
}

// showAdvancedSearchWindow opens the Advanced Search window: every
// structured field Filter supports, or a raw SQL box that takes priority
// over all of them whenever it has text (the structured fields disable
// themselves as soon as the SQL box is non-empty, so it's never ambiguous
// which one is actually in effect). It is a real, independent, non-modal
// fyne.Window — the same pattern viewer.go already uses for the
// per-record viewer, not a modal dialog.PopUp like Manage Tags — so it
// stays open across repeated Search clicks and the main window remains
// fully usable alongside it.
func showAdvancedSearchWindow(cfg advancedSearchConfig) {
	win := fyne.CurrentApp().NewWindow("Advanced Search")

	tagEntry := widget.NewEntry()
	tagEntry.SetText(cfg.initial.Tag)
	siteEntry := widget.NewEntry()
	siteEntry.SetText(cfg.initial.Site)
	pathEntry := widget.NewEntry()
	pathEntry.SetText(cfg.initial.Path)

	urlEntry := widget.NewEntry()
	identifierEntry := widget.NewEntry()
	statusEntry := widget.NewEntry()
	hashEntry := widget.NewEntry()
	hashEntry.SetPlaceHolder("exact match")

	widthMinEntry := widget.NewEntry()
	widthMinEntry.SetPlaceHolder("min")
	widthMaxEntry := widget.NewEntry()
	widthMaxEntry.SetPlaceHolder("max")
	heightMinEntry := widget.NewEntry()
	heightMinEntry.SetPlaceHolder("min")
	heightMaxEntry := widget.NewEntry()
	heightMaxEntry.SetPlaceHolder("max")

	discoveredFromEntry := widget.NewEntry()
	discoveredFromEntry.SetPlaceHolder(discoveredAtLayout)
	discoveredToEntry := widget.NewEntry()
	discoveredToEntry.SetPlaceHolder(discoveredAtLayout)

	// structuredEntries is every field the SQL box's OnChanged below
	// disables/enables as a unit — the visual half of "SQL wins outright
	// if it has any text" (the other half is resolveStructuredFilter
	// never being called at all once sqlEntry has text; see the Search
	// button below).
	structuredEntries := []*widget.Entry{
		tagEntry, siteEntry, pathEntry, urlEntry, identifierEntry, statusEntry, hashEntry,
		widthMinEntry, widthMaxEntry, heightMinEntry, heightMaxEntry,
		discoveredFromEntry, discoveredToEntry,
	}
	setStructuredEnabled := func(enabled bool) {
		for _, e := range structuredEntries {
			if enabled {
				e.Enable()
			} else {
				e.Disable()
			}
		}
	}

	sqlEntry := widget.NewMultiLineEntry()
	sqlEntry.SetPlaceHolder("SELECT id FROM files WHERE ...")
	sqlEntry.Wrapping = fyne.TextWrapWord
	sqlEntry.SetMinRowsVisible(8)
	sqlEntry.OnChanged = func(text string) {
		setStructuredEnabled(strings.TrimSpace(text) == "")
	}

	errorLabel := widget.NewLabel("")
	errorLabel.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("Tag", tagEntry),
		widget.NewFormItem("Site", siteEntry),
		widget.NewFormItem("Path", pathEntry),
		widget.NewFormItem("URL", urlEntry),
		widget.NewFormItem("Identifier", identifierEntry),
		widget.NewFormItem("Status", statusEntry),
		widget.NewFormItem("Hash", hashEntry),
		widget.NewFormItem("Width min/max", container.NewGridWithColumns(2, widthMinEntry, widthMaxEntry)),
		widget.NewFormItem("Height min/max", container.NewGridWithColumns(2, heightMinEntry, heightMaxEntry)),
		widget.NewFormItem("Discovered from/to", container.NewGridWithColumns(2, discoveredFromEntry, discoveredToEntry)),
	)

	searchBtn := widget.NewButton("Search", func() {
		errorLabel.SetText("")

		rawSQL := strings.TrimSpace(sqlEntry.Text)
		if rawSQL != "" {
			filter, err := resolveSQLFilter(cfg.dbPath, rawSQL)
			if err != nil {
				errorLabel.SetText(err.Error())
				return
			}
			cfg.onSearch(filter)
			return
		}

		filter, err := resolveStructuredFilter(structuredInput{
			Tag:            tagEntry.Text,
			Site:           siteEntry.Text,
			Path:           pathEntry.Text,
			URL:            urlEntry.Text,
			Identifier:     identifierEntry.Text,
			Status:         statusEntry.Text,
			Hash:           hashEntry.Text,
			WidthMin:       widthMinEntry.Text,
			WidthMax:       widthMaxEntry.Text,
			HeightMin:      heightMinEntry.Text,
			HeightMax:      heightMaxEntry.Text,
			DiscoveredFrom: discoveredFromEntry.Text,
			DiscoveredTo:   discoveredToEntry.Text,
		})
		if err != nil {
			errorLabel.SetText(err.Error())
			return
		}
		cfg.onSearch(filter)
	})
	searchBtn.Importance = widget.HighImportance

	clearBtn := widget.NewButton("Clear", func() {
		for _, e := range structuredEntries {
			e.SetText("")
		}
		sqlEntry.SetText("") // re-enables structuredEntries via OnChanged
		errorLabel.SetText("")
		cfg.onClear()
	})

	buttons := container.NewHBox(searchBtn, clearBtn)

	body := container.NewVBox(
		form,
		widget.NewSeparator(),
		widget.NewLabel("Raw SQL (must return a file_id or id column; overrides every field above when non-empty):"),
		sqlEntry,
	)

	win.SetContent(container.NewBorder(
		nil,
		container.NewVBox(errorLabel, buttons),
		nil, nil,
		container.NewVScroll(body),
	))
	win.Resize(fyne.NewSize(520, 680))
	win.Show()
}

// structuredInput is the raw text of every structured field in the
// Advanced Search window, before parsing/validation.
type structuredInput struct {
	Tag, Site, Path               string
	URL, Identifier, Status, Hash string
	WidthMin, WidthMax            string
	HeightMin, HeightMax          string
	DiscoveredFrom, DiscoveredTo  string
}

// resolveStructuredFilter parses in's numeric and date fields and builds
// the Filter they describe. Every blank field means "unfiltered" for that
// field, matching Filter's own zero-value convention. Returns an error
// naming the offending field if a numeric or date field is non-blank but
// doesn't parse.
func resolveStructuredFilter(in structuredInput) (Filter, error) {
	widthMin, err := parseOptionalInt("Width min", in.WidthMin)
	if err != nil {
		return Filter{}, err
	}
	widthMax, err := parseOptionalInt("Width max", in.WidthMax)
	if err != nil {
		return Filter{}, err
	}
	heightMin, err := parseOptionalInt("Height min", in.HeightMin)
	if err != nil {
		return Filter{}, err
	}
	heightMax, err := parseOptionalInt("Height max", in.HeightMax)
	if err != nil {
		return Filter{}, err
	}
	discoveredFrom, err := parseOptionalDate("Discovered from", in.DiscoveredFrom)
	if err != nil {
		return Filter{}, err
	}
	discoveredTo, err := parseOptionalDate("Discovered to", in.DiscoveredTo)
	if err != nil {
		return Filter{}, err
	}

	return Filter{
		Tag:            strings.TrimSpace(in.Tag),
		Site:           strings.TrimSpace(in.Site),
		Path:           strings.TrimSpace(in.Path),
		URL:            strings.TrimSpace(in.URL),
		Identifier:     strings.TrimSpace(in.Identifier),
		Status:         strings.TrimSpace(in.Status),
		Hash:           strings.TrimSpace(in.Hash),
		WidthMin:       widthMin,
		WidthMax:       widthMax,
		HeightMin:      heightMin,
		HeightMax:      heightMax,
		DiscoveredFrom: discoveredFrom,
		DiscoveredTo:   discoveredTo,
	}, nil
}

// parseOptionalInt parses text as an int, or returns 0 if text is blank.
// label identifies the field in a returned error.
func parseOptionalInt(label, text string) (int, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%s: %q is not a whole number", label, text)
	}
	return n, nil
}

// parseOptionalDate parses text as discoveredAtLayout, or returns nil if
// text is blank. label identifies the field in a returned error.
func parseOptionalDate(label, text string) (*time.Time, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	t, err := time.Parse(discoveredAtLayout, text)
	if err != nil {
		return nil, fmt.Errorf("%s: %q is not a %s date", label, text, discoveredAtLayout)
	}
	return &t, nil
}

// resolveSQLFilter opens a short-lived, read-only connection to dbPath,
// runs rawSQL through invlib.QueryFileIDs, and closes the connection
// before returning — the window never holds a second connection open
// between searches. The returned Filter always has a non-nil FileIDs
// (even when empty), so it reliably means "restrict to exactly these
// results" to searchRecords regardless of how many rows rawSQL matched.
func resolveSQLFilter(dbPath, rawSQL string) (Filter, error) {
	ctx := context.Background()

	roDB, err := invlib.OpenReadOnly(ctx, dbPath)
	if err != nil {
		return Filter{}, fmt.Errorf("open database read-only: %w", err)
	}
	defer roDB.Close()

	ids, err := roDB.QueryFileIDs(ctx, rawSQL)
	if err != nil {
		return Filter{}, err
	}

	return Filter{FileIDs: ids}, nil
}
