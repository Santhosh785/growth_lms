// Package logging configures the application's structured logger.
package logging

import (
	"log/slog"
	"os"
)

// New builds a slog.Logger. Production always logs JSON; development may
// optionally use a human-readable handler for local convenience.
func New(humanFormat bool, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if humanFormat {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
