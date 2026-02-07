package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	if ctx.Monitor.KwLastSignatures == nil {
		ctx.Monitor.KwLastSignatures = make(map[string]string)
	}

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

		// On first run, record the baseline without alerting
		if !ctx.Monitor.KwInitialized {
			ctx.Monitor.KwLastSignatures[evt.Name] = lastLine
			continue
		}

		// Skip if we already alerted for this exact line
		if prev, ok := ctx.Monitor.KwLastSignatures[evt.Name]; ok && prev == lastLine {
			continue
		}
		ctx.Monitor.KwLastSignatures[evt.Name] = lastLine

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

		// ALWAYS send — critical events ignore quiet hours
		msg := fmt.Sprintf(ctx.Tr(evt.TrKey), ctxText)
		m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
	}

	if !ctx.Monitor.KwInitialized {
		ctx.Monitor.KwInitialized = true
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
