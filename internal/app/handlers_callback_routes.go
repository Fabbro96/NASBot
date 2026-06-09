//go:build !fswatchdog
// +build !fswatchdog

package app

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleSettingsCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) bool {
	if lang, fromSettings, ok := parseLanguageCallbackData(data); ok {
		ctx.Settings.SetLanguage(lang)
		registerBotCommands(ctx, bot)
		saveState(ctx)
		if fromSettings {
			text, kb := getSettingsMenuText(ctx)
			editMessage(bot, chatID, msgID, text, &kb)
		} else {
			editMessage(bot, chatID, msgID, ctx.Tr(languageSetKey(lang)), nil)
		}
		return true
	}
	if data == "settings_change_lang" {
		msg := tgbotapi.NewEditMessageText(chatID, msgID, ctx.Tr("lang_select"))
		kb := getLanguageSelectionKeyboard(ctx, true)
		msg.ReplyMarkup = &kb
		bot.Send(msg)
		return true
	}
	if data == "settings_change_reports" {
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_enable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.ReportsEnabled = true
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_disable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.ReportsEnabled = false
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_interval_inc" || data == "report_interval_dec" {
		ctx.Settings.Mu.Lock()
		if data == "report_interval_inc" {
			ctx.Settings.ReportInterval++
		} else if ctx.Settings.ReportInterval > 1 {
			ctx.Settings.ReportInterval--
		}
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_add_time" {
		ctx.Bot.SetPendingAction("add_report_time")
		msg := tgbotapi.NewMessage(chatID, ctx.Tr("type_time_prompt"))
		safeSend(bot, msg)
		return true
	}
	if strings.HasPrefix(data, "report_del_time_") {
		var idx int
		if _, err := fmt.Sscanf(data, "report_del_time_%d", &idx); err == nil {
			ctx.Settings.Mu.Lock()
			if idx >= 0 && idx < len(ctx.Settings.ReportTimes) {
				ctx.Settings.ReportTimes = append(ctx.Settings.ReportTimes[:idx], ctx.Settings.ReportTimes[idx+1:]...)
			}
			ctx.Settings.Mu.Unlock()
			saveState(ctx)
			text, kb := getReportSettingsText(ctx)
			editMessage(bot, chatID, msgID, text, &kb)
		}
		return true
	}

	if data == "settings_change_thresholds" {
		text, kb := getThresholdsMenuText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if strings.HasPrefix(data, "thresh_edit_") {
		res := strings.TrimPrefix(data, "thresh_edit_")
		text, kb := getThresholdResourceText(ctx, res)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if strings.HasPrefix(data, "thresh_inc_") || strings.HasPrefix(data, "thresh_dec_") {
		// thresh_inc_w_cpu, thresh_dec_c_ram
		parts := strings.Split(data, "_")
		if len(parts) == 4 {
			action := parts[1] // inc / dec
			level := parts[2]  // w / c
			res := parts[3]    // cpu / ram / ssd / temp
			
			// Build the patch
			key := ""
			if res == "temp" {
				key = "temperature"
			} else {
				key = "notifications." + res
				if res == "ssd" { key = "notifications.disk_ssd" }
			}
			if level == "w" { key += ".warning_threshold" } else { key += ".critical_threshold" }
			
			// Get current value
			var currentVal float64
			cfg := ctx.Config
			switch res {
			case "cpu": if level == "w" { currentVal = cfg.Notifications.CPU.WarningThreshold } else { currentVal = cfg.Notifications.CPU.CriticalThreshold }
			case "ram": if level == "w" { currentVal = cfg.Notifications.RAM.WarningThreshold } else { currentVal = cfg.Notifications.RAM.CriticalThreshold }
			case "ssd": if level == "w" { currentVal = cfg.Notifications.DiskSSD.WarningThreshold } else { currentVal = cfg.Notifications.DiskSSD.CriticalThreshold }
			case "temp": if level == "w" { currentVal = cfg.Temperature.WarningThreshold } else { currentVal = cfg.Temperature.CriticalThreshold }
			}
			
			newVal := currentVal
			if action == "inc" { newVal += 5.0 } else { newVal -= 5.0 }
			if newVal < 0 { newVal = 0 }
			if newVal > 100 && res != "temp" { newVal = 100 }
			if newVal > 120 && res == "temp" { newVal = 120 }
			
			// Apply patch
			patch := map[string]interface{}{}
			if res == "temp" {
				patch["temperature"] = map[string]interface{}{}
				if level == "w" { patch["temperature"].(map[string]interface{})["warning_threshold"] = newVal } else { patch["temperature"].(map[string]interface{})["critical_threshold"] = newVal }
			} else {
				node := "cpu"
				if res == "ssd" { node = "disk_ssd" } else if res == "ram" { node = "ram" }
				patch["notifications"] = map[string]interface{}{
					node: map[string]interface{}{},
				}
				if level == "w" { patch["notifications"].(map[string]interface{})[node].(map[string]interface{})["warning_threshold"] = newVal } else { patch["notifications"].(map[string]interface{})[node].(map[string]interface{})["critical_threshold"] = newVal }
			}
			
			// Call applyConfigPatch directly
			applyConfigPatch(patch)
			
			text, kb := getThresholdResourceText(ctx, res)
			editMessage(bot, chatID, msgID, text, &kb)
		}
		return true
	}

	if data == "back_settings" {
		text, kb := getSettingsMenuText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "settings_change_quiet" {
		text, kb := getQuietHoursSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "quiet_enable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.QuietHours.Enabled = true
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getQuietHoursSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "quiet_disable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.QuietHours.Enabled = false
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getQuietHoursSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "settings_change_prune" {
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "prune_enable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.DockerPrune.Enabled = true
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "prune_disable" {
		ctx.Settings.Mu.Lock()
		ctx.Settings.DockerPrune.Enabled = false
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "prune_change_schedule" {
		text, kb := getPruneScheduleText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if strings.HasPrefix(data, "prune_day_") {
		day := strings.TrimPrefix(data, "prune_day_")
		ctx.Settings.Mu.Lock()
		ctx.Settings.DockerPrune.Day = day
		ctx.Settings.Mu.Unlock()
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}

	return false
}

func handlePowerAndDockerCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) bool {
	if data == "update_apply_latest" {
		applyLatestRelease(ctx, bot, chatID, msgID)
		return true
	}
	if data == "update_cancel" {
		editMessage(bot, chatID, msgID, ctx.Tr("update_cancel_text"), nil)
		return true
	}

	if data == "confirm_reboot" || data == "confirm_shutdown" {
		handlePowerConfirm(ctx, bot, chatID, msgID, data)
		return true
	}
	if data == "cancel_power" {
		editMessage(bot, chatID, msgID, ctx.Tr("cancelled"), nil)
		return true
	}
	if data == "pre_confirm_reboot" || data == "pre_confirm_shutdown" {
		action := strings.TrimPrefix(data, "pre_confirm_")
		askPowerConfirmation(ctx, bot, chatID, msgID, action)
		return true
	}
	if data == "force_reboot_now" {
		executeForcedReboot(ctx, bot, chatID, msgID, "manual-force-button")
		return true
	}

	if data == "confirm_restart_docker" {
		executeDockerServiceRestart(ctx, bot, chatID, msgID)
		return true
	}
	if data == "cancel_restart_docker" {
		editMessage(bot, chatID, msgID, ctx.Tr("docker_restart_cancel"), nil)
		return true
	}
	if data == "docker_restart_service" {
		askDockerRestartConfirmationEdit(ctx, bot, chatID, msgID)
		return true
	}
	if data == "docker_restart_all" {
		askRestartAllContainersConfirmation(ctx, bot, chatID, msgID)
		return true
	}
	if data == "confirm_restart_all" {
		executeRestartAllContainers(ctx, bot, chatID, msgID)
		return true
	}
	if data == "cancel_restart_all" {
		text, kb := getDockerMenuText(ctx)
		editMessage(bot, chatID, msgID, text, kb)
		return true
	}

	return false
}

func handleScopedCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
	if strings.HasPrefix(data, "health_") {
		handleHealthCallback(ctx, bot, query, data)
		return true
	}
	if strings.HasPrefix(data, "container_") {
		handleContainerCallback(ctx, bot, chatID, msgID, data)
		return true
	}
	return false
}

func handleMainMenuCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	var text string
	var kb *tgbotapi.InlineKeyboardMarkup

	switch data {
	case "refresh_status":
		text = getStatusText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_temp":
		text = getTempText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_docker":
		text, kb = getDockerMenuText(ctx)
	case "show_dstats":
		text = getDockerStatsText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_top":
		text = getTopProcText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_net":
		text = getNetworkText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_report":
		text = generateReport(ctx, true, nil)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	case "show_power":
		text, kb = getPowerMenuText(ctx)
	case "back_main":
		text = getStatusText(ctx)
		mainKb := getMainKeyboard(ctx)
		kb = &mainKb
	default:
		return
	}

	editMessage(bot, chatID, msgID, text, kb)
}
