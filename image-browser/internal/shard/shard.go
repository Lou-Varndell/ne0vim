// Package shard implements the image-browser shard command: it moves every
// database-tracked file that doesn't already live inside one of a root
// directory's shard subdirectories into one (via invlib.Sharder), keeping
// the database's path column in sync with each move the same way
// move.PerformMoves does for the move command.
package shard

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	invlib "inv-lib"

	"image-browser/internal/images"
	"image-browser/internal/invdb"
	"image-browser/internal/paths"
)

// Run executes the shard command.
func Run(args []string) error {
	fs := flag.NewFlagSet("shard", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	root := fs.String("root", "", "sharded root directory to distribute files under (default: ~/Images)")
	capacity := fs.Int("capacity", invlib.DefaultShardCapacity, "files placed in -root's first shard directory before starting a new one (each later shard doubles the one before it)")
	apply := fs.Bool("apply", false, "move files and update the database (default: report only)")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: image-browser shard [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Moves every database-tracked file that isn't already directly inside one of")
		fmt.Fprintln(os.Stderr, "-root's shard directories into one, updating the database's path column to")
		fmt.Fprintln(os.Stderr, "match. Report-only by default; pass -apply to actually move files.")
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
	if fs.NArg() != 0 {
		return errors.New("shard: unexpected positional arguments")
	}
	if *capacity <= 0 {
		return errors.New("shard: -capacity must be a positive integer")
	}

	rootPath := *root
	if rootPath == "" {
		rootPath = paths.DefaultRoot()
	}
	rootPath, err := paths.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("shard: resolving root: %w", err)
	}

	dbPath, err := invlib.DefaultPath()
	if err != nil {
		return fmt.Errorf("shard: resolve inv.db path: %w", err)
	}
	ctx := context.Background()
	inv, err := invlib.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("shard: open inv.db: %w", err)
	}
	defer inv.Close()

	if removed, err := invdb.PruneMissing(ctx, inv); err != nil {
		return fmt.Errorf("shard: pruning missing files: %w", err)
	} else if removed > 0 {
		fmt.Printf("Removed %d database record(s) whose file no longer exists\n", removed)
	}

	all, err := inv.ListFiles(ctx)
	if err != nil {
		return fmt.Errorf("shard: listing files: %w", err)
	}

	var toMove []*invlib.File
	alreadySharded := 0
	for _, f := range all {
		if invlib.IsSharded(rootPath, f.Path) {
			alreadySharded++
			continue
		}
		toMove = append(toMove, f)
	}

	fmt.Printf("%d file(s) already sharded under %s\n", alreadySharded, rootPath)
	if len(toMove) == 0 {
		fmt.Println("Nothing to shard.")
		return nil
	}

	if !*apply {
		fmt.Printf("%d file(s) would be sharded under %s (pass -apply to move them):\n", len(toMove), rootPath)
		for _, f := range toMove {
			fmt.Printf("  %s\n", f.Path)
		}
		return nil
	}

	return applyShard(ctx, inv, toMove, rootPath, *capacity)
}

// applyShard moves every file in toMove into a shard directory under
// rootPath (creating rootPath if necessary) and updates its database row's
// path in place, reporting a summary. Mirrors move.PerformMoves' per-file
// handling (duplicate detection, move, database update) but allocates each
// destination directory from a Sharder instead of a single fixed one.
func applyShard(ctx context.Context, inv *invlib.DB, toMove []*invlib.File, rootPath string, capacity int) error {
	sharder, err := invlib.NewSharder(rootPath, capacity)
	if err != nil {
		return fmt.Errorf("shard: %w", err)
	}

	var moved, duplicates, failures int
	for _, f := range toMove {
		if _, err := os.Stat(f.Path); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %s: %v\n", f.Path, err)
			failures++
			continue
		}

		dir, err := sharder.Dir()
		if err != nil {
			return fmt.Errorf("shard: %w", err)
		}

		destPath, duplicate, err := images.ResolveDestination(f.Path, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  error: resolving destination for %s: %v\n", f.Path, err)
			failures++
			continue
		}
		if duplicate {
			fmt.Printf("  duplicate (left in place): %s\n", f.Path)
			duplicates++
			continue
		}

		if err := images.Move(f.Path, destPath); err != nil {
			fmt.Fprintf(os.Stderr, "  error: moving %s: %v\n", f.Path, err)
			failures++
			continue
		}
		sharder.Advance()

		if err := inv.UpdateFilePath(ctx, f.ID, destPath, filepath.Base(destPath)); err != nil {
			fmt.Fprintf(os.Stderr, "  error: updating database for %s: %v\n", destPath, err)
			failures++
			continue
		}

		fmt.Printf("  %s -> %s\n", f.Path, destPath)
		moved++
	}

	fmt.Printf("\nSharded %d; duplicates %d; failed %d (of %d to shard)\n", moved, duplicates, failures, len(toMove))
	if failures > 0 {
		return fmt.Errorf("shard: %d file(s) failed to shard", failures)
	}
	return nil
}
