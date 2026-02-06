//go:build fswatchdog
// +build fswatchdog

package main

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Global variables are in config.go

func main() {
	setupLogger()
	slog.Info("Starting FS Watchdog Service...")

	loadConfig()

	// Start the watchdog loop in a goroutine (it blocks)
	// Pass nil for bot since this is the standalone binary
	go RunFSWatchdog(nil)

	slog.Info("FS Watchdog started successfully")

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down FS Watchdog...")
}
