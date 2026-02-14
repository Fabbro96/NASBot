//go:build !fswatchdog
// +build !fswatchdog

package main

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func handleSettingsCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) bool {
	if lang, fromSettings, ok := parseLanguageCallbackData(data); ok {
		ctx.Settings.SetLanguage(lang)
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
	if strings.HasPrefix(data, "set_reports_") {
		mode := 0
		if data == "set_reports_1" {
			mode = 1
		} else if data == "set_reports_2" {
			mode = 2
		}
		ctx.Settings.mu.Lock()
		ctx.Settings.ReportMode = mode
		ctx.Settings.mu.Unlock()
		saveState(ctx)
		text, kb := getSettingsMenuText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
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
		ctx.Settings.mu.Lock()
		ctx.Settings.QuietHours.Enabled = true
		ctx.Settings.mu.Unlock()
		saveState(ctx)
		text, kb := getQuietHoursSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "quiet_disable" {
		ctx.Settings.mu.Lock()
		ctx.Settings.QuietHours.Enabled = false
		ctx.Settings.mu.Unlock()
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
		ctx.Settings.mu.Lock()
		ctx.Settings.DockerPrune.Enabled = true
		ctx.Settings.mu.Unlock()
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "prune_disable" {
		ctx.Settings.mu.Lock()
		ctx.Settings.DockerPrune.Enabled = false
		ctx.Settings.mu.Unlock()
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
		ctx.Settings.mu.Lock()
		ctx.Settings.DockerPrune.Day = day
		ctx.Settings.mu.Unlock()
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}

	return false
}

func handlePowerAndDockerCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) bool {
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
