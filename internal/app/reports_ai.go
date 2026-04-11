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
)

func generateAIReport(ctx *AppContext, events []ReportEvent, onModelChange func(string)) (string, error) {
	return generateAIReportWithPeriod(ctx, events, "", onModelChange)
}

func generateAIReportWithPeriod(ctx *AppContext, events []ReportEvent, periodDesc string, onModelChange func(string)) (string, error) {
	if ctx.Config.GeminiAPIKey == "" {
		return "", nil
	}

	if len(events) == 0 {
		return "- No noteworthy events recorded.", nil
	}

	var sysContext strings.Builder

	eventsInfo := fmt.Sprintf("%d events recorded:\n", len(events))
	loc := ctx.State.TimeLocation
	for _, e := range events {
		eventsInfo += fmt.Sprintf("- [%s] %s: %s\n", e.Time.In(loc).Format("15:04"), e.Type, e.Message)
	}
	sysContext.WriteString(fmt.Sprintf("Events:\n%s", eventsInfo))

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
A system report is being generated and I need you to summarize the recent system events.

**Events Data:**
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

⚠️ *Events & Alerts*
- Categorize events (e.g., - Critical:, - Network:, - Maintenance:).
- Summarize anomalies accurately. Do not invent details.

**Style:** Bullet-point heavy, scannable, rigorous, direct, and extremely concise.`, sysContext.String(), lang)

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
