package filedialog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"image-browser/internal/images"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

// FileDialog is a small application-local file/folder picker based on Fyne
// widgets. It intentionally keeps the public API close to Fyne's FileDialog
// for easy replacement in the application.
type FileDialog struct {
	parent           fyne.Window
	callback         func(fyne.URIReadCloser, error)
	folderCB         func(fyne.ListableURI, error)
	folderMode       bool
	filter           storage.FileFilter
	location         fyne.ListableURI
	desiredSize      fyne.Size
	pop              *widget.PopUp
	list             *typeAheadList
	listHost         *fyne.Container
	breadcrumb       *fyne.Container
	breadcrumbScroll *container.Scroll
	fileName         *widget.Label
	open             *widget.Button
	cancel           *widget.Button
	data             []dirEntry
	selected         fyne.URI
	search           string
	searchAt         time.Time
	showHidden       bool
	loadGeneration   int
}

const typeAheadWindow = time.Second

// NewFileOpen creates a file-picker dialog that invokes callback with the
// selected file when the user confirms, or with a nil reader if they cancel.
func NewFileOpen(callback func(fyne.URIReadCloser, error), parent fyne.Window) *FileDialog {
	return &FileDialog{callback: callback, parent: parent}
}

// NewFolderOpen creates a folder-picker dialog that invokes callback with the
// selected directory when the user confirms, or with a nil URI if they cancel.
func NewFolderOpen(callback func(fyne.ListableURI, error), parent fyne.Window) *FileDialog {
	return &FileDialog{folderCB: callback, folderMode: true, parent: parent}
}

// SetFilter restricts the files shown to those matching filter. It has no
// effect in folder mode.
func (f *FileDialog) SetFilter(filter storage.FileFilter) {
	if f.folderMode {
		return
	}
	f.filter = filter
	if f.location != nil {
		f.refresh()
	}
}

// SetLocation sets the directory the dialog will open in.
func (f *FileDialog) SetLocation(location fyne.ListableURI) {
	f.location = location
	if f.pop != nil {
		f.setLocation(location)
	}
}

// Resize sets the size of the dialog window.
func (f *FileDialog) Resize(size fyne.Size) {
	f.desiredSize = size
	if f.pop != nil {
		f.pop.Resize(size)
	}
}

// Show displays the dialog, building it on first use and re-showing it on
// subsequent calls.
func (f *FileDialog) Show() {
	if f.pop != nil {
		f.pop.Show()
		f.focusList()
		return
	}

	f.breadcrumb = container.NewHBox()
	f.breadcrumbScroll = container.NewHScroll(container.NewPadded(f.breadcrumb))
	f.fileName = widget.NewLabel("")

	f.open = widget.NewButton("Open", f.openSelected)
	f.open.Importance = widget.HighImportance
	f.open.Disable()
	f.cancel = widget.NewButton("Cancel", f.cancelDialog)

	f.listHost = container.NewMax()
	f.rebuildList()

	body := container.NewBorder(
		f.breadcrumbScroll,
		container.NewBorder(nil, nil, nil, container.NewHBox(f.cancel, f.open), f.fileName),
		nil,
		nil,
		f.listHost,
	)

	title := "Open File"
	if f.folderMode {
		title = "Open Folder"
	}
	header := widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	content := container.NewBorder(header, nil, nil, nil, body)

	f.pop = widget.NewModalPopUp(content, f.parent.Canvas())
	if f.desiredSize.IsZero() {
		f.pop.Resize(fyne.NewSize(900, 600))
	} else {
		f.pop.Resize(f.desiredSize)
	}

	// Cmd+Shift+G on macOS (and Super+Shift+G on desktop platforms).
	goToShortcut := &desktop.CustomShortcut{
		KeyName:  fyne.KeyG,
		Modifier: fyne.KeyModifierSuper | fyne.KeyModifierShift,
	}
	f.pop.Canvas.AddShortcut(goToShortcut, func(_ fyne.Shortcut) {
		f.showGoToPath()
	})

	// Cmd+Shift+. toggles hidden files and folders, matching the macOS
	// Finder shortcut. Hidden entries are hidden by default.
	hiddenShortcut := &desktop.CustomShortcut{
		KeyName:  fyne.KeyPeriod,
		Modifier: fyne.KeyModifierSuper | fyne.KeyModifierShift,
	}
	f.pop.Canvas.AddShortcut(hiddenShortcut, func(_ fyne.Shortcut) {
		f.showHidden = !f.showHidden
		f.refresh()
	})

	start := f.location
	if start == nil {
		start = homeLocation()
	}
	f.setLocation(start)
	f.pop.Show()
}

func (f *FileDialog) focusList() {
	// f.pop.Visible() guards against an async refresh() completing after the
	// dialog has already been cancelled/confirmed and its popup hidden.
	if f.pop != nil && f.pop.Visible() && f.list != nil {
		f.pop.Canvas.Focus(f.list)
	}
}

func (f *FileDialog) cancelDialog() {
	if f.pop != nil {
		f.pop.Hide()
	}
	if f.folderMode {
		if f.folderCB != nil {
			f.folderCB(nil, nil)
		}
	} else if f.callback != nil {
		f.callback(nil, nil)
	}
}

func (f *FileDialog) openSelected() {
	if f.folderMode {
		if f.folderCB == nil {
			return
		}
		selected, ok := f.selected.(fyne.ListableURI)
		if !ok || selected == nil {
			return
		}
		f.pop.Hide()
		f.folderCB(selected, nil)
		return
	}
	if f.selected == nil || f.callback == nil {
		return
	}
	reader, err := storage.Reader(f.selected)
	f.pop.Hide()
	f.callback(reader, err)
}

func (f *FileDialog) setLocation(location fyne.URI) {
	if location == nil {
		return
	}
	list, err := storage.ListerForURI(location)
	if err != nil {
		dialog.ShowError(fmt.Errorf("cannot open folder %q: %w", location.Name(), err), f.parent)
		return
	}
	f.location = list
	f.selected = nil
	f.open.Disable()
	f.fileName.SetText("")
	if f.folderMode {
		// The current directory itself is always a valid folder selection.
		f.selected = list
		f.fileName.SetText(list.Name())
		f.open.Enable()
	}

	f.breadcrumb.Objects = nil
	for current := fyne.URI(list); current != nil; {
		name := current.Name()
		uri := current
		f.breadcrumb.Add(widget.NewButton(name, func() { f.setLocation(uri) }))
		parent, err := storage.Parent(current)
		if err != nil || parent == nil || parent.String() == current.String() {
			break
		}
		current = parent
	}
	for i, j := 0, len(f.breadcrumb.Objects)-1; i < j; i, j = i+1, j-1 {
		f.breadcrumb.Objects[i], f.breadcrumb.Objects[j] = f.breadcrumb.Objects[j], f.breadcrumb.Objects[i]
	}
	f.breadcrumb.Refresh()
	f.refresh()
}

// refresh lists the current directory and rebuilds the picker's list.
// Listing runs on a background goroutine so a large directory (or a slow
// filesystem) can't freeze the window; loadGeneration lets a later refresh
// (e.g. the user clicking into another folder before this one finishes)
// discard a stale in-flight result instead of overwriting newer data.
func (f *FileDialog) refresh() {
	if f.location == nil {
		return
	}
	location := f.location
	folderMode := f.folderMode
	showHidden := f.showHidden
	filter := f.filter

	f.loadGeneration++
	generation := f.loadGeneration
	f.showLoading()

	go func() {
		entries, err := location.List()
		if err != nil {
			fyne.Do(func() {
				if generation != f.loadGeneration {
					return
				}
				dialog.ShowError(fmt.Errorf("cannot read folder %q: %w", location.Name(), err), f.parent)
			})
			return
		}

		data := buildDirectoryData(location, entries, folderMode, showHidden, filter)

		fyne.Do(func() {
			if generation != f.loadGeneration {
				return
			}
			f.data = data
			f.rebuildList()
			f.focusList()
		})
	}()
}

// showLoading displays a placeholder in the list area while a directory
// listing is in flight.
func (f *FileDialog) showLoading() {
	if f.listHost == nil {
		return
	}
	f.listHost.Objects = []fyne.CanvasObject{widget.NewLabel("Loading...")}
	f.listHost.Refresh()
}

// dirEntry pairs a listing entry with the directory-ness (and, if it is a
// directory, the fyne.ListableURI needed to navigate into it) determined
// once while building the listing, so callers like handleSelection and
// findTypeAhead don't need to re-probe the filesystem per keystroke/click.
type dirEntry struct {
	uri        fyne.URI
	isDir      bool
	listable   fyne.ListableURI // non-nil when isDir is true
	imageCount int              // number of images directly inside; only set in folder mode
}

// buildDirectoryData filters and sorts a directory listing. It touches no
// FileDialog state, so it is safe to call from the background goroutine in
// refresh() while the UI thread keeps responding.
func buildDirectoryData(location fyne.URI, entries []fyne.URI, folderMode, showHidden bool, filter storage.FileFilter) []dirEntry {
	// Parent lookup only fails at the filesystem root, which has no parent
	// entry to add, so a lookup error is equivalent to a nil parent here.
	parent, _ := storage.Parent(location)

	// Build a completely new data slice before replacing the list. Fyne's
	// List is a pooled widget; reusing the same backing slice while changing
	// directories can leave a previously pooled row displaying old data.
	// Rebuilding the small picker list also guarantees that row IDs and URIs
	// belong to the same directory view.
	data := make([]dirEntry, 0, len(entries)+1)
	if parent != nil && parent.String() != location.String() {
		if parentListable, err := storage.ListerForURI(parent); err == nil {
			entry := dirEntry{uri: parent, isDir: true, listable: parentListable}
			if folderMode {
				entry.imageCount = countImages(parentListable)
			}
			data = append(data, entry)
		}
	}

	for _, uri := range entries {
		if !showHidden && isHiddenURI(uri) {
			continue
		}

		listable, listErr := storage.ListerForURI(uri)
		isDir := listErr == nil
		if folderMode {
			if isDir {
				data = append(data, dirEntry{uri: listable, isDir: true, listable: listable, imageCount: countImages(listable)})
			}
			continue
		}
		if isDir || filter == nil || filter.Matches(uri) {
			data = append(data, dirEntry{uri: uri, isDir: isDir, listable: listable})
		}
	}

	parentOffset := 0
	if parent != nil && len(data) > 0 && data[0].uri.String() == parent.String() {
		parentOffset = 1
	}

	// Sort by a precomputed lowercase key rather than recomputing
	// strings.ToLower on every comparison: sort.Slice's less func would
	// otherwise call it O(n log n) times, which is real cost on 10K+ entry
	// directories. Sorting (entry, key) pairs together (instead of a
	// parallel keys slice) avoids desyncing the key from its entry, since
	// sort.Slice's Swap only swaps whatever slice it's given.
	type keyedEntry struct {
		entry dirEntry
		key   string
	}
	rest := data[parentOffset:]
	keyed := make([]keyedEntry, len(rest))
	for i, e := range rest {
		keyed[i] = keyedEntry{entry: e, key: strings.ToLower(e.uri.Name())}
	}
	sort.Slice(keyed, func(i, j int) bool {
		return keyed[i].key < keyed[j].key
	})
	for i, k := range keyed {
		rest[i] = k.entry
	}

	return data
}

// countImages returns the number of images directly inside u, or 0 if the
// count can't be determined (e.g. permission denied) — such folders simply
// show no count rather than blocking the listing on an error.
func countImages(u fyne.URI) int {
	count, err := images.CountImages(u.Path())
	if err != nil {
		return 0
	}
	return count
}

func (f *FileDialog) rebuildList() {
	if f.listHost == nil {
		return
	}

	f.list = newTypeAheadList(
		func(r rune) { f.typeAhead(string(r)) },
		func(k *fyne.KeyEvent) { f.handleListKey(k) },
	)
	f.list.Length = func() int { return len(f.data) }
	f.list.CreateItem = func() fyne.CanvasObject {
		return container.NewHBox(
			widget.NewFileIcon(storage.NewFileURI(".")),
			widget.NewLabel(""),
		)
	}
	f.list.UpdateItem = func(id widget.ListItemID, obj fyne.CanvasObject) {
		if int(id) < 0 || int(id) >= len(f.data) {
			return
		}
		row, ok := obj.(*fyne.Container)
		if !ok || len(row.Objects) < 2 {
			return
		}
		icon, ok := row.Objects[0].(*widget.FileIcon)
		if !ok {
			return
		}
		label, ok := row.Objects[1].(*widget.Label)
		if !ok {
			return
		}
		entry := f.data[id]
		icon.SetURI(entry.uri)
		name := entry.uri.Name()
		if f.folderMode {
			name = fmt.Sprintf("%s  (%d images)", name, entry.imageCount)
		}
		label.SetText(name)
	}
	f.list.OnSelected = func(id widget.ListItemID) {
		f.handleSelection(int(id))
	}

	f.listHost.Objects = []fyne.CanvasObject{f.list}
	f.listHost.Refresh()
	f.list.ScrollToTop()
}

func isHiddenURI(uri fyne.URI) bool {
	if uri == nil {
		return false
	}
	name := uri.Name()
	return name != "" && name != "." && name != ".." && strings.HasPrefix(name, ".")
}

func (f *FileDialog) handleSelection(id int) {
	if id < 0 || id >= len(f.data) {
		return
	}

	entry := f.data[id]

	if f.folderMode {
		// Folder navigation is immediate, matching the normal file-picker
		// workflow: clicking a directory enters it. The current directory
		// remains the selection, so Open chooses the directory currently shown.
		if entry.isDir {
			f.setLocation(entry.listable)
		}
		return
	}

	if entry.isDir {
		// File mode keeps the normal file-picker behavior: selecting a
		// directory navigates into it immediately.
		f.setLocation(entry.listable)
		return
	}

	f.selected = entry.uri
	f.fileName.SetText(entry.uri.Name())
	f.open.Enable()
}

func (f *FileDialog) typeAhead(r string) {
	if f.list == nil || f.location == nil {
		return
	}
	if time.Since(f.searchAt) > typeAheadWindow {
		f.search = ""
	}
	f.searchAt = time.Now()
	f.search += strings.ToLower(string(r))

	if i := f.findTypeAhead(f.search, true); i >= 0 {
		f.list.Select(widget.ListItemID(i))
		f.list.ScrollTo(widget.ListItemID(i))
		return
	}

	// If the accumulated prefix does not match, try the newest character.
	f.search = strings.ToLower(string(r))
	if i := f.findTypeAhead(f.search, true); i >= 0 {
		f.list.Select(widget.ListItemID(i))
		f.list.ScrollTo(widget.ListItemID(i))
		return
	}
	if i := f.findTypeAhead(f.search, false); i >= 0 {
		f.list.Select(widget.ListItemID(i))
		f.list.ScrollTo(widget.ListItemID(i))
	}
}

func (f *FileDialog) findTypeAhead(prefix string, directoriesOnly bool) int {
	for i, entry := range f.data {
		if !strings.HasPrefix(strings.ToLower(entry.uri.Name()), prefix) {
			continue
		}
		if directoriesOnly && !entry.isDir {
			continue
		}
		return i
	}
	return -1
}

func (f *FileDialog) handleListKey(k *fyne.KeyEvent) {
	if k == nil {
		return
	}
	if k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter {
		if f.selected != nil {
			f.openSelected()
		}
	}
}

func (f *FileDialog) showGoToPath() {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("/Users/boomer/Images/...")

	content := container.NewVBox(
		widget.NewLabel("Go to folder:"),
		entry,
	)

	cancel := widget.NewButton("Cancel", nil)
	goButton := widget.NewButton("Go", nil)
	goButton.Importance = widget.HighImportance

	var pop *widget.PopUp
	close := func() {
		if pop != nil {
			pop.Hide()
		}
		f.focusList()
	}
	cancel.OnTapped = close
	goButton.OnTapped = func() {
		path := strings.TrimSpace(entry.Text)
		if path == "" {
			return
		}
		path = expandHome(path)
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			entry.SetValidationError(fmt.Errorf("folder does not exist"))
			return
		}
		list, err := storage.ListerForURI(storage.NewFileURI(path))
		if err != nil {
			entry.SetValidationError(err)
			return
		}
		close()
		f.setLocation(list)
		f.focusList()
	}
	entry.OnSubmitted = func(string) { goButton.OnTapped() }

	buttons := container.NewHBox(cancel, goButton)
	box := container.NewBorder(nil, buttons, nil, nil, content)
	pop = widget.NewModalPopUp(box, f.pop.Canvas)
	pop.Resize(fyne.NewSize(600, 170))
	pop.Show()
	pop.Canvas.Focus(entry)
}

func expandHome(path string) string {
	if path == "~" {
		// Ignored: if the home directory can't be determined, home is "",
		// and the caller's os.Stat("") fails with a clear validation error
		// rather than this function needing to report it separately.
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return filepath.Clean(path)
}

func homeLocation() fyne.ListableURI {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	// Ignored: a resolvable home directory is not guaranteed to be listable
	// (e.g. permissions); Show() already treats a nil starting location as
	// "open with nothing selected" rather than an error.
	list, _ := storage.ListerForURI(storage.NewFileURI(home))
	return list
}

type typeAheadList struct {
	*widget.List
	onRune func(rune)
	onKey  func(*fyne.KeyEvent)
}

func newTypeAheadList(onRune func(rune), onKey func(*fyne.KeyEvent)) *typeAheadList {
	l := &typeAheadList{onRune: onRune, onKey: onKey}
	l.List = widget.NewList(
		func() int { return 0 },
		func() fyne.CanvasObject {
			return container.NewHBox(
				widget.NewFileIcon(storage.NewFileURI(".")),
				widget.NewLabel(""),
			)
		},
		func(widget.ListItemID, fyne.CanvasObject) {},
	)
	return l
}

func (l *typeAheadList) TypedRune(r rune) {
	if l.onRune != nil {
		l.onRune(r)
	}
}

func (l *typeAheadList) TypedKey(k *fyne.KeyEvent) {
	if k != nil && (k.Name == fyne.KeyReturn || k.Name == fyne.KeyEnter) {
		if l.onKey != nil {
			l.onKey(k)
		}
		return
	}
	l.List.TypedKey(k)
}
