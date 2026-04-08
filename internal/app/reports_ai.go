package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nasbot/internal/format"
)

func generateAIReport(ctx *AppContext, s Stats, events []ReportEvent, isMorning bool, onModelChange func(string)) (string, error) {
	return generateAIReportWithPeriod(ctx, s, events, isMorning, "", onModelChange)
}

func generateAIReportWithPeriod(ctx *AppContext, s Stats, events []ReportEvent, isMorning bool, periodDesc string, onModelChange func(string)) (string, error) {
	if ctx.Config.GeminiAPIKey == "" {
		return "", nil
	}

	var sysContext strings.Builder
	sysContext.WriteString("NAS System Status:\n")
	sysContext.WriteString(fmt.Sprintf("- CPU: %.1f%%\n", s.CPU))
	sysContext.WriteString(fmt.Sprintf("- RAM: %.1f%%\n", s.RAM))
	if s.Swap > 5 {
		sysContext.WriteString(fmt.Sprintf("- Swap: %.1f%%\n", s.Swap))
	}
	sysContext.WriteString(fmt.Sprintf("- SSD: %.1f%% used, %s free\n", s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	sysContext.WriteString(fmt.Sprintf("- HDD: %.1f%% used, %s free\n", s.VolHDD.Used, format.FormatBytes(s.VolHDD.Free)))
	sysContext.WriteString(fmt.Sprintf("- Uptime: %s\n", format.FormatUptime(s.Uptime)))

	containers := getCachedContainerList(ctx)
	running, stopped := 0, 0
	stoppedList := []string{}
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
			stoppedList = append(stoppedList, c.Name)
		}
	}
	if ctx.Config.Healthchecks.Enabled {
		hc := ctx.Monitor.Healthchecks
		status := "Offline"
		if hc.LastPingSuccess {
			status = "Online"
		}
		sysContext.WriteString(fmt.Sprintf("- Healthchecks.io: %s (%.1f%% success rate)\n", status, float64(hc.SuccessfulPings)/float64(maxInt(hc.TotalPings, 1))*100))
		if len(hc.DowntimeEvents) > 0 {
			sysContext.WriteString("  Recent Healthchecks downtimes:\n")
			ctx.State.Mu.Lock()
			lastReportTime := ctx.State.LastReport
			ctx.State.Mu.Unlock()
			for _, e := range hc.DowntimeEvents {
				if e.StartTime.After(lastReportTime) {
					sysContext.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", e.StartTime.Format("15:04"), e.Reason, e.Duration))
				}
			}
		}
	}
	sysContext.WriteString(fmt.Sprintf("- Docker: %d running, %d stopped\n", running, stopped))
	if len(stoppedList) > 0 {
		sysContext.WriteString(fmt.Sprintf("- Stopped containers: %s\n", strings.Join(stoppedList, ", ")))
	}

	eventsInfo := "No events recorded in this period."
	if len(events) > 0 {
		eventsInfo = fmt.Sprintf("%d events recorded:\n", len(events))
		loc := ctx.State.TimeLocation
		for _, e := range events {
			eventsInfo += fmt.Sprintf("- [%s] %s: %s\n", e.Time.In(loc).Format("15:04"), e.Type, e.Message)
		}
	}
	sysContext.WriteString(fmt.Sprintf("\nEvents: %s", eventsInfo))

	timeOfDay := "morning"
	if !isMorning {
		timeOfDay = "evening"
	}

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

	periodInfo := ""
	if periodDesc != "" {
		periodInfo = fmt.Sprintf("\n**Report Period:** %s", periodDesc)
	}

	prompt := fmt.Sprintf(`You are "NasBot", an intelligent home NAS assistant.
Generate a system status report for the owner.

**Status Data:**
%s%s

**Context:**
- Time: %s
- Language: %s

**CRITICAL TELEGRAM FORMATTING RULES:**
1. NO HEADERS (# or ##). Telegram does not support Markdown headers.
2. NO DOUBLE ASTERISKS (**). Use *single asterisks* for bold text.
3. Base formatting: *bold*, _italic_, and `+"`"+`code`+"`"+`.
4. Replace section headers with emojis (e.g., 📊 *System Status*, 🐳 *Docker*, ⚠️ *Alerts*).

**REPORT STRUCTURE:**
- **Greeting:** Friendly but brief (1 sentence max).
- **System Health:** Only report CPU/RAM/Disk if they exceed 80%% or warrant attention.
- **Docker Recap:** State X running, Y stopped (mention stopped names).
- **Events & Alerts:** Summarize anomalies. Say "Quiet period" if none.
- **Footer:** Brief sign-off.

**Style:** Bullet-point heavy, scannable, direct, and conversational but extremely concise. Skip empty pleasantries.`, sysContext.String(), periodInfo, timeOfDay, lang)

	return callGeminiWithFallback(ctx, prompt, onModelChange)
}

func callGeminiWithFallback(ctx *AppContext, prompt string, onModelChange func(string)) (string, error) {
	models := []string{"gemini-3.1-flash-lite-preview", "gemini-3-flash-preview", "gemini-3.1-pro-preview"}

	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var summary string
	var err error

	for _, model := range models {
		if onModelChange != nil {
			onModelChange(model)
		}
		summary, err = callGeminiAPIWithError(ctx, c, prompt, model)
		if err == nil {
			return summary, nil
		}

		select {
		case <-c.Done():
			slog.Error("Gemini: Overall timeout")
			return "", fmt.Errorf("overall timeout")
		default:
		}
	}
	return "", err
}

func callGeminiAPIWithError(ctx *AppContext, parentCtx context.Context, prompt string, model string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, ctx.Config.GeminiAPIKey)

	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 8192,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	c, cancel := context.WithTimeout(parentCtx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(c, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := http.DefaultClient
	if ctx.HTTP != nil {
		client = ctx.HTTP
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		text := strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text)

		// Clean up any rogue double asterisks into single ones for Telegram
		reBold := regexp.MustCompile(`\*\*([^*]+)\*\*`)
		text = reBold.ReplaceAllString(text, "*$1*")

		// Replace headers (###, ##, #) with bolded text for Telegram
		reH3 := regexp.MustCompile(`(?m)^###\s+(.*?)\r?$`)
		text = reH3.ReplaceAllString(text, "*$1*")

		reH2 := regexp.MustCompile(`(?m)^##\s+(.*?)\r?$`)
		text = reH2.ReplaceAllString(text, "*$1*")

		reH1 := regexp.MustCompile(`(?m)^#\s+(.*?)\r?$`)
		text = reH1.ReplaceAllString(text, "*$1*")

		return text, nil
	}
	return "", fmt.Errorf("empty response")
}
