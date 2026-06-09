package app

import (
	"fmt"
	"net/http"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleAdBlockCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	if !ctx.Config.AdBlock.Enabled {
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("adblock_disabled")))
		return
	}

	url := ctx.Config.AdBlock.URL
	token := ctx.Config.AdBlock.Token

	if url == "" {
		safeSend(bot, tgbotapi.NewMessage(chatID, "❌ AdBlock URL not configured."))
		return
	}

	// Clean the URL
	url = strings.TrimSuffix(url, "/")

	var apiURL string
	if data == "adblock_pause_5m" {
		apiURL = fmt.Sprintf("%s/admin/api.php?disable=300&auth=%s", url, token)
	} else if data == "adblock_resume" {
		apiURL = fmt.Sprintf("%s/admin/api.php?enable&auth=%s", url, token)
	} else {
		return
	}

	resp, err := http.Get(apiURL)
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
