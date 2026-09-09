package filedialog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
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
	data             []fyne.URI
	selected         fyne.URI
	selectedID       int
	search           string
	searchAt         time.Time
	clickAt          time.Time
	showHidden       bool
	goToShortcut     fyne.Shortcut
	hiddenShortcut   fyne.Shortcut
}

const typeAheadWindow = time.Second

func NewFileOpen(callback func(fyne.URIReadCloser, error), parent fyne.Window) *FileDialog {
	return &FileDialog{callback: callback, parent: parent}
}

func NewFolderOpen(callback func(fyne.ListableURI, error), parent fyne.Window) *FileDialog {
	return &FileDialog{folderCB: callback, folderMode: true, parent: parent}
}

func (f *FileDialog) SetFilter(filter storage.FileFilter) {
	if f.folderMode {
		return
	}
	f.filter = filter
	if f.location != nil {
		f.refresh()
	}
}

func (f *FileDialog) SetLocation(location fyne.ListableURI) {
	f.location = location
	if f.pop != nil {
		f.setLocation(location)
	}
}

func (f *FileDialog) Resize(size fyne.Size) {
	f.desiredSize = size
	if f.pop != nil {
		f.pop.Resize(size)
	}
}

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
	f.goToShortcut = &desktop.CustomShortcut{
		KeyName:  fyne.KeyG,
		Modifier: fyne.KeyModifierSuper | fyne.KeyModifierShift,
	}
	f.pop.Canvas.AddShortcut(f.goToShortcut, func(_ fyne.Shortcut) {
		f.showGoToPath()
	})

	// Cmd+Shift+. toggles hidden files and folders, matching the macOS
	// Finder shortcut. Hidden entries are hidden by default.
	f.hiddenShortcut = &desktop.CustomShortcut{
		KeyName:  fyne.KeyPeriod,
		Modifier: fyne.KeyModifierSuper | fyne.KeyModifierShift,
	}
	f.pop.Canvas.AddShortcut(f.hiddenShortcut, func(_ fyne.Shortcut) {
		f.showHidden = !f.showHidden
		f.refresh()
	})

	start := f.location
	if start == nil {
		start = homeLocation()
	}
	f.setLocation(start)
	f.pop.Show()
	f.focusList()
}

func (f *FileDialog) focusList() {
	if f.pop != nil && f.list != nil {
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
		return
	}
	f.location = list
	f.selected = nil
	f.selectedID = -1
	f.clickAt = time.Time{}
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

func (f *FileDialog) refresh() {
	if f.location == nil {
		return
	}
	entries, err := f.location.List()
	if err != nil {
		return
	}

	var parent fyne.URI
	parent, _ = storage.Parent(f.location)

	// Build a completely new data slice before replacing the list. Fyne's
	// List is a pooled widget; reusing the same backing slice while changing
	// directories can leave a previously pooled row displaying old data.
	// Rebuilding the small picker list also guarantees that row IDs and URIs
	// belong to the same directory view.
	data := make([]fyne.URI, 0, len(entries)+1)
	if parent != nil && parent.String() != f.location.String() {
		data = append(data, parent)
	}

	for _, uri := range entries {
		if !f.showHidden && isHiddenURI(uri) {
			continue
		}

		listable, listErr := storage.ListerForURI(uri)
		if f.folderMode {
			if listErr == nil {
				data = append(data, listable)
			}
			continue
		}
		if listErr == nil || f.filter == nil || f.filter.Matches(uri) {
			data = append(data, uri)
		}
	}

	parentOffset := 0
	if parent != nil && len(data) > 0 && data[0].String() == parent.String() {
		parentOffset = 1
	}
	sort.Slice(data[parentOffset:], func(i, j int) bool {
		return strings.ToLower(data[parentOffset+i].Name()) < strings.ToLower(data[parentOffset+j].Name())
	})

	f.data = data
	f.rebuildList()
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
		row := obj.(*fyne.Container)
		icon := row.Objects[0].(*widget.FileIcon)
		label := row.Objects[1].(*widget.Label)
		uri := f.data[id]
		icon.SetURI(uri)
		label.SetText(uri.Name())
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

	uri := f.data[id]
	_, listErr := storage.ListerForURI(uri)
	isDir := listErr == nil

	if f.folderMode {
		// Folder navigation is immediate, matching the normal file-picker
		// workflow: clicking a directory enters it. The current directory
		// remains the selection, so Open chooses the directory currently shown.
		if isDir {
			if listable, err := storage.ListerForURI(uri); err == nil {
				f.setLocation(listable)
			}
		}
		return
	}

	if isDir {
		// File mode keeps the normal file-picker behavior: selecting a
		// directory navigates into it immediately.
		if listable, err := storage.ListerForURI(uri); err == nil {
			f.setLocation(listable)
		}
		return
	}

	f.selected = uri
	f.selectedID = id
	f.fileName.SetText(uri.Name())
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
	for i, uri := range f.data {
		if !strings.HasPrefix(strings.ToLower(uri.Name()), prefix) {
			continue
		}
		_, err := storage.ListerForURI(uri)
		isDir := err == nil
		if directoriesOnly && !isDir {
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
