//go:build fswatchdog
// +build fswatchdog

package main

import "log/slog"

// Stub for isQuietHours - Watchdog always alerts
func isQuietHours() bool {
	return false
}

// Stub for addReportEvent - Log to stdout instead
func addReportEvent(eventType, message string) {
	slog.Info("Report Event", "type", eventType, "msg", message)
}
