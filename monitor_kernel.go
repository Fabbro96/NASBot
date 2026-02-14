package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Regex to extract process name from OOM logs
// e.g. "Out of memory: Killed process 123 (python3)..."
// e.g. "oom_reaper: reaped process 123 (python3)..."
var reOOMProcess = regexp.MustCompile(`(?:Killed process|reaped process) \d+ \((.+?)\)`)

// OOM Loop thresholds
const (
	oomLoopWindow    = 30 * time.Minute
	oomLoopThreshold = 5
)

// kernelEventType defines a class of critical kernel events
type kernelEventType struct {
	Name     string
	TrKey    string
	Keywords []string
}

var kernelEventTypes = []kernelEventType{
	{
		Name:  "OOM",
		TrKey: "oom_alert",
		Keywords: []string{
			"out of memory",
			"oom-kill",
			"oom kill",
			"killed process",
			"memory cgroup out of memory",
			"oom_reaper",
		},
	},
	{
		Name:  "KernelPanic",
		TrKey: "kernel_panic",
		Keywords: []string{
			"kernel panic",
			"kernel bug",
			"bug: unable to handle",
			"oops:",
			"general protection fault",
			"rcu_sched self-detected stall",
		},
	},
	{
		Name:  "FSReadOnly",
		TrKey: "fs_readonly",
		Keywords: []string{
			"remounting filesystem read-only",
			"remount read-only",
			"ext4_abort",
			"abort (dev",
			"forcing read-only",
		},
	},
	{
		Name:  "IOError",
		TrKey: "io_error",
		Keywords: []string{
			"i/o error",
			"buffer i/o error",
			"blk_update_request: i/o error",
			"ata error",
			"medium error",
			"end_request: i/o error",
		},
	},
	{
		Name:  "HungTask",
		TrKey: "hung_task",
		Keywords: []string{
			"info: task .* blocked for more than",
			"hung_task_timeout",
		},
	},
}

// ═══════════════════════════════════════════════════════════════════
//  KERNEL WATCHDOG — OOM, panic, I/O errors, read-only FS, hung tasks
//  ALWAYS notifies (ignores quiet hours) — these are critical events
// ═══════════════════════════════════════════════════════════════════

func checkKernelEvents(ctx *AppContext, bot BotAPI) {
	lines := getKernelLogLines()
	if len(lines) == 0 {
		return
	}

	processKernelLines(ctx, bot, lines)
}

func processKernelLines(ctx *AppContext, bot BotAPI, lines []string) {
	ctx.Monitor.mu.Lock()
	if ctx.Monitor.KwLastSignatures == nil {
		ctx.Monitor.KwLastSignatures = make(map[string]string)
	}
	ctx.Monitor.mu.Unlock()

	for _, evt := range kernelEventTypes {
		lastIdx := -1
		for i, line := range lines {
			low := strings.ToLower(line)
			for _, k := range evt.Keywords {
				if strings.Contains(low, k) {
					lastIdx = i
					break
				}
			}
		}

		if lastIdx == -1 {
			continue
		}

		lastLine := strings.TrimSpace(lines[lastIdx])
		if lastLine == "" {
			continue
		}

		ctx.Monitor.mu.Lock()
		// On first run, record the baseline without alerting
		if !ctx.Monitor.KwInitialized {
			ctx.Monitor.KwLastSignatures[evt.Name] = lastLine
			ctx.Monitor.mu.Unlock()
			continue
		}

		// Skip if we already alerted for this exact line
		if prev, ok := ctx.Monitor.KwLastSignatures[evt.Name]; ok && prev == lastLine {
			ctx.Monitor.mu.Unlock()
			continue
		}
		ctx.Monitor.KwLastSignatures[evt.Name] = lastLine
		ctx.Monitor.mu.Unlock()

		// Build context (±3 lines around the event)
		start := lastIdx - 3
		if start < 0 {
			start = 0
		}
		end := lastIdx + 3
		if end >= len(lines) {
			end = len(lines) - 1
		}

		ctxText := strings.Join(lines[start:end+1], "\n")
		// Truncate if too long for Telegram
		if len(ctxText) > 3000 {
			ctxText = ctxText[:3000] + "\n..."
		}

		ctx.State.AddEvent("critical", fmt.Sprintf("%s detected", evt.Name))
		slog.Warn("KernelWatchdog event detected", "event", evt.Name, "line", lastLine)

		// Special handling for OOM
		var msg string
		if evt.Name == "OOM" {
			// Track OOM for auto-reboot
			handleOOMLoop(ctx, bot)

			// Only report the process name (no full log)
			procName := ctx.Tr("oom_unknown_proc")
			matches := reOOMProcess.FindStringSubmatch(lastLine)
			if len(matches) > 1 {
				procName = matches[1]
			}
			msg = fmt.Sprintf(ctx.Tr("oom_alert_simple"), procName, procName)
		}

		// Fallback or standard message
		if msg == "" {
			msg = fmt.Sprintf(ctx.Tr(evt.TrKey), ctxText)
		}

		// ALWAYS send — critical events ignore quiet hours
		m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
	}

	ctx.Monitor.mu.Lock()
	if !ctx.Monitor.KwInitialized {
		ctx.Monitor.KwInitialized = true
	}
	ctx.Monitor.mu.Unlock()
}

func handleOOMLoop(ctx *AppContext, bot BotAPI) {
	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	now := time.Now()
	// Append current event
	ctx.Monitor.RecentOOMs = append(ctx.Monitor.RecentOOMs, now)

	// Prune old events
	valid := make([]time.Time, 0, len(ctx.Monitor.RecentOOMs))
	for _, t := range ctx.Monitor.RecentOOMs {
		if now.Sub(t) < oomLoopWindow {
			valid = append(valid, t)
		}
	}
	ctx.Monitor.RecentOOMs = valid

	// Check threshold
	if len(valid) >= oomLoopThreshold {
		// Reset to avoid multiple reboots triggers if it fails or takes time
		ctx.Monitor.RecentOOMs = []time.Time{}

		// Notify user
		msg := tgbotapi.NewMessage(ctx.Config.AllowedUserID, ctx.Tr("oom_reboot_warning"))
		msg.ParseMode = "Markdown"
		safeSend(bot, msg)

		// Log it
		slog.Error("OOM Loop detected. Triggering reboot.", "count", len(valid), "window", oomLoopWindow)

		// Execute reboot in a separate goroutine to allow message to send
		go func() {
			slog.Info("Rebooting system now...")
			_ = runCommand(context.Background(), "reboot")
		}()
	}
}

func getKernelLogLines() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try dmesg first (more reliable, no auth needed on most systems)
	out, err := runCommandStdout(ctx, "dmesg", "--time-format", "reltime")
	if err != nil {
		// Fallback: dmesg without format flag (older kernels)
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		out, err = runCommandStdout(ctx2, "dmesg")
	}
	if err != nil {
		// Fallback: journalctl kernel messages
		ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel3()
		out, _ = runCommandStdout(ctx3, "journalctl", "-k", "-n", "300", "--no-pager")
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

func checkPreviousBootCrash(ctx *AppContext) string {
	if !commandExists("journalctl") {
		return ""
	}

	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Check previous boot kernel logs
	out, err := runCommandStdout(c, "journalctl", "-b", "-1", "-k", "-n", "100", "--no-pager")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")

	var crashLines []string

	// Scan for OOM or Panic
	for _, line := range lines {
		low := strings.ToLower(line)
		isHit := false
		for _, evt := range kernelEventTypes {
			if evt.Name != "OOM" && evt.Name != "KernelPanic" {
				continue
			}
			for _, k := range evt.Keywords {
				if strings.Contains(low, k) {
					isHit = true
					break
				}
			}
			if isHit {
				break
			}
		}

		if isHit {
			crashLines = append(crashLines, strings.TrimSpace(line))
		}
	}

	if len(crashLines) > 0 {
		// Dedup and limit
		if len(crashLines) > 5 {
			crashLines = crashLines[len(crashLines)-5:]
		}
		return fmt.Sprintf(ctx.Tr("crash_detected_prev_boot"), strings.Join(crashLines, "\n"))
	}

	return ""
}
