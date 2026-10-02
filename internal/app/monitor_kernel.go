package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Regex to extract process name from OOM logs
// e.g. "Out of memory: Killed process 123 (python3)..."
// e.g. "oom_reaper: reaped process 123 (python3)..."
var reOOMProcess = regexp.MustCompile(`(?:Killed process|reaped process) \d+ \((.+?)\)`)

// reHungTask matches a kernel hung-task / stall report. A real line looks like
//
//	INFO: task kworker/0:1:123 blocked for more than 120 seconds.
//
// This could never be matched with strings.Contains, because the entry in
// kernelEventTypes is a pattern, not a literal. It is compiled once at package
// level, so a cycle costs one MatchString per line, not one Compile.
var reHungTask = regexp.MustCompile(`(?i)(?:info:\s*)?task\s+\S+\s+blocked for more than\s+\d+\s+seconds?`)

// OOM Loop thresholds
const (
	oomLoopWindow    = 30 * time.Minute
	oomLoopThreshold = 5
)

// maxKernelLogLines bounds how many dmesg lines are scanned per cycle. The
// kernel ring buffer holds 100k+ lines on a long uptime, and the previous code
// split them all, lowercased each of them once per event type (5 types) and ran
// 25 strings.Contains on each: once a second of CPU, every single cycle.
const maxKernelLogLines = 300

// kernelContextRunes bounds the log context attached to an alert. Truncate is
// rune-aware, so a multi-byte kernel line is cut on a character boundary and the
// alert cannot be lost to a UTF-8 error.
const kernelContextRunes = 3000

// kernelEventType defines a class of critical kernel events
type kernelEventType struct {
	Name     string
	TrKey    string
	Keywords []string
	// Pattern is checked in addition to Keywords, against the lowercased line,
	// for events whose kernel log form is not a literal substring.
	Pattern *regexp.Regexp
}

// kernelEventMatches reports whether a lowercased kernel line belongs to evt.
func kernelEventMatches(evt kernelEventType, low string) bool {
	for _, k := range evt.Keywords {
		if strings.Contains(low, k) {
			return true
		}
	}
	return evt.Pattern != nil && evt.Pattern.MatchString(low)
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
		Name:     "HungTask",
		TrKey:    "hung_task",
		Keywords: []string{"hung_task_timeout"},
		// "blocked for more than N seconds" always has a task name in between,
		// so no literal keyword can catch a real stall line: this needs the
		// regex, which is compiled once at package level.
		Pattern: reHungTask,
	},
}

// ═══════════════════════════════════════════════════════════════════
//  KERNEL WATCHDOG — OOM, panic, I/O errors, read-only FS, hung tasks
//  ALWAYS notifies (ignores quiet hours) — these are critical events
// ═══════════════════════════════════════════════════════════════════

func checkKernelEvents(ctx *AppContext, bot BotAPI) {
	ctx.Monitor.Mu.Lock()
	ctx.Monitor.KwLastCheckTime = time.Now()
	ctx.Monitor.Mu.Unlock()

	lines, err := getKernelLogLines()
	if err != nil {
		ctx.Monitor.Mu.Lock()
		ctx.Monitor.KwConsecutiveCheckErrors++
		ctx.Monitor.KwLastCheckError = err.Error()
		ctx.Monitor.Mu.Unlock()
		slog.Warn("KernelWatchdog log collection failed", "err", err)
		return
	}

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.KwConsecutiveCheckErrors = 0
	ctx.Monitor.KwLastCheckError = ""
	ctx.Monitor.Mu.Unlock()

	if len(lines) == 0 {
		return
	}

	processKernelLines(ctx, bot, lines)
}

func processKernelLines(ctx *AppContext, bot BotAPI, lines []string) {
	ctx.Monitor.Mu.Lock()
	if ctx.Monitor.KwLastSignatures == nil {
		ctx.Monitor.KwLastSignatures = make(map[string]string)
	}
	ctx.Monitor.Mu.Unlock()

	// Lowercase once per line instead of once per line per event type.
	lowLines := make([]string, len(lines))
	for i, line := range lines {
		lowLines[i] = strings.ToLower(line)
	}

	for _, evt := range kernelEventTypes {
		lastIdx := -1
		for i, low := range lowLines {
			if kernelEventMatches(evt, low) {
				lastIdx = i
			}
		}

		if lastIdx == -1 {
			continue
		}

		lastLine := strings.TrimSpace(lines[lastIdx])
		if lastLine == "" {
			continue
		}

		ctx.Monitor.Mu.Lock()
		// On first run, record the baseline without alerting
		if !ctx.Monitor.KwInitialized {
			ctx.Monitor.KwLastSignatures[evt.Name] = lastLine
			ctx.Monitor.Mu.Unlock()
			continue
		}

		// Skip if we already alerted for this exact line
		if prev, ok := ctx.Monitor.KwLastSignatures[evt.Name]; ok && prev == lastLine {
			ctx.Monitor.Mu.Unlock()
			continue
		}
		ctx.Monitor.KwLastSignatures[evt.Name] = lastLine
		ctx.Monitor.Mu.Unlock()

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
		// Truncate if too long for Telegram. Truncate counts runes, so this
		// cannot split a UTF-8 sequence: the previous ctxText[:3000] cut bytes
		// and made Telegram reject the whole alert with a parse error.
		if len([]rune(ctxText)) > kernelContextRunes {
			ctxText = format.Truncate(ctxText, kernelContextRunes)
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
		m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
	}

	ctx.Monitor.Mu.Lock()
	if !ctx.Monitor.KwInitialized {
		ctx.Monitor.KwInitialized = true
	}
	ctx.Monitor.Mu.Unlock()
}

func handleOOMLoop(ctx *AppContext, bot BotAPI) {
	now := time.Now()
	triggerReboot := false
	oomCount := 0

	ctx.Monitor.Mu.Lock()
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
		triggerReboot = true
		oomCount = len(valid)
	}
	// Lock released before touching Telegram: safeSend uses a client with a
	// 75-90s timeout, and holding Monitor.Mu across it froze SMART, temperature,
	// network, RAID, healthchecks, the daily report and this very function.
	ctx.Monitor.Mu.Unlock()

	if !triggerReboot {
		return
	}

	// Notify user
	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, ctx.Tr("oom_reboot_warning"))
	m.ParseMode = "Markdown"
	safeSend(bot, m)

	// Log it
	slog.Error("OOM Loop detected. Triggering reboot.", "count", oomCount, "window", oomLoopWindow)

	// Execute reboot in a separate goroutine to allow message to send
	goSafe("oom-reboot", func() {
		slog.Info("Rebooting system now...")
		if err := runCommand(context.Background(), "reboot"); err != nil {
			slog.Error("OOM reboot command failed", "err", err)
		}
	})
}

// keepLastLines returns the tail of out, at most n lines, without splitting the
// first line in half.
func keepLastLines(out []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	// Count newlines; the text is scanned in place, no copy of the buffer.
	newlines := bytes.Count(out, []byte{'\n'})
	if newlines <= n {
		return out
	}
	skip := newlines - n
	pos := 0
	for i := 0; i < skip; i++ {
		j := bytes.IndexByte(out[pos:], '\n')
		if j < 0 {
			return out
		}
		pos += j + 1
	}
	return out[pos:]
}

func getKernelLogLines() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// dmesg has no tail equivalent, so the buffer is capped in Go. This keeps
	// the split (and therefore the ToLower + 25 Contains per line) bounded to
	// maxKernelLogLines instead of the whole ring buffer.
	out, err := runCommandStdout(ctx, "dmesg", "--time-format", "reltime")
	lastErr := err
	if err != nil {
		// Fallback: dmesg without format flag (older kernels)
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		out, err = runCommandStdout(ctx2, "dmesg")
		lastErr = err
	}
	if err != nil {
		// Fallback: journalctl kernel messages
		ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel3()
		out, err = runCommandStdout(ctx3, "journalctl", "-k", "-n", "300", "--no-pager")
		lastErr = err
	}

	if lastErr != nil {
		return nil, fmt.Errorf("failed to collect kernel logs: %w", lastErr)
	}

	text := strings.TrimSpace(string(keepLastLines(out, maxKernelLogLines)))
	if text == "" {
		return nil, nil
	}

	return strings.Split(text, "\n"), nil
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
			if kernelEventMatches(evt, low) {
				isHit = true
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
