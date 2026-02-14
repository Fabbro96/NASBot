package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
			ctx.State.mu.Lock()
			lastReportTime := ctx.State.LastReport
			ctx.State.mu.Unlock()
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

	prompt := fmt.Sprintf(`You are an intelligent home NAS assistant named "NasBot".
Your goal is to write a **Daily Report** for the owner.

**Status Data:**
%s%s

**Time:** %s

**Instructions:**
1. **Style:** Friendly, discursive/narrative, but **CONCISE**. Keep it short.
2. **Language:** Write in %s.
3. **Format:** Use Markdown (bold, italics) and Emojis.
4. **Content:**
   - Greeting and date/time.
   - **MANDATORY:** State number of running/stopped containers.
   - **Focus on what happened.**
   - If no events, say "quiet period".
   - If everything is fine, say it briefly.
   - Explain issues clearly with warning icons.
   - Mention resources only if high usage.
   - End with short footer showing report period.

**Goal:** Useful, readable, short summary.`, sysContext.String(), periodInfo, timeOfDay, lang)

	return callGeminiWithFallback(ctx, prompt, onModelChange)
}

func callGeminiWithFallback(ctx *AppContext, prompt string, onModelChange func(string)) (string, error) {
	models := []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.0-flash"}

	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var summary string
	var err error

	for _, model := range models {
		select {
		case <-c.Done():
			slog.Error("Gemini: Overall timeout")
			return "", fmt.Errorf("overall timeout")
		default:
		}

		if onModelChange != nil {
			onModelChange(model)
		}
		summary, err = callGeminiAPIWithError(ctx, prompt, model)
		if err == nil {
			return summary, nil
		}
	}
	return "", err
}

func callGeminiAPIWithError(ctx *AppContext, prompt string, model string) (string, error) {
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

	c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
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
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text), nil
	}
	return "", fmt.Errorf("empty response")
}
