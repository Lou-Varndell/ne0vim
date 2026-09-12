package viewer

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"

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

	matches, err := images.Find(rootPath, pattern)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		fmt.Printf("No images found for %q under %s\n", pattern, rootPath)
		return nil
	}

	if *moveAll != "" {
		moveAllPath, err := paths.Abs(*moveAll)
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

// Session owns one Fyne application and window that can be reused for
// multiple image batches. Fyne applications have a single run loop, so a
// session is required when a command needs to show several previews in
// sequence.
type Session struct {
	app fyne.App
	win fyne.Window

	mu     sync.Mutex
	action chan ReviewAction
}

// NewSession creates a reusable image-browser session.
func NewSession() *Session {
	a := app.NewWithID("com.example.image-browser")
	win := a.NewWindow("Image Browser")
	win.Resize(fyne.NewSize(1000, 700))

	s := &Session{
		app:    a,
		win:    win,
		action: make(chan ReviewAction, 1),
	}

	win.SetCloseIntercept(func() {
		s.finish(ReviewContinue)
	})

	return s
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
