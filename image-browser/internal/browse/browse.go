// Package browse implements the image-browser browse command: an
// interactive terminal UI for browsing, filtering, selecting, and moving
// database file records, as a keyboard-driven alternative to the Fyne
// `view`/`size`/`move -preview` windows for a headless or SSH session.
package browse

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	invlib "inv-lib"

	"image-browser/internal/invdb"
	"image-browser/internal/paths"
)

// Run executes the browse command.
func Run(args []string) error {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "scope the browser to files under this directory (default: ~/Images)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser browse [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	// Bubble Tea needs to read raw input from stdin and write escape
	// sequences to stdout; a pipe on either side (CI logs, `| less`,
	// `< file`) would hang or spew garbage rather than do either.
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return errors.New("browse: requires an interactive terminal (stdin/stdout must not be redirected)")
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = paths.DefaultRoot()
	}
	rootPath, err := paths.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("browse: resolving root: %w", err)
	}

	dbPath, err := invlib.DefaultPath()
	if err != nil {
		return fmt.Errorf("browse: resolve inv.db path: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inv, err := invlib.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("browse: open inv.db: %w", err)
	}
	defer inv.Close()

	if removed, err := invdb.PruneMissing(ctx, inv); err != nil {
		return fmt.Errorf("browse: pruning missing files: %w", err)
	} else if removed > 0 {
		fmt.Printf("Removed %d database record(s) whose file no longer exists\n", removed)
	}

	p := tea.NewProgram(newModel(ctx, cancel, inv, rootPath))

	finalModel, err := p.Run()
	if err != nil {
		return fmt.Errorf("browse: %w", err)
	}

	if fm, ok := finalModel.(model); ok && fm.loadErr != nil {
		return fmt.Errorf("browse: %w", fm.loadErr)
	}

	return nil
}
