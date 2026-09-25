// Package applog sets up the CLI's JSON logger and keeps its log file from
// growing without bound across repeated runs.
package applog

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Setup rotates path out of the way if it has grown past maxBytes, opens it
// for append, and returns a JSON slog.Logger that writes to both stdout and
// path. The caller owns closing the returned file.
func Setup(path string, maxBytes int64) (*slog.Logger, *os.File, error) {
	if err := rotateIfLarge(path, maxBytes); err != nil {
		return nil, nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, nil, err
	}

	f, err := os.OpenFile(
		path,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return nil, nil, err
	}

	mw := io.MultiWriter(os.Stdout, f)

	logger := slog.New(slog.NewJSONHandler(mw, &slog.HandlerOptions{
		AddSource: true,
	}))

	return logger, f, nil
}

// rotateIfLarge renames path out of the way if it's grown past maxBytes, so
// repeated runs don't append to one unbounded log file forever.
func rotateIfLarge(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	if info.Size() < maxBytes {
		return nil
	}

	return os.Rename(
		path,
		path+"."+time.Now().Format("20060102-150405.000000000"),
	)
}
