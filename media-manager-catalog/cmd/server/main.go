// Command server runs the media-manager catalog browser: an HTTP server for
// browsing a directory of images one folder at a time.
package main

import (
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"media-manager/internal/catalog"
	"media-manager/web/static"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	imageDirFlag := flag.String("image-dir", "", "directory containing images to catalog (default: $HOME/Images)")
	flag.Parse()

	if err := run(logger, *imageDirFlag); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}

// run wires up and serves the app until it receives a shutdown signal, or
// returns an error if setup, serving, or shutdown fails. Every exit path is
// a plain return, so deferred cleanup (signal.Stop, the shutdown context's
// cancel) always runs — only main calls os.Exit, and only once.
func run(logger *slog.Logger, imageDirFlag string) error {
	imageDir, err := resolveImageDir(imageDirFlag)
	if err != nil {
		return fmt.Errorf("resolve image directory: %w", err)
	}

	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return fmt.Errorf("create image directory %s: %w", imageDir, err)
	}

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           newRouter(logger, imageDir),
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Bind synchronously so a busy port fails fast, before we ever log
	// "server starting" or start waiting on shutdown signals.
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("bind listener on %s: %w", srv.Addr, err)
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", "addr", srv.Addr, "imageDir", imageDir)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-stop:
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger.Info("server shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	return nil
}

// resolveImageDir returns imageDirFlag if non-empty, otherwise $HOME/Images.
func resolveImageDir(imageDirFlag string) (string, error) {
	if imageDirFlag != "" {
		return imageDirFlag, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, "Images"), nil
}

// newRouter wires the embedded static assets, the configured image
// directory, and the catalog routes onto a fresh chi router.
func newRouter(logger *slog.Logger, imageDir string) chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	getAndHead(r, "/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(static.FS))))
	getAndHead(r, "/images/*", http.StripPrefix("/images/", imageFileServer(imageDir)))

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/catalog", http.StatusFound)
	})

	catalog.Mount(r, logger, imageDir)

	return r
}

// getAndHead registers h for both GET and HEAD on pattern — file-serving
// handlers have no business responding to anything else.
func getAndHead(r chi.Router, pattern string, h http.Handler) {
	r.Get(pattern, h.ServeHTTP)
	r.Head(pattern, h.ServeHTTP)
}

// imageFileServer serves individual files under imageDir but refuses to
// render directory listings: browsing directories is the catalog page's job
// (breadcrumbs, one level at a time), not this raw file server's.
func imageFileServer(imageDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The leading "/" mirrors the jail in internal/catalog's
		// resolveDir, neutralizing any ".." segments before they reach
		// the filesystem.
		rel := filepath.FromSlash(path.Clean("/" + r.URL.Path))
		fullPath := filepath.Join(imageDir, rel)

		// Open once and stat that same handle, rather than stat-then-open,
		// so there's no window for the filesystem to change between the
		// directory check and serving the file.
		f, err := os.Open(fullPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}

		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}
