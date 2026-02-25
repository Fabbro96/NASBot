//go:build !fswatchdog
// +build !fswatchdog

package main

import (
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var cmdRegistry *CommandRegistry

func init() {
	cmdRegistry = SetupCommandRegistry()
}

func handleCommand(bot BotAPI, msg *tgbotapi.Message) {
	if app == nil {
		slog.Error("App context is nil in handleCommand")
		return
	}
	if cmdRegistry.Execute(app, bot, msg) {
		return
	}
	safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("unknown_command")))
}

func handleCallback(bot BotAPI, query *tgbotapi.CallbackQuery) {
	if app == nil {
		slog.Error("App context is nil in handleCallback")
		return
	}
	if query == nil || query.Message == nil {
		slog.Warn("Invalid callback payload")
		return
	}
	bot.Request(tgbotapi.NewCallback(query.ID, ""))
	if query.From == nil || query.From.ID != int64(app.Config.AllowedUserID) {
		slog.Warn("Unauthorized callback ignored")
		return
	}
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	data := query.Data

	if handleSettingsCallback(app, bot, chatID, msgID, data) {
		return
	}
	if handlePowerAndDockerCallback(app, bot, chatID, msgID, data) {
		return
	}
	if handleScopedCallback(app, bot, chatID, msgID, query, data) {
		return
	}
	handleMainMenuCallback(app, bot, chatID, msgID, data)
}
