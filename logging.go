package main

import (
	"log/slog"
	"os"
)

// setupLogger initializes the structured logger
func setupLogger() {
	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
		// AddSource: true, // Uncomment to see file/line numbers in logs (optional)
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)
}
