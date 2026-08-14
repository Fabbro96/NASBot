package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleAdBlockCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	if !ctx.Config.AdBlock.Enabled {
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("adblock_disabled")))
		return
	}

	baseURL := ctx.Config.AdBlock.URL
	token := ctx.Config.AdBlock.Token

	if baseURL == "" {
		safeSend(bot, tgbotapi.NewMessage(chatID, "❌ AdBlock URL not configured."))
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
		return
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", apiURL, nil)
	if err != nil {
		safeSend(bot, tgbotapi.NewMessage(chatID, "❌ Error creating AdBlock request: "+err.Error()))
		return
	}

	client := ctx.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		safeSend(bot, tgbotapi.NewMessage(chatID, "❌ Error contacting AdBlock: "+err.Error()))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ AdBlock returned error status: %d", resp.StatusCode)))
		return
	}

	if data == "adblock_pause_5m" {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_paused_success")))
	} else {
		safeSend(bot, tgbotapi.NewMessage(chatID, "✅ "+ctx.Tr("adblock_resumed_success")))
	}
}
