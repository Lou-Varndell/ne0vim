package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"inv-db/db"
	"inv-db/internal/finder"
	"inv-db/internal/hash"
	"inv-db/store"
)

// imagePatterns lists the glob patterns doWork scans for. Case variants
// are listed explicitly since filepath.Match is case-sensitive.
var imagePatterns = []string{"*.jpg", "*.JPG", "*.jpeg", "*.JPEG", "*.png", "*.PNG"}

func main() {
	os.Exit(run())
}

func run() int {
	dbPath := flag.String("db", "inv.db", "path to the SQLite database file")
	rootDir := flag.String("root", "/Users/louisvarndell-local/Images", "directory to scan for media files")
	flag.Parse()

	database, err := db.Open(*dbPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		return 1
	}
	defer database.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := store.New(database)

	if err := doWork(ctx, s, *rootDir); err != nil {
		slog.Error("doWork failed", "error", err)
		return 1
	}
	return 0
}

func doWork(ctx context.Context, s *store.Store, root string) error {
	paths, err := finder.Find(root, imagePatterns)
	if err != nil {
		return fmt.Errorf("find files: %w", err)
	}

	entries := make([]store.File, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}

		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("stat %q: %w", path, err)
		}

		digest, err := hash.BLAKE3(path)
		if err != nil {
			return fmt.Errorf("hash %q: %w", path, err)
		}

		entries = append(entries, store.File{
			Path:       path,
			Filename:   filepath.Base(path),
			Extension:  filepath.Ext(path),
			Filesize:   info.Size(),
			BLAKE3:     sql.NullString{String: digest, Valid: true},
			ModifiedAt: sql.NullTime{Time: info.ModTime(), Valid: true},
		})
	}

	if err := s.AddFiles(ctx, entries); err != nil {
		return fmt.Errorf("add files: %w", err)
	}

	return nil
}
