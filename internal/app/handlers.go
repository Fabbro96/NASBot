package app

import (
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var cmdRegistry *CommandRegistry
var cbRegistry *CallbackRegistry

func init() {
	cmdRegistry = SetupCommandRegistry()
	cbRegistry = SetupCallbackRegistry()
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
		// The pending action is cleared only once the input is known good:
		// clearing it first made a mistyped time throw away the whole flow.
		var hour, minute int
		if _, err := fmt.Sscanf(msg.Text, "%d:%d", &hour, &minute); err != nil {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("err_invalid_time_fmt")))
			return
		}

		if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("err_invalid_time_range")))
			return
		}

		app.Bot.ClearPendingAction()
		app.Settings.AddReportTime(TimePoint{Hour: hour, Minute: minute})
		saveState(app)

		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("time_added_success")))
		text, kb := getReportSettingsText(app)
		msgSettings := newMarkdownMessage(msg.Chat.ID, text)
		msgSettings.ReplyMarkup = kb
		safeSend(bot, msgSettings)
		return
	}

	if action == "set_backup_uid" {
		var uid int64
		if _, err := fmt.Sscanf(strings.TrimSpace(msg.Text), "%d", &uid); err != nil {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("err_invalid_uid_fmt")))
			return
		}

		app.Bot.ClearPendingAction()
		patch := map[string]interface{}{
			"backup": map[string]interface{}{
				"target_user_id": uid,
			},
		}
		if !applySettingsPatch(app, bot, msg.Chat.ID, patch) {
			// No success line and no redrawn screen: on a failure the screen
			// would be rebuilt from the configuration that was never reloaded,
			// showing the old value under a confirmation.
			return
		}

		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("backup_uid_updated")))
		text, kb := getBackupSettingsText(app)
		msgSettings := newMarkdownMessage(msg.Chat.ID, text)
		msgSettings.ReplyMarkup = kb
		safeSend(bot, msgSettings)
		return
	}

	if strings.HasPrefix(action, "thresh_custom_") {
		var val float64
		if _, err := fmt.Sscanf(strings.TrimSpace(msg.Text), "%f", &val); err != nil || val < 0 || val > 100 {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("err_invalid_percent_val")))
			return
		}

		level, res, ok := parseThresholdAction(action, "thresh_custom_")
		if !ok {
			// A pending action no longer matching the current grammar: drop it
			// instead of leaving it armed for the next unrelated message.
			app.Bot.ClearPendingAction()
			slog.Warn("Dropping unparsable pending action", "action", truncate(action, 96))
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("session_expired")))
			return
		}
		canonical, mount, ok := resolveThresholdResource(app, res)
		if !ok {
			app.Bot.ClearPendingAction()
			rejectCallback(app, bot, msg.Chat.ID, "thresh_custom_input", res, "")
			return
		}

		app.Bot.ClearPendingAction()
		cfg := app.Cfg()
		if cfg == nil {
			return
		}
		patch := thresholdPatch(cfg, canonical, mount, level, val)
		if !applySettingsPatch(app, bot, msg.Chat.ID, patch) {
			return
		}

		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, app.Tr("thresh_updated_success")))
		text, kb := getThresholdResourceText(app, canonical)
		msgSettings := newMarkdownMessage(msg.Chat.ID, text)
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

	// Authorization is decided before the payload reaches any handler.
	// answerCallbackQuery is still sent for an unknown sender, and that part is
	// deliberate: it only stops the spinner on the client that pressed the
	// button, it carries no data and it cannot touch the bot state.
	authorized := query.From != nil && query.From.ID == app.Cfg().AllowedUserID
	if _, err := bot.Request(tgbotapi.NewCallback(query.ID, "")); err != nil {
		slog.Warn("Failed to acknowledge callback", "err", sanitizeErr(err))
	}
	if !authorized {
		slog.Warn("Unauthorized callback ignored")
		return
	}

	if cbRegistry.Execute(app, bot, query) {
		return
	}
	slog.Warn("Unknown callback data", "data", truncate(query.Data, 64))
}
