// Command fddup finds and removes duplicate files using a scan-then-delete
// model: a scan never modifies files; it queues pending deletions in SQLite
// for a later `delete` invocation. See the README for the full picture.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	iofs "io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"fddup/internal/invdb"

	"fddup/internal/scan"

	"fddup/internal/queue"
)

const usage = `fddup - find and remove duplicate files

Usage:
  fddup scan   [flags] <path> [path...]   Scan trees, queue duplicates in DB
  fddup list   [flags]                    Show pending duplicates queued for deletion
  fddup delete [flags]                    Delete files queued by a previous scan
  fddup status [flags]                    Show queue counts by status
  fddup reset  [flags]                    Empty the queue (no files touched)
  fddup prune  [flags]                    Remove stale hash cache entries for missing files

Common flags:
  -db PATH      database path (default: ~/.fddup/queue.db)
  -inv-db PATH  optional: consult inv-lib's shared inventory database at PATH
                (e.g. ~/.config/inventory-manager/db/inv.db) for an
                already-known hash before hashing a file (scan), and remove
                a file's row there once fddup confirms it's gone (delete).
                Unset by default — fddup never touches inv.db on its own.

Run 'fddup <subcommand> -h' for subcommand-specific flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	// Single SIGINT/SIGTERM handler turns Ctrl-C into a context cancellation
	// rather than an abrupt exit. Stage workers and DB writes get a chance
	// to finish cleanly so the queue isn't left half-written.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "scan":
		err = cmdScan(ctx, args)
	case "list":
		err = cmdList(args)
	case "delete":
		err = cmdDelete(ctx, args)
	case "status":
		err = cmdStatus(args)
	case "reset":
		err = cmdReset(ctx, args)
	case "prune":
		err = cmdPrune(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func defaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine home directory: %w", err)
	}
	return filepath.Join(home, ".fddup", "queue.db"), nil
}

func resolveDB(path string) (*queue.DB, error) {
	if path == "" {
		var err error
		path, err = defaultDBPath()
		if err != nil {
			return nil, err
		}
	}
	return queue.Open(path)
}

// withDB resolves the database at dbPath (or the default path), runs fn,
// and closes the handle afterward regardless of fn's outcome. fn's error is
// returned as-is; wrap it with context (%w) before returning if fn doesn't
// already describe which operation failed.
func withDB(dbPath string, fn func(*queue.DB) error) error {
	q, err := resolveDB(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer q.Close()
	return fn(q)
}

// parseOrExit parses args with fs. The second return value is false when
// parsing requested -h/--help (already printed by fs.Usage); callers should
// treat that as "exit 0, do nothing more" rather than an error.
func parseOrExit(fs *flag.FlagSet, args []string) (run bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// confirm prompts the user to type "yes". It returns false if they decline,
// if reading stdin fails, or if ctx is cancelled while waiting for input.
//
// If ctx is cancelled first, the stdin-reading goroutine is left running
// until it gets input (or stdin closes) rather than being cleaned up — a
// deliberate, bounded leak since the process exits shortly after. Don't
// copy this pattern into a long-lived process without adding a real way to
// unblock the read (e.g. closing stdin or using a cancellable reader).
func confirm(ctx context.Context, prompt string) bool {
	fmt.Print(prompt)

	resp := make(chan string, 1)
	go func() {
		var s string
		if _, err := fmt.Scanln(&s); err != nil {
			s = ""
		}
		resp <- s
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "\ncancelled")
		return false
	case s := <-resp:
		return s == "yes"
	}
}

func cmdScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path (default: ~/.fddup/queue.db)")
	workers := fs.Int("workers", 4, "concurrent hash workers (1 for HDD, ncpu for SSD/NVMe)")
	invDBPath := fs.String("inv-db", "", "optional: consult this inv-lib database for an already-known hash before hashing a file")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: fddup scan [flags] <path> [path...]")
		fs.PrintDefaults()
	}
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}
	roots := fs.Args()
	if len(roots) == 0 {
		fs.Usage()
		return fmt.Errorf("at least one path is required")
	}
	limit := runtime.NumCPU() * 2
	switch {
	case *workers < 1:
		*workers = 1
	case *workers > limit:
		*workers = limit
	}

	cfg := scan.Config{Roots: roots, Workers: *workers}

	if *invDBPath != "" {
		store, err := invdb.Open(ctx, *invDBPath)
		if err != nil {
			return fmt.Errorf("open inv-db: %w", err)
		}
		defer store.Close()
		cfg.InvDBLookup = store.LookupHash
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		return scan.Run(ctx, q, cfg)
	})
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path")
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		rows, err := q.ListPending()
		if err != nil {
			return fmt.Errorf("list pending: %w", err)
		}
		if len(rows) == 0 {
			fmt.Println("Queue is empty.")
			return nil
		}
		var totalBytes int64
		for _, r := range rows {
			fmt.Printf("size=%-12d  %s\n", r.Size, r.Path)
			fmt.Printf("              keeper: %s\n\n", r.Keeper)
			totalBytes += r.Size
		}
		fmt.Printf("Total: %d pending file(s), %.2f MB\n",
			len(rows), float64(totalBytes)/(1<<20))
		return nil
	})
}

func cmdDelete(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	invDBPath := fs.String("inv-db", "", "optional: remove a deleted file's row from this inv-lib database too")
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}

	var invStore *invdb.Store
	if *invDBPath != "" {
		var err error
		invStore, err = invdb.Open(ctx, *invDBPath)
		if err != nil {
			return fmt.Errorf("open inv-db: %w", err)
		}
		defer invStore.Close()
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		rows, err := q.ListPending()
		if err != nil {
			return fmt.Errorf("list pending: %w", err)
		}
		if len(rows) == 0 {
			fmt.Println("Queue is empty. Nothing to delete.")
			return nil
		}

		var totalBytes int64
		for _, r := range rows {
			totalBytes += r.Size
		}
		fmt.Printf("About to delete %d file(s) totalling %.2f MB.\n",
			len(rows), float64(totalBytes)/(1<<20))

		if !*yes && !confirm(ctx, "Type 'yes' to proceed: ") {
			if err := ctx.Err(); err != nil {
				return err
			}
			fmt.Println("Aborted.")
			return nil
		}

		var deleted, missing, failed int
		var reclaimed int64
		for _, r := range rows {
			// Honor cancellation between rows so a Ctrl-C leaves the queue
			// in a state the next `delete` call can resume from.
			if err := ctx.Err(); err != nil {
				fmt.Fprintln(os.Stderr, "\ncancelled")
				break
			}

			removeErr := os.Remove(r.Path)
			switch {
			case removeErr == nil:
				deleted++
				reclaimed += r.Size
				if err := q.MarkProcessed(r.ID, queue.StatusDeleted, ""); err != nil {
					fmt.Fprintf(os.Stderr, "db update %s: %v\n", r.Path, err)
				}
				forgetInvDB(ctx, invStore, r.Path)
			case errors.Is(removeErr, iofs.ErrNotExist):
				missing++
				if err := q.MarkProcessed(r.ID, queue.StatusMissing, removeErr.Error()); err != nil {
					fmt.Fprintf(os.Stderr, "db update %s: %v\n", r.Path, err)
				}
				// Already gone, presumably by other tooling — still worth
				// clearing out of inv.db for the same reason as the
				// deleted case above.
				forgetInvDB(ctx, invStore, r.Path)
			default:
				failed++
				fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", r.Path, removeErr)
				if err := q.MarkProcessed(r.ID, queue.StatusFailed, removeErr.Error()); err != nil {
					fmt.Fprintf(os.Stderr, "db update %s: %v\n", r.Path, err)
				}
			}
		}

		fmt.Printf("\nDeleted: %d  Missing: %d  Failed: %d  Reclaimed: %.2f MB\n",
			deleted, missing, failed, float64(reclaimed)/(1<<20))
		if err := ctx.Err(); err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("delete completed with %d failure(s)", failed)
		}
		return nil
	})
}

// forgetInvDB removes path's row from inv.db, if -inv-db was given and a
// row for it exists. A failure here is logged and otherwise ignored —
// fddup's own queue already recorded the authoritative outcome for path;
// inv.db going briefly stale is exactly the gap image-browser's own
// reconcile pass (invdb.PruneMissing) exists to close.
func forgetInvDB(ctx context.Context, store *invdb.Store, path string) {
	if store == nil {
		return
	}
	if err := store.Forget(ctx, path); err != nil {
		fmt.Fprintf(os.Stderr, "inv-db forget %s: %v\n", path, err)
	}
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path")
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		counts, pendingBytes, err := q.Stats()
		if err != nil {
			return fmt.Errorf("get stats: %w", err)
		}
		if len(counts) == 0 {
			fmt.Println("Queue is empty.")
			return nil
		}
		statuses := []string{"pending", "deleted", "missing", "failed"}
		fmt.Printf("%-10s %-10s\n", "STATUS", "COUNT")
		for _, s := range statuses {
			if c, ok := counts[s]; ok {
				fmt.Printf("%-10s %-10d\n", s, c)
			}
		}
		if pendingBytes > 0 {
			fmt.Printf("\nPending reclaim: %.2f MB\n", float64(pendingBytes)/(1<<20))
		}
		return nil
	})
}

func cmdPrune(args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: fddup prune [flags]")
		fmt.Fprintln(fs.Output(), "Removes hash cache entries for files that no longer exist on disk.")
		fs.PrintDefaults()
	}
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		n, err := q.PruneCache()
		if err != nil {
			return fmt.Errorf("prune cache: %w", err)
		}
		if n == 0 {
			fmt.Println("Hash cache is clean — no stale entries found.")
		} else {
			fmt.Printf("Pruned %d stale cache entries.\n", n)
		}
		return nil
	})
}

func cmdReset(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("reset", flag.ContinueOnError)
	dbPath := fs.String("db", "", "database path")
	yes := fs.Bool("yes", false, "skip confirmation prompt")
	if run, err := parseOrExit(fs, args); err != nil {
		return err
	} else if !run {
		return nil
	}

	return withDB(*dbPath, func(q *queue.DB) error {
		if !*yes && !confirm(ctx, "This will clear the queue (no files deleted). Type 'yes' to proceed: ") {
			if err := ctx.Err(); err != nil {
				return err
			}
			fmt.Println("Aborted.")
			return nil
		}
		if err := q.Clear(); err != nil {
			return fmt.Errorf("clear queue: %w", err)
		}
		fmt.Println("Queue cleared.")
		return nil
	})
}
