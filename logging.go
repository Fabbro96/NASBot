package main

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	persistentLogFile *os.File
	persistentLogPath string
	loggingMu         sync.Mutex
)

func defaultPersistentLogPath() string {
	logPath := os.Getenv("NASBOT_LOG_FILE")
	if logPath == "" {
		logPath = "nasbot.log"
	}
	return logPath
}

// setupLogger initializes the structured logger
func setupLogger() {
	loggingMu.Lock()
	defer loggingMu.Unlock()
	setupLoggerLocked(true)
}

func setupLoggerLocked(announce bool) {
	if persistentLogFile != nil {
		_ = persistentLogFile.Sync()
		_ = persistentLogFile.Close()
		persistentLogFile = nil
	}

	logPath := defaultPersistentLogPath()
	persistentLogPath = logPath

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logFile = nil
	}

	opts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
		// AddSource: true, // Uncomment to see file/line numbers in logs (optional)
	}

	var out io.Writer = os.Stdout
	if logFile != nil {
		persistentLogFile = logFile
		out = io.MultiWriter(os.Stdout, logFile)
	}

	handler := slog.NewTextHandler(out, opts)
	logger := slog.New(handler).With("app", "nasbot")
	slog.SetDefault(logger)

	if announce {
		if logFile != nil {
			slog.Info("Persistent logging enabled", "file", logPath)
		} else {
			slog.Error("Persistent logging disabled: failed to open log file", "file", logPath)
		}
	}
}

func closeLogger() {
	loggingMu.Lock()
	defer loggingMu.Unlock()
	closeLoggerLocked()
}

func closeLoggerLocked() {
	if persistentLogFile == nil {
		return
	}
	_ = persistentLogFile.Sync()
	_ = persistentLogFile.Close()
	persistentLogFile = nil
}

func prunePersistentLogsAfterReport(ctx *AppContext) {
	retention := reportLogRetentionDuration(ctx)
	if retention <= 0 {
		return
	}
	if err := prunePersistentLogsOlderThan(retention); err != nil {
		slog.Error("Failed to prune persistent logs", "err", err, "retention", retention.String())
		return
	}
	slog.Info("Persistent logs pruned", "retention", retention.String())
}

func reportLogRetentionDuration(ctx *AppContext) time.Duration {
	if ctx == nil {
		return 48 * time.Hour
	}

	ctx.Settings.mu.RLock()
	mode := ctx.Settings.ReportMode
	ctx.Settings.mu.RUnlock()

	switch mode {
	case 2:
		return 24 * time.Hour
	case 1:
		return 48 * time.Hour
	default:
		return 48 * time.Hour
	}
}

func prunePersistentLogsOlderThan(retention time.Duration) error {
	cutoff := time.Now().Add(-retention)

	loggingMu.Lock()
	defer loggingMu.Unlock()

	logPath := persistentLogPath
	if logPath == "" {
		logPath = defaultPersistentLogPath()
		persistentLogPath = logPath
	}

	closeLoggerLocked()
	defer setupLoggerLocked(false)

	in, err := os.Open(logPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer in.Close()

	tmpPath := logPath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)
	writer := bufio.NewWriter(out)

	for scanner.Scan() {
		line := scanner.Text()
		if ts, ok := extractLogTimestamp(line); ok && ts.Before(cutoff) {
			continue
		}
		if _, err := writer.WriteString(line + "\n"); err != nil {
			_ = out.Close()
			_ = os.Remove(tmpPath)
			return err
		}
	}

	if err := scanner.Err(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := writer.Flush(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmpPath)
		return err
	}

	if err := out.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, logPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

func extractLogTimestamp(line string) (time.Time, bool) {
	idx := strings.Index(line, "time=")
	if idx < 0 {
		return time.Time{}, false
	}

	valuePart := line[idx+len("time="):]
	if valuePart == "" {
		return time.Time{}, false
	}

	var raw string
	if valuePart[0] == '"' {
		end := strings.IndexByte(valuePart[1:], '"')
		if end < 0 {
			return time.Time{}, false
		}
		raw = valuePart[1 : end+1]
	} else {
		end := strings.IndexByte(valuePart, ' ')
		if end < 0 {
			raw = valuePart
		} else {
			raw = valuePart[:end]
		}
	}

	ts, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}

	return ts, true
}
