package watcher

import (
	"io"
	"log/slog"
)

func noopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
