package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// geminiAPIKeyHeader is the header Google documents for passing the API key
	// (https://ai.google.dev/gemini-api/docs/api-key: `-H "x-goog-api-key: …"`).
	// The key must NOT go in the query string: net/http reports transport
	// failures as *url.Error, which embeds the whole request URL, and those
	// errors are logged to var/nasbot.log and echoed into Telegram messages.
	geminiAPIKeyHeader = "x-goog-api-key"

	// redactedSecret replaces a secret in any string bound for a log or a chat.
	redactedSecret = "[REDACTED]"

	// geminiMaxResponseBytes caps the API response (1MB) to keep memory flat on
	// the low-RAM targets NASBot runs on.
	geminiMaxResponseBytes = 1 << 20

	// geminiErrorBodyChars caps how much of an error body is kept for context.
	geminiErrorBodyChars = 200
)

// Pre-compiled regex for Telegram formatting cleanup.
// Compiled once at package init rather than per-call.
var (
	reBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	reH3   = regexp.MustCompile(`(?m)^###\s+(.*?)\r?$`)
	reH2   = regexp.MustCompile(`(?m)^##\s+(.*?)\r?$`)
	reH1   = regexp.MustCompile(`(?m)^#\s+(.*?)\r?$`)
)

// redactSecret replaces every occurrence of secret in s with a placeholder.
func redactSecret(s, secret string) string {
	if s == "" || secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, redactedSecret)
}

// sanitizeGeminiError strips the API key out of an error before that error can
// reach a log line or a Telegram message.
//
// It is applied at the single exit point of the Gemini layer, so it also covers
// the cases where the key is not in the URL at all: a *url.Error that carries a
// request built elsewhere, a body read failure that quotes the request line, or
// an API error payload that echoes the credential back.
func sanitizeGeminiError(err error, secret string) error {
	if err == nil {
		return nil
	}
	redacted := redactSecret(err.Error(), secret)
	if redacted == err.Error() {
		return err
	}
	return errors.New(redacted)
}

// geminiAPIKey returns the configured key, tolerating a context built without a
// published Config (the Gemini layer is optional and must never panic).
func geminiAPIKey(ctx *AppContext) string {
	if ctx == nil {
		return ""
	}
	cfg := ctx.Cfg()
	if cfg == nil {
		return ""
	}
	return cfg.GeminiAPIKey
}

// generateAIReport triggers an API call yielding a conversational model readout for NAS events.
func generateAIReport(ctx *AppContext, events []ReportEvent, onModelChange func(string)) (string, error) {
	// Optional feature: without a key the report is produced without AI and
	// nothing else in the bot is affected.
	if geminiAPIKey(ctx) == "" {
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
	models := []string{"gemini-3.1-flash-lite", "gemini-3.5-flash", "gemini-3.1-pro-preview"}

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
			// err is already sanitized: it can be logged as-is.
			slog.Error("Gemini: Overall timeout", "model", model, "err", err)
			return "", errors.New("overall timeout")
		default:
		}
	}
	return "", err
}

// callGeminiAPIWithError is the only entry point to the Gemini HTTP layer.
// It sanitizes whatever the underlying call reports, so no caller can leak the
// API key by logging or sending the returned error.
func callGeminiAPIWithError(ctx *AppContext, parentCtx context.Context, prompt string, model string) (string, error) {
	apiKey := geminiAPIKey(ctx)
	text, err := callGeminiAPI(ctx, parentCtx, prompt, model, apiKey)
	if err != nil {
		return "", sanitizeGeminiError(err, apiKey)
	}
	return text, nil
}

// callGeminiAPI performs one generateContent request. The API key travels in the
// x-goog-api-key header, never in the URL.
func callGeminiAPI(ctx *AppContext, parentCtx context.Context, prompt string, model, apiKey string) (string, error) {
	if apiKey == "" {
		// Degrade cleanly: the key is optional, so an unconfigured bot must not
		// burn three model retries on unauthenticated requests.
		return "", errors.New("gemini api key not configured")
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)

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

	req, err := http.NewRequestWithContext(c, http.MethodPost, url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(geminiAPIKeyHeader, apiKey)

	var client *http.Client
	if ctx != nil {
		client = ctx.HTTP
	}
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Limit response body to 1MB to prevent memory exhaustion on low-RAM systems
	body, err := io.ReadAll(io.LimitReader(resp.Body, geminiMaxResponseBytes))
	if err != nil {
		return "", err
	}

	if resp.StatusCode == 429 {
		return "", fmt.Errorf("rate limited (429)")
	}
	if resp.StatusCode != 200 {
		// Truncate error body to avoid flooding logs
		errBody := string(body)
		if len(errBody) > geminiErrorBodyChars {
			errBody = errBody[:geminiErrorBodyChars] + "..."
		}
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, errBody)
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
		text = reBold.ReplaceAllString(text, "*$1*")

		// Replace headers (###, ##, #) with bolded text for Telegram
		text = reH3.ReplaceAllString(text, "*$1*")
		text = reH2.ReplaceAllString(text, "*$1*")
		text = reH1.ReplaceAllString(text, "*$1*")

		return text, nil
	}
	return "", fmt.Errorf("empty response")
}
