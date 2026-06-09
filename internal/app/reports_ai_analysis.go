package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// AnalyzeCriticalAlerts fetches system context and asks Gemini to diagnose the issue.
func AnalyzeCriticalAlerts(ctx *AppContext, onModelChange func(string)) (string, error) {
	if ctx.Config.GeminiAPIKey == "" {
		return "❌ Gemini API Key not configured.", nil
	}

	sysContext := fetchSystemContextForAI()

	lang := "English"
	switch ctx.Settings.GetLanguage() {
	case "it":
		lang = "Italian"
	case "es":
		lang = "Spanish"
	case "de":
		lang = "German"
	case "zh":
		lang = "Chinese"
	case "uk":
		lang = "Ukrainian"
	}

	prompt := fmt.Sprintf(`You are "NasBot", an intelligent home NAS assistant.
A critical alert was just triggered by the system monitors. The user wants you to analyze the current system state and logs to find the root cause of the issue and suggest a fix.

**System Context:**
%s

**Context:**
- Language: %s

**CRITICAL TELEGRAM FORMATTING RULES:**
1. NO HEADERS (# or ##). Telegram does not support Markdown headers.
2. NO DOUBLE ASTERISKS (**). Use *single asterisks* for bold text.
3. Base formatting: *bold*, _italic_, and `+"`"+`code`+"`"+`.
4. Keep the output as a bullet-point list.

**REQUIRED REPORT STRUCTURE:**
(Do not add a greeting or footer)

🚨 *Diagnosi AI*
- What caused the critical alert?
- What process/container is responsible?
- Suggested command or action to fix it.

**Style:** Direct, helpful, concise, and technically accurate.`, sysContext, lang)

	return callGeminiWithFallback(ctx, prompt, onModelChange)
}

func fetchSystemContextForAI() string {
	var sb strings.Builder

	// Top processes
	ctxExec, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctxExec, "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu").CombinedOutput()
	if err == nil {
		lines := strings.Split(string(out), "\n")
		limit := 10
		if len(lines) < 10 {
			limit = len(lines)
		}
		sb.WriteString("Top Processes:\n")
		sb.WriteString(strings.Join(lines[:limit], "\n"))
		sb.WriteString("\n\n")
	}

	// Syslog
	ctxExec2, cancel2 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel2()
	outSyslog, err := exec.CommandContext(ctxExec2, "journalctl", "-n", "30", "--no-pager").CombinedOutput()
	if err == nil {
		sb.WriteString("Recent Syslog:\n")
		sb.WriteString(string(outSyslog))
		sb.WriteString("\n\n")
	}

	// Docker stats
	ctxExec3, cancel3 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel3()
	outDocker, err := exec.CommandContext(ctxExec3, "docker", "stats", "--no-stream", "--format", "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}").CombinedOutput()
	if err == nil {
		sb.WriteString("Docker Stats:\n")
		sb.WriteString(string(outDocker))
	}

	return sb.String()
}
