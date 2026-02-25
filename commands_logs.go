package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func getLogsText(ctx *AppContext) string {
	tr := ctx.Tr
	recentLogs, err := getRecentLogs(ctx)
	if err != nil {
		return fmt.Sprintf("%s_No logs available_\n", tr("logs_title"))
	}

	return fmt.Sprintf("%s```\n%s\n```", tr("logs_title"), recentLogs)
}

func getRecentLogs(_ *AppContext) (string, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), logCmdTimeout)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "dmesg")
	if err != nil || len(out) == 0 {
		fallbackOut, fallbackErr := runCommandOutput(reqCtx, "journalctl", "-n", fmt.Sprint(maxLogLines), "--no-pager")
		if fallbackErr != nil || len(fallbackOut) == 0 {
			if err != nil {
				return "", fmt.Errorf("dmesg failed: %w; journalctl failed: %v", err, fallbackErr)
			}
			return "", fmt.Errorf("no logs available")
		}
		out = fallbackOut
	}
	if len(out) == 0 {
		return "", fmt.Errorf("no logs available")
	}

	lines := strings.Split(string(out), "\n")
	start := len(lines) - maxLogLines
	if start < 0 {
		start = 0
	}
	recentLogs := strings.Join(lines[start:], "\n")

	if len(recentLogs) > maxLogChars {
		recentLogs = recentLogs[len(recentLogs)-maxLogChars:]
	}

	return strings.TrimSpace(recentLogs), nil
}

// ═══════════════════════════════════════════════════════════════════
//  LOG SEARCH
// ═══════════════════════════════════════════════════════════════════

func getLogSearchText(ctx *AppContext, args string) string {
	_ = ctx
	// Parse: container keyword
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		return "Usage: `/logsearch <container> <keyword>`\n\nExample: `/logsearch plex error`"
	}

	container := parts[0]
	keyword := parts[1]

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
			// Truncate long lines
			if len(line) > 100 {
				line = line[:97] + "..."
			}
			matches = append(matches, line)
		}
	}

	if len(matches) == 0 {
		return fmt.Sprintf("🔍 No matches for `%s` in `%s` logs", keyword, container)
	}

	// Limit to last 10 matches
	if len(matches) > 10 {
		matches = matches[len(matches)-10:]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔍 *Log Search*: `%s` in `%s`\n\n", keyword, container))
	b.WriteString(fmt.Sprintf("Found %d matches (showing last %d):\n\n", len(matches), len(matches)))
	b.WriteString("```\n")
	for _, m := range matches {
		b.WriteString(m + "\n")
	}
	b.WriteString("```")

	return b.String()
}
