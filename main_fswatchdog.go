package main

import (
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// Global variables are in config.go

func runFSWatchdogService() {
	setupLogger()
	defer closeLogger()
	slog.Info("Starting FS Watchdog Service...")

	loadConfig()

	// Start the watchdog loop in a goroutine (it blocks)
	// Pass nil for bot since this is the standalone binary
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic recovered in goroutine", "goroutine", "fswatchdog-loop", "err", r, "stack", string(debug.Stack()))
			}
		}()
		RunFSWatchdog(nil)
	}()

	slog.Info("FS Watchdog started successfully")

	// Wait for termination signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down FS Watchdog...")
}
