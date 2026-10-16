package ui

import (
	"context"
	"sort"
	"strings"

	invlib "inv-lib"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// maxTagSuggestions caps how many matches tagInput's suggestion list shows,
// so a database with thousands of tags never renders a huge list for a
// single keystroke.
const maxTagSuggestions = 8

// tagInput is the comma-separated tag field used throughout the app (the
// viewer's per-file tags and the bulk tag dialog's "add tags" field): a
// plain Entry with a list of existing tag names shown underneath it,
// filtered live by whichever tag is currently being typed — the substring
// after the last comma. Clicking a suggestion completes that one tag;
// typing something that matches none of them is used exactly as typed,
// same as a plain free-type entry, so adding a brand-new tag always works.
//
// The suggestions render as an ordinary (non-overlay) list beneath the
// entry rather than a dropdown popup: Fyne's popup widgets grab keyboard
// focus the moment they're shown (see widget.PopUpMenu.Show), which would
// yank focus away from the entry after every keystroke and make it
// impossible to keep typing. An inline list never takes focus, so typing
// is uninterrupted; Fyne's box layout collapses it to zero height whenever
// it's hidden, so it costs no space when there's nothing to suggest.
type tagInput struct {
	Entry       *widget.Entry
	suggestions *fyne.Container
	root        *fyne.Container
	allTags     []string
}

// newTagInput builds a tagInput whose suggestions come from every tag name
// currently in store. Loading the tag list is best-effort: if it fails (or
// store is nil), the field still works as a plain comma-separated entry —
// it just has no suggestions to offer.
func newTagInput(store *invlib.DB) *tagInput {
	e := &tagInput{
		Entry:       widget.NewEntry(),
		suggestions: container.NewVBox(),
	}
	e.Entry.SetPlaceHolder("comma, separated, tags")
	e.suggestions.Hide()
	e.root = container.NewVBox(e.Entry, e.suggestions)

	if store != nil {
		if tags, err := store.ListTags(context.Background()); err == nil {
			names := make([]string, len(tags))
			for i, t := range tags {
				names[i] = t.Name
			}
			sort.Strings(names)
			e.allTags = names
		}
	}

	e.Entry.OnChanged = func(string) { e.showSuggestions() }
	e.Entry.OnSubmitted = func(string) { e.hideSuggestions() }
	return e
}

// CanvasObject returns the entry plus its suggestion list, for embedding in
// a parent container in place of a plain *widget.Entry.
func (e *tagInput) CanvasObject() fyne.CanvasObject {
	return e.root
}

// Text returns the field's current comma-separated text.
func (e *tagInput) Text() string {
	return e.Entry.Text
}

// SetTextSilently sets the field's text without running the suggestion
// logic, for callers loading an existing value (e.g. the viewer switching
// records) rather than responding to the user typing. It also moves the
// cursor to the end of the new text — SetText on its own only clamps the
// existing cursor position to stay within bounds, it doesn't move it, so
// without this a cursor left mid-string from previous typing would cause
// the next character typed to land in the middle of the value instead of
// at the end.
func (e *tagInput) SetTextSilently(text string) {
	onChanged := e.Entry.OnChanged
	e.Entry.OnChanged = nil
	e.Entry.SetText(text)
	e.Entry.CursorRow = 0
	e.Entry.CursorColumn = len([]rune(text))
	e.Entry.OnChanged = onChanged
}

// showSuggestions rebuilds the suggestion list from every existing tag name
// matching the tag currently being typed, and hides it if there's no
// in-progress token or nothing matches.
func (e *tagInput) showSuggestions() {
	token := strings.ToLower(currentTagToken(e.Entry.Text))
	if token == "" {
		e.hideSuggestions()
		return
	}

	var matches []string
	for _, name := range e.allTags {
		if strings.Contains(strings.ToLower(name), token) {
			matches = append(matches, name)
			if len(matches) == maxTagSuggestions {
				break
			}
		}
	}
	if len(matches) == 0 {
		e.hideSuggestions()
		return
	}

	buttons := make([]fyne.CanvasObject, len(matches))
	for i, name := range matches {
		b := widget.NewButton(name, func() { e.applySuggestion(name) })
		b.Alignment = widget.ButtonAlignLeading
		buttons[i] = b
	}
	e.suggestions.Objects = buttons
	e.suggestions.Show()
	e.suggestions.Refresh()
}

// hideSuggestions collapses the suggestion list, if shown.
func (e *tagInput) hideSuggestions() {
	if len(e.suggestions.Objects) == 0 && !e.suggestions.Visible() {
		return
	}
	e.suggestions.Objects = nil
	e.suggestions.Hide()
	e.suggestions.Refresh()
}

// applySuggestion replaces the tag currently being typed — the substring
// after the last comma — with name, leaving any already-completed tags
// before it untouched, and returns focus to the entry (clicking the
// suggestion button would otherwise leave it unfocused) so the user can
// keep typing the next tag right away.
func (e *tagInput) applySuggestion(name string) {
	text := e.Entry.Text
	prefix := ""
	if idx := strings.LastIndex(text, ","); idx >= 0 {
		prefix = text[:idx+1] + " "
	}
	e.SetTextSilently(prefix + name)
	e.hideSuggestions()

	if c := fyne.CurrentApp().Driver().CanvasForObject(e.Entry); c != nil {
		c.Focus(e.Entry)
	}
}

// currentTagToken returns the tag currently being typed in a
// comma-separated tags field: the trimmed substring after the last comma
// (or the whole string, if there isn't one yet).
func currentTagToken(text string) string {
	token := text
	if idx := strings.LastIndex(text, ","); idx >= 0 {
		token = text[idx+1:]
	}
	return strings.TrimSpace(token)
}
