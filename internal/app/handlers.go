//go:build !fswatchdog
// +build !fswatchdog

package app

import (
	"fmt"
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

func handleMessage(bot BotAPI, msg *tgbotapi.Message) {
	if app == nil {
		slog.Error("App context is nil in handleMessage")
		return
	}
	
	action := app.Bot.GetPendingAction()
	if action == "add_report_time" {
		app.Bot.ClearPendingAction()
		
		var hour, minute int
		if _, err := fmt.Sscanf(msg.Text, "%d:%d", &hour, &minute); err != nil {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "❌ Invalid format. Use HH:MM (e.g., 14:30)"))
			return
		}
		
		if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "❌ Invalid time. Hours: 0-23, Minutes: 0-59"))
			return
		}
		
		app.Settings.Mu.Lock()
		app.Settings.ReportTimes = append(app.Settings.ReportTimes, TimePoint{Hour: hour, Minute: minute})
		app.Settings.Mu.Unlock()
		saveState(app)
		
		text, kb := getReportSettingsText(app)
		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "✅ Time added successfully."))
		msgSettings := tgbotapi.NewMessage(msg.Chat.ID, text)
		msgSettings.ParseMode = "Markdown"
		msgSettings.ReplyMarkup = kb
		safeSend(bot, msgSettings)
		return
	}
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
	if _, err := bot.Request(tgbotapi.NewCallback(query.ID, "")); err != nil {
		slog.Warn("Failed to acknowledge callback", "err", err)
	}
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
