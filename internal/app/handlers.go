//go:build !fswatchdog
// +build !fswatchdog

package app

import (
	"fmt"
	"log/slog"
	"strings"

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

	if action == "set_backup_uid" {
		app.Bot.ClearPendingAction()
		uid := int64(0)
		if _, err := fmt.Sscanf(strings.TrimSpace(msg.Text), "%d", &uid); err != nil {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "❌ Formato non valido. Devi inserire un numero (es. `123456789`)."))
			return
		}

		patch := map[string]interface{}{
			"backup": map[string]interface{}{
				"target_user_id": uid,
			},
		}
		applyConfigPatch(patch)

		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "✅ ID Destinatario Backup aggiornato con successo."))
		text, kb := getBackupSettingsText(app)
		msgSettings := tgbotapi.NewMessage(msg.Chat.ID, text)
		msgSettings.ParseMode = "Markdown"
		msgSettings.ReplyMarkup = kb
		safeSend(bot, msgSettings)
		return
	}

	if strings.HasPrefix(action, "thresh_custom_") {
		app.Bot.ClearPendingAction()

		var val float64
		if _, err := fmt.Sscanf(strings.TrimSpace(msg.Text), "%f", &val); err != nil || val < 0 || val > 100 {
			safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "❌ Invalid value. Must be a number between 0 and 100."))
			return
		}

		var level, res string
		if strings.HasPrefix(action, "thresh_custom_w_") {
			level, res = "w", strings.TrimPrefix(action, "thresh_custom_w_")
		} else if strings.HasPrefix(action, "thresh_custom_c_") {
			level, res = "c", strings.TrimPrefix(action, "thresh_custom_c_")
		} else {
			return
		}

		cfg := app.Config
		isDisk := strings.HasPrefix(res, "disk:")
		var mount string

		app.Settings.Mu.Lock()
		if isDisk {
			mount = strings.TrimPrefix(res, "disk:")
			if cfg.Notifications.SecondaryDisks == nil {
				cfg.Notifications.SecondaryDisks = make(map[string]ResourceConfig)
			}
			diskCfg, ok := cfg.Notifications.SecondaryDisks[mount]
			if !ok {
				diskCfg = ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95}
			}
			if level == "w" {
				diskCfg.WarningThreshold = val
			} else {
				diskCfg.CriticalThreshold = val
			}
			cfg.Notifications.SecondaryDisks[mount] = diskCfg
		} else {
			switch res {
			case "cpu":
				if level == "w" {
					cfg.Notifications.CPU.WarningThreshold = val
				} else {
					cfg.Notifications.CPU.CriticalThreshold = val
				}
			case "ram":
				if level == "w" {
					cfg.Notifications.RAM.WarningThreshold = val
				} else {
					cfg.Notifications.RAM.CriticalThreshold = val
				}
			case "ssd":
				if level == "w" {
					cfg.Notifications.DiskSSD.WarningThreshold = val
				} else {
					cfg.Notifications.DiskSSD.CriticalThreshold = val
				}
			case "temp":
				if level == "w" {
					cfg.Temperature.WarningThreshold = val
				} else {
					cfg.Temperature.CriticalThreshold = val
				}
			}
		}
		app.Settings.Mu.Unlock()

		patch := map[string]interface{}{}
		if isDisk {
			patch["notifications"] = map[string]interface{}{
				"secondary_disks": cfg.Notifications.SecondaryDisks,
			}
		} else {
			switch res {
			case "cpu":
				patch["notifications"] = map[string]interface{}{"cpu": cfg.Notifications.CPU}
			case "ram":
				patch["notifications"] = map[string]interface{}{"ram": cfg.Notifications.RAM}
			case "ssd":
				patch["notifications"] = map[string]interface{}{"disk_ssd": cfg.Notifications.DiskSSD}
			case "temp":
				patch["temperature"] = cfg.Temperature
			}
		}
		applyConfigPatch(patch)

		safeSend(bot, tgbotapi.NewMessage(msg.Chat.ID, "✅ Threshold updated successfully."))
		text, kb := getThresholdResourceText(app, res)
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
