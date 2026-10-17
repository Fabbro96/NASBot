package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleAdBlockCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	cfg := ctx.Cfg()
	if cfg == nil {
		return
	}
	if !cfg.AdBlock.Enabled {
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("adblock_disabled")))
		return
	}

	baseURL := cfg.AdBlock.URL
	token := cfg.AdBlock.Token

	if baseURL == "" {
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("adblock_no_url")))
		return
	}

	// Clean the URL
	baseURL = strings.TrimSuffix(baseURL, "/")

	pause := data == "adblock_pause_5m"
	if !pause && data != "adblock_resume" {
		slog.Warn("Rejected adblock callback payload", "data", truncate(data, 64))
		return
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := buildAdBlockRequest(reqCtx, cfg.AdBlock.Type, baseURL, token, pause)
	if err != nil {
		// The URL may carry the AdBlock auth token, so the error is sanitized
		// before it is logged or shown.
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("adblock_err_create_req"), sanitizeErr(err))))
		return
	}

	client := ctx.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("adblock_err_contact"), sanitizeErr(err))))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))

	if resp.StatusCode >= 400 {
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("adblock_err_status"), resp.StatusCode)))
		return
	}
	if !adBlockResponseOK(cfg.AdBlock.Type, body, pause) {
		// Pi-hole answers 200 with an empty list when the token is wrong.
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("adblock_err_status"), http.StatusUnauthorized)))
		return
	}

	if data == "adblock_pause_5m" {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_paused_success")))
	} else {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_resumed_success")))
	}
}

// buildAdBlockRequest builds the pause/resume request for the configured
// backend. Pi-hole (the default) uses its legacy admin API with the token in the
// query string; AdGuard Home uses POST /control/protection with HTTP basic
// auth, where the token is "user:password".
func buildAdBlockRequest(ctx context.Context, kind, baseURL, token string, pause bool) (*http.Request, error) {
	if u, err := url.Parse(baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("adblock.url must be an http(s) URL")
	}
	if strings.EqualFold(kind, "adguard") {
		payload := map[string]any{"enabled": !pause}
		if pause {
			payload["duration"] = 300000 // milliseconds
		}
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/control/protection", bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if user, pass, ok := strings.Cut(token, ":"); ok {
			req.SetBasicAuth(user, pass)
		}
		return req, nil
	}
	apiURL := fmt.Sprintf("%s/admin/api.php?enable&auth=%s", baseURL, url.QueryEscape(token))
	if pause {
		apiURL = fmt.Sprintf("%s/admin/api.php?disable=300&auth=%s", baseURL, url.QueryEscape(token))
	}
	return http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
}

// adBlockResponseOK checks the body of a 2xx answer. AdGuard Home answers with
// an empty body; Pi-hole reports {"status":"disabled"|"enabled"} and, on a bad
// token, a 200 with an empty JSON list.
func adBlockResponseOK(kind string, body []byte, pause bool) bool {
	if strings.EqualFold(kind, "adguard") {
		return true
	}
	var parsed struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Status == "" {
		return false
	}
	want := "enabled"
	if pause {
		want = "disabled"
	}
	return parsed.Status == want
}
