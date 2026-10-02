package commands

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nasbot/internal/format"
)

func getLogsText(ctx *AppContext) string {
	tr := ctx.Tr
	recentLogs, err := getRecentLogs(ctx)
	if err != nil {
		return fmt.Sprintf("%s_No logs available_\n", tr("logs_title"))
	}

	return fmt.Sprintf("%s```\n%s\n```", tr("logs_title"), recentLogs)
}

// errNoLogs is internal only: the user sees the translated "no logs" message.
var errNoLogs = errors.New("no logs available")

func getRecentLogs(_ *AppContext) (string, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), logCmdTimeout)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "dmesg")
	if err != nil || len(out) == 0 {
		// Own context for the fallback: when dmesg blocks until its deadline
		// (common when the kernel ring buffer is full) the request context is
		// already expired, so journalctl had ~0 ms left and /logs reported
		// "No logs available" even though journalctl would have worked.
		fallbackCtx, cancelFallback := context.WithTimeout(context.Background(), logCmdTimeout)
		defer cancelFallback()

		fallbackOut, fallbackErr := runCommandOutput(fallbackCtx, "journalctl", "-n", strconv.Itoa(maxLogLines), "--no-pager")
		if fallbackErr != nil || len(fallbackOut) == 0 {
			if err != nil {
				return "", fmt.Errorf("dmesg failed: %w; journalctl failed: %v", err, fallbackErr)
			}
			return "", errNoLogs
		}
		out = fallbackOut
	}
	if len(out) == 0 {
		return "", errNoLogs
	}

	lines := strings.Split(string(out), "\n")
	start := len(lines) - maxLogLines
	if start < 0 {
		start = 0
	}
	recentLogs := strings.Join(lines[start:], "\n")

	// Keep the newest lines: a byte slice would split runes in half and turn
	// non-ASCII kernel timestamps into U+FFFD, which makes Telegram reject the
	// message.
	recentLogs = runeTail(recentLogs, maxLogChars)

	return strings.TrimSpace(recentLogs), nil
}

// ═══════════════════════════════════════════════════════════════════
//  LOG SEARCH
// ═══════════════════════════════════════════════════════════════════

func getLogSearchText(ctx *AppContext, args string) string {
	tr := ctx.Tr
	// Parse: container keyword
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		return tr("logsearch_usage")
	}

	container := parts[0]
	keyword := parts[1]

	// Sanitize container name to prevent injection
	if strings.ContainsAny(container, ";|&$`\\\"'") {
		return tr("logsearch_invalid_container")
	}

	// Search logs
	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "docker", "logs", "--tail", "500", container)
	if err != nil {
		return fmt.Sprintf("❌ Error: `%v`", err)
	}

	// Filter lines containing keyword
	lines := strings.Split(string(out), "\n")
	var matches []string
	keywordLower := strings.ToLower(keyword)

	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), keywordLower) {
			// Truncate long lines (rune-safe)
			matches = append(matches, format.Truncate(line, maxLogLineChars))
		}
	}

	if len(matches) == 0 {
		return trf(tr, "logsearch_no_matches", keyword, container)
	}

	// Limit to last 10 matches
	totalFound := len(matches)
	if len(matches) > 10 {
		matches = matches[len(matches)-10:]
	}

	var b strings.Builder
	b.WriteString(trf(tr, "logsearch_title", keyword, container))
	b.WriteString(trf(tr, "logsearch_found_fmt", totalFound, len(matches)))
	b.WriteString("```\n")
	for _, m := range matches {
		b.WriteString(m + "\n")
	}
	b.WriteString("```")

	return b.String()
}
