package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"page-fetcher/internal/handlers"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	// Static files
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// Routes
	h := handlers.New(logger)
	r.Get("/", h.Home)
	r.Post("/", h.Home)

	logger.Info("server starting", "addr", ":8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		logger.Error("server failed", "err", err)
		os.Exit(1)
	}
}
