package app

import (
	"context"
	"fmt"
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

	var apiURL string
	if data == "adblock_pause_5m" {
		apiURL = fmt.Sprintf("%s/admin/api.php?disable=300&auth=%s", baseURL, url.QueryEscape(token))
	} else if data == "adblock_resume" {
		apiURL = fmt.Sprintf("%s/admin/api.php?enable&auth=%s", baseURL, url.QueryEscape(token))
	} else {
		slog.Warn("Rejected adblock callback payload", "data", truncate(data, 64))
		return
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", apiURL, nil)
	if err != nil {
		// The URL carries the AdBlock auth token, so the error is sanitized
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

	if resp.StatusCode >= 400 {
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("adblock_err_status"), resp.StatusCode)))
		return
	}

	if data == "adblock_pause_5m" {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_paused_success")))
	} else {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_resumed_success")))
	}
}
