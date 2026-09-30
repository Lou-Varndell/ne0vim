package viewer

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"image-browser/internal/filedialog"
	"image-browser/internal/images"
	"image-browser/internal/paths"
)

// ReviewAction is the result of closing a review window.
type ReviewAction int

const (
	// ReviewContinue closes the current preview and continues with the next
	// item in a review sequence.
	ReviewContinue ReviewAction = iota
	// ReviewQuit closes the current preview and terminates the review sequence.
	ReviewQuit
)

// Run executes the view command and starts the Fyne application.
func Run(args []string) error {
	fs := flag.NewFlagSet("view", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "root directory to search (default: ~/Images)")
	moveAll := fs.String("move-all", "", "enable Move All and move matches to destination")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser view [pattern] [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	// The standard flag package stops parsing when it encounters a positional
	// argument. Extract the single pattern first, while preserving flags and
	// their values for FlagSet to parse. This allows both
	//   view [pattern] --move-all /path/to/my-dest
	// and
	//   view --move-all /path/to/my-dest [pattern]
	// forms.
	parseArgs := make([]string, 0, len(args))
	var pattern string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			if pattern != "" {
				return errors.New("view: too many positional arguments")
			}
			pattern = arg
			continue
		}

		parseArgs = append(parseArgs, arg)
		if arg == "-root" || arg == "-move-all" {
			if i+1 >= len(args) {
				return fmt.Errorf("view: %s requires a value", arg)
			}
			i++
			parseArgs = append(parseArgs, args[i])
		}
	}

	if err := fs.Parse(parseArgs); err != nil {
		return err
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = paths.DefaultRoot()
	}

	rootPath, err := paths.Abs(rootPath)
	if err != nil {
		return err
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("not a directory: %s", rootPath)
	}

	if !info.IsDir() {
		return fmt.Errorf("not a directory: %s", rootPath)
	}

	// No pattern and no move-all destination means there is no set of search
	// matches to review — the user just wants to look around rootPath, so
	// switch to a plain folder browser (folder picker, no review toolbar)
	// instead of the search/review workflow below.
	if pattern == "" && *moveAll == "" {
		return RunBrowse(rootPath)
	}

	matches, err := images.Find(rootPath, pattern)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		fmt.Printf("No images found for %q under %s\n", pattern, rootPath)
		return nil
	}

	if *moveAll != "" {
		moveAllPath, err := resolveMoveAllDestination(*moveAll)
		if err != nil {
			return fmt.Errorf("view: resolving move-all destination: %w", err)
		}
		return RunImagesWithMoveAll(rootPath, pattern, matches, true, moveAllPath)
	}

	return RunImages(rootPath, pattern, matches, true)
}

// RunImages displays the supplied image paths in the image browser.
// allowTrash controls whether the browser's individual-file trash shortcuts
// are enabled.
func RunImages(root, pattern string, matches []string, allowTrash bool) error {
	return runImages(root, pattern, matches, allowTrash, "")
}

// RunImagesWithMoveAll displays the supplied image paths and enables the
// optional Move All review control for the supplied destination.
func RunImagesWithMoveAll(root, pattern string, matches []string, allowTrash bool, moveAllDest string) error {
	return runImages(root, pattern, matches, allowTrash, moveAllDest)
}

func runImages(root, pattern string, matches []string, allowTrash bool, moveAllDest string) error {
	session := NewSession()
	session.ShowImagesWithMoveAll(root, pattern, matches, allowTrash, nil, ReviewContinue, moveAllDest)

	// Run must block the calling goroutine because Fyne's event loop owns it.
	// Wait for the user to close the window on a separate goroutine, then quit
	// the application through the Fyne event loop.
	go func() {
		session.WaitAction()
		session.Quit()
	}()

	session.Run()
	return nil
}

// RunBrowse opens a plain folder browser at root: a Choose Directory /
// Refresh toolbar lets the user navigate, with no search-and-review workflow
// attached. It is used when `view` is given no pattern (and no move-all
// destination).
func RunBrowse(root string) error {
	session := NewSession()
	session.ShowBrowse(root)

	// Same shutdown handshake as runImages: closing the window (or any other
	// path that calls finish()) unblocks WaitAction, and this goroutine then
	// quits the app through the Fyne event loop.
	go func() {
		session.WaitAction()
		session.Quit()
	}()

	session.Run()
	return nil
}

// Session owns one Fyne application and window that can be reused for
// multiple image batches. Fyne applications have a single run loop, so a
// session is required when a command needs to show several previews in
// sequence.
type Session struct {
	app fyne.App
	win fyne.Window

	mu     sync.Mutex
	action chan ReviewAction

	fullscreen  bool
	restoreSize fyne.Size

	// browseChoose is set by loadBrowse to the session's current Choose
	// Directory action, and is nil otherwise. The File > Open Folder menu
	// item uses it so that clicking it during a review-loop command (e.g.
	// `size`, which reuses one Session across several ShowImages batches
	// and decides whether to continue based on WaitAction's result) is a
	// safe no-op instead of swapping the window's content out from under
	// that loop.
	browseChoose func()
}

// NewSession creates a reusable image-browser session.
func NewSession() *Session {
	a := app.NewWithID("com.example.image-browser")
	// a := app.NewWithID("image-browser-" + uuid.New().String()[:8])
	win := a.NewWindow("Image Browser")
	size := fyne.NewSize(2880, 1864)
	win.Resize(size)

	s := &Session{
		app:         a,
		win:         win,
		action:      make(chan ReviewAction, 1),
		restoreSize: size,
	}

	win.SetCloseIntercept(func() {
		s.finish(ReviewContinue)
	})
	win.SetMainMenu(s.buildMainMenu())

	return s
}

// buildMainMenu constructs the window's native menu bar: File (Open Folder /
// Quit), Settings (Fullscreen Mode), and About.
func (s *Session) buildMainMenu() *fyne.MainMenu {
	openFolder := fyne.NewMenuItem("Open Folder", func() {
		s.mu.Lock()
		choose := s.browseChoose
		s.mu.Unlock()
		if choose != nil {
			choose()
		}
	})
	quit := fyne.NewMenuItem("Quit", s.win.Close)
	fileMenu := fyne.NewMenu("File", openFolder, fyne.NewMenuItemSeparator(), quit)

	fullscreen := fyne.NewMenuItem("Fullscreen Mode", func() {
		s.fullscreen = !s.fullscreen
		s.win.SetFullScreen(s.fullscreen)
		if !s.fullscreen {
			s.win.Resize(s.restoreSize)
		}
	})
	settingsMenu := fyne.NewMenu("Settings", fullscreen)

	aboutDialog := dialog.NewCustom(
		"About",
		"Close",
		widget.NewCard(
			"Image Browser",
			"browse, search, and organize images",
			widget.NewRichTextFromMarkdown(
				"Browse a folder as a thumbnail grid — recursively, with a "+
					"folder picker — or search a directory tree by pattern "+
					"and review, trash, or move the matches. Click a "+
					"thumbnail for a full-size viewer with previous/next "+
					"and keyboard navigation.",
			),
		),
		s.win,
	)
	aboutMenu := fyne.NewMenu("About", fyne.NewMenuItem("About", func() {
		aboutDialog.Show()
	}))

	return fyne.NewMainMenu(fileMenu, settingsMenu, aboutMenu)
}

// ShowImages replaces the window contents with a new batch and shows it.
// trashFunc, when non-nil, is used by the review toolbar's Trash Batch
// button. All Fyne work is scheduled on the Fyne event loop.
func (s *Session) ShowImages(root, pattern string, matches []string, allowTrash bool, trashFunc func() error, continueAction ReviewAction) {
	s.ShowImagesWithMoveAll(root, pattern, matches, allowTrash, trashFunc, continueAction, "")
}

// ShowImagesWithMoveAll is like ShowImages but optionally enables the Move All
// control when moveAllDest is non-empty. A successful Move All ends the current
// viewer session; failures leave the viewer open so the user can inspect the
// result.
func (s *Session) ShowImagesWithMoveAll(root, pattern string, matches []string, allowTrash bool, trashFunc func() error, continueAction ReviewAction, moveAllDest string) {
	shown := make(chan struct{})

	s.mu.Lock()
	s.action = make(chan ReviewAction, 1)
	s.mu.Unlock()

	fyne.Do(func() {
		browser := New(s.app, s.win, root, pattern, matches, true, true)
		browser.SetTrashEnabled(allowTrash)

		if !allowTrash {
			trashFunc = nil
		}

		browser.SetReviewControls(
			func() { s.finish(continueAction) },
			func() { s.finish(ReviewQuit) },
			trashFunc,
		)
		if moveAllDest != "" {
			browser.SetMoveAll(moveAllDest, func() { s.finish(ReviewQuit) })
		}
		browser.Show()
		s.win.Show()
		close(shown)
	})

	<-shown
}

// ShowBrowse displays a plain, non-recursive listing of root's images. A
// Choose Directory / Refresh / Preview subdirectories toolbar replaces the
// review toolbar used by search-driven commands, since browsing a folder has
// no batch of matches to review, move, or trash as a group.
func (s *Session) ShowBrowse(root string) {
	shown := make(chan struct{})

	s.mu.Lock()
	s.action = make(chan ReviewAction, 1)
	s.mu.Unlock()

	fyne.Do(func() {
		// This is the only content the window has before the first
		// loadBrowse's scan finishes; every later loadBrowse call (Choose
		// Directory, Refresh, toggling preview) leaves whatever Browser is
		// currently shown in place until it has a result, so a slow scan or
		// a load error never leaves the window stuck on a placeholder with
		// no way to recover.
		s.win.SetContent(widget.NewLabel(fmt.Sprintf("Loading %s…", root)))
		s.win.Show()
		close(shown)
	})

	<-shown

	s.loadBrowse(root, false)
}

// loadBrowse scans root and replaces the window's content with a fresh
// Browser for the result. With preview false, it lists only the images
// directly inside root (a flat grid). With preview true, it recursively
// walks root's whole subtree and groups the result by subdirectory (the
// same grouped, drill-in view search commands use for their matches — see
// Browser.groupedPaths and openDirectoryPreview), letting the user survey
// every subdirectory with images before opening one. The scan runs on a
// background goroutine so a slow or huge directory tree can't freeze the
// window; the result is applied back on the Fyne event loop, and the
// window's content is only ever touched once that result (success or
// error) is known.
func (s *Session) loadBrowse(root string, preview bool) {
	go func() {
		var paths []string
		var err error

		if preview {
			paths, err = images.Find(root, "")
		} else {
			var items []images.Item
			items, err = images.Scan(context.Background(), root)
			if err == nil {
				paths = make([]string, len(items))
				for i, item := range items {
					paths[i] = item.Path
				}
			}
		}

		fyne.Do(func() {
			if err != nil {
				dialog.ShowError(fmt.Errorf("could not load %q: %w", root, err), s.win)
				return
			}

			chooseDirectory := func() { s.chooseBrowseDirectory(root, preview) }
			s.mu.Lock()
			s.browseChoose = chooseDirectory
			s.mu.Unlock()

			browser := New(s.app, s.win, root, "", paths, true, preview)
			browser.SetBrowseControls(
				root,
				chooseDirectory,
				func(path string) { s.loadBrowse(path, preview) },
				preview,
				func(next bool) { s.loadBrowse(root, next) },
			)
			browser.Show()
		})
	}()
}

// chooseBrowseDirectory opens the folder picker anchored at current and
// loads whatever directory the user selects, preserving the current
// Preview subdirectories setting.
func (s *Session) chooseBrowseDirectory(current string, preview bool) {
	d := filedialog.NewFolderOpen(func(lu fyne.ListableURI, err error) {
		if err != nil || lu == nil {
			return
		}
		s.loadBrowse(lu.Path(), preview)
	}, s.win)

	if listable, err := storage.ListerForURI(storage.NewFileURI(current)); err == nil {
		d.SetLocation(listable)
	}
	d.Resize(fyne.NewSize(900, 600))
	d.Show()
}

// Run starts the session's Fyne event loop. It must only be called once and
// must be called from the main goroutine.
func (s *Session) Run() {
	s.app.Run()
}

// WaitAction waits until the current preview is closed or the user chooses
// one of the review controls.
func (s *Session) WaitAction() ReviewAction {
	s.mu.Lock()
	action := s.action
	s.mu.Unlock()

	return <-action
}

func (s *Session) finish(action ReviewAction) {
	s.mu.Lock()
	actionCh := s.action
	s.mu.Unlock()

	select {
	case actionCh <- action:
	default:
		return
	}

	// The close intercept is invoked on Fyne's event loop, as are the review
	// button callbacks, so these operations are safe here.
	s.win.Hide()

	if action == ReviewQuit {
		s.app.Quit()
	}
}

// Quit stops the Fyne application. It is safe to call from a non-Fyne
// goroutine; the actual quit is marshalled through fyne.Do.
func (s *Session) Quit() {
	fyne.Do(func() {
		s.app.Quit()
	})
}
