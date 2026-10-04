package main

import (
	"log/slog"

	"inv-db/internal/db"
	"inv-db/internal/store"
)

func main() {
	database, err := db.Open("inv.db")
	if err != nil {
		slog.Error("failed to open database", "error", err)
		return
	}

	store := store.New(database)

	if err := run(store); err != nil {
		slog.Error("error", "error", err)
	}
}

func run(store *store.Store) error {
	return nil
}
