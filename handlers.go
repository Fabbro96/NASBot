package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  COMMAND & CALLBACK HANDLERS
// ═══════════════════════════════════════════════════════════════════

func handleCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	args := msg.CommandArguments()

	switch msg.Command() {
	case "status", "start":
		sendWithKeyboard(bot, chatID, getStatusText())
	case "quick", "q":
		sendMarkdown(bot, chatID, getQuickText())
	case "docker":
		sendDockerMenu(bot, chatID)
	case "dstats":
		sendWithKeyboard(bot, chatID, getDockerStatsText())
	case "top":
		sendWithKeyboard(bot, chatID, getTopProcText())
	case "temp":
		sendWithKeyboard(bot, chatID, getTempText())
	case "net":
		sendMarkdown(bot, chatID, getNetworkText())
	case "logs":
		sendMarkdown(bot, chatID, getLogsText())
	case "logsearch":
		sendMarkdown(bot, chatID, getLogSearchText(args))
	case "report":
		sendMarkdown(bot, chatID, generateReport(true))
	case "container":
		handleContainerCommand(bot, chatID, args)
	case "kill":
		handleKillCommand(bot, chatID, args)
	case "ping":
		sendMarkdown(bot, chatID, getPingText())
	case "config":
		sendMarkdown(bot, chatID, getConfigText())
	case "sysinfo":
		sendMarkdown(bot, chatID, getSysInfoText())
	case "speedtest":
		handleSpeedtest(bot, chatID)
	case "diskpred", "prediction":
		sendMarkdown(bot, chatID, getDiskPredictionText())
	case "restartdocker":
		askDockerRestartConfirmation(bot, chatID)
	case "reboot":
		askPowerConfirmation(bot, chatID, 0, "reboot")
	case "shutdown":
		askPowerConfirmation(bot, chatID, 0, "shutdown")
	case "language":
		sendLanguageSelection(bot, chatID)
	case "settings":
		sendSettingsMenu(bot, chatID)
	case "help":
		sendMarkdown(bot, chatID, getHelpText())
	default:
		bot.Send(tgbotapi.NewMessage(chatID, "Hmm, I don't know that one. Try /help"))
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery) {
	bot.Request(tgbotapi.NewCallback(query.ID, ""))

	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	data := query.Data

	// Language selection
	if data == "set_lang_en" {
		currentLanguage = "en"
		saveState()
		editMessage(bot, chatID, msgID, tr("lang_set_en"), nil)
		return
	}
	if data == "set_lang_it" {
		currentLanguage = "it"
		saveState()
		editMessage(bot, chatID, msgID, tr("lang_set_it"), nil)
		return
	}

	// Settings menu
	if data == "settings_change_lang" {
		msg := tgbotapi.NewEditMessageText(chatID, msgID, tr("lang_select"))
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🇬🇧 English", "set_lang_en_settings"),
				tgbotapi.NewInlineKeyboardButtonData("🇮🇹 Italiano", "set_lang_it_settings"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
			),
		)
		msg.ReplyMarkup = &kb
		bot.Send(msg)
		return
	}
	if data == "set_lang_en_settings" {
		currentLanguage = "en"
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "set_lang_it_settings" {
		currentLanguage = "it"
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_reports" {
		text, kb := getReportSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if strings.HasPrefix(data, "set_reports_") {
		mode := 0
		if data == "set_reports_1" {
			mode = 1
		} else if data == "set_reports_2" {
			mode = 2
		}
		reportMode = mode
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "back_settings" {
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_quiet" {
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "quiet_enable" {
		quietHoursEnabled = true
		saveState()
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "quiet_disable" {
		quietHoursEnabled = false
		saveState()
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_prune" {
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_enable" {
		dockerPruneEnabled = true
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_disable" {
		dockerPruneEnabled = false
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_change_schedule" {
		text, kb := getPruneScheduleText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if strings.HasPrefix(data, "prune_day_") {
		day := strings.TrimPrefix(data, "prune_day_")
		dockerPruneDay = day
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}

	// Power confirmation management
	if data == "confirm_reboot" || data == "confirm_shutdown" {
		handlePowerConfirm(bot, chatID, msgID, data)
		return
	}
	if data == "cancel_power" {
		editMessage(bot, chatID, msgID, "❌ Cancelled", nil)
		return
	}
	// Power menu pre-confirmation management
	if data == "pre_confirm_reboot" || data == "pre_confirm_shutdown" {
		action := strings.TrimPrefix(data, "pre_confirm_")
		askPowerConfirmation(bot, chatID, msgID, action)
		return
	}

	// Docker service restart confirmation
	if data == "confirm_restart_docker" {
		executeDockerServiceRestart(bot, chatID, msgID)
		return
	}
	if data == "cancel_restart_docker" {
		editMessage(bot, chatID, msgID, "❌ Docker restart cancelled", nil)
		return
	}

	// Container actions management
	if strings.HasPrefix(data, "container_") {
		handleContainerCallback(bot, chatID, msgID, data)
		return
	}

	// Normal navigation
	var text string
	var kb *tgbotapi.InlineKeyboardMarkup
	switch data {
	case "refresh_status":
		text = getStatusText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_temp":
		text = getTempText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_docker":
		text, kb = getDockerMenuText()
	case "show_dstats":
		text = getDockerStatsText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_top":
		text = getTopProcText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_net":
		text = getNetworkText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_report":
		text = generateReport(true)
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_power":
		text, kb = getPowerMenuText()
	case "back_main":
		text = getStatusText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	default:
		return
	}
	editMessage(bot, chatID, msgID, text, kb)
}

// ═══════════════════════════════════════════════════════════════════
//  SETTINGS MENUS
// ═══════════════════════════════════════════════════════════════════

func sendLanguageSelection(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, tr("lang_select"))
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🇬🇧 English", "set_lang_en"),
			tgbotapi.NewInlineKeyboardButtonData("🇮🇹 Italiano", "set_lang_it"),
		),
	)
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

func sendSettingsMenu(bot *tgbotapi.BotAPI, chatID int64) {
	text, kb := getSettingsMenuText()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

func getSettingsMenuText() (string, tgbotapi.InlineKeyboardMarkup) {
	langName := "English 🇬🇧"
	if currentLanguage == "it" {
		langName = "Italiano 🇮🇹"
	}

	reportText := tr("report_disabled")
	if reportMode == 1 {
		reportText = tr("report_once")
	} else if reportMode == 2 {
		reportText = tr("report_twice")
	}

	quietText := tr("quiet_disabled")
	if quietHoursEnabled {
		quietText = fmt.Sprintf("%02d:%02d - %02d:%02d", quietStartHour, quietStartMinute, quietEndHour, quietEndMinute)
	}

	pruneText := tr("prune_disabled")
	if dockerPruneEnabled {
		pruneText = fmt.Sprintf("%s %02d:00", tr(dockerPruneDay), dockerPruneHour)
	}

	text := fmt.Sprintf("%s\n\n", tr("settings_title"))
	text += fmt.Sprintf("🌐 %s: %s\n", tr("settings_lang"), langName)
	text += fmt.Sprintf("📨 %s: %s\n", tr("settings_reports"), reportText)
	text += fmt.Sprintf("🌙 %s: %s\n", tr("settings_quiet"), quietText)
	text += fmt.Sprintf("🧹 %s: %s\n", tr("settings_prune"), pruneText)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 "+tr("settings_lang"), "settings_change_lang"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📨 "+tr("settings_reports"), "settings_change_reports"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌙 "+tr("settings_quiet"), "settings_change_quiet"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧹 "+tr("settings_prune"), "settings_change_prune"),
		),
	)

	return text, kb
}

func getReportSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "📨 *Report Giornalieri*\n\nSeleziona la frequenza dei report automatici:"
	} else {
		text = "📨 *Daily Reports*\n\nSelect automatic report frequency:"
	}

	checkDisabled := " "
	checkOnce := " "
	checkTwice := " "

	if reportMode == 0 {
		checkDisabled = "✓"
	} else if reportMode == 1 {
		checkOnce = "✓"
	} else if reportMode == 2 {
		checkTwice = "✓"
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkDisabled+" "+tr("report_disabled"), "set_reports_0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkOnce+" "+tr("report_once"), "set_reports_1"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkTwice+" "+tr("report_twice"), "set_reports_2"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
		),
	)

	return text, kb
}

func getQuietHoursSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "🌙 *Ore Silenziose*\n\nDurante questo periodo non riceverai notifiche.\n\n"
	} else {
		text = "🌙 *Quiet Hours*\n\nNo notifications during this period.\n\n"
	}

	if quietHoursEnabled {
		text += fmt.Sprintf("Attualmente: %02d:%02d - %02d:%02d\n", quietStartHour, quietStartMinute, quietEndHour, quietEndMinute)
	} else {
		text += tr("disabled") + "\n"
	}

	toggleText := tr("enable")
	toggleData := "quiet_enable"
	if quietHoursEnabled {
		toggleText = tr("disable")
		toggleData = "quiet_disable"
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
		),
	)

	return text, kb
}

func getDockerPruneSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "🧹 *Pulizia Docker*\n\nPulizia automatica delle immagini inutilizzate.\n\n"
	} else {
		text = "🧹 *Docker Prune*\n\nAutomatic cleanup of unused images.\n\n"
	}

	if dockerPruneEnabled {
		dayName := tr(dockerPruneDay)
		text += fmt.Sprintf("%s: %s %02d:00\n", tr("schedule"), dayName, dockerPruneHour)
	} else {
		text += tr("disabled") + "\n"
	}

	toggleText := tr("enable")
	toggleData := "prune_enable"
	if dockerPruneEnabled {
		toggleText = tr("disable")
		toggleData = "prune_disable"
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData)},
	}

	if dockerPruneEnabled {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(tr("schedule"), "prune_change_schedule"),
		})
	}

	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
	})

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)

	return text, kb
}

func getPruneScheduleText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "📅 *Programmazione Pulizia*\n\nSeleziona il giorno:"
	} else {
		text = "📅 *Prune Schedule*\n\nSelect day:"
	}

	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	var rows [][]tgbotapi.InlineKeyboardButton

	for _, day := range days {
		check := " "
		if dockerPruneDay == day {
			check = "✓"
		}
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(check+" "+tr(day), "prune_day_"+day),
		})
	}

	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "settings_change_prune"),
	})

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)

	return text, kb
}

// ═══════════════════════════════════════════════════════════════════
//  SPEEDTEST
// ═══════════════════════════════════════════════════════════════════

func handleSpeedtest(bot *tgbotapi.BotAPI, chatID int64) {
	if _, err := exec.LookPath("speedtest-cli"); err != nil {
		sendMarkdown(bot, chatID, "❌ `speedtest-cli` not installed.\n\nInstall it with:\n`sudo apt install speedtest-cli`")
		return
	}

	msg := tgbotapi.NewMessage(chatID, "🚀 Running speed test... (this may take a minute)")
	sent, _ := bot.Send(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "speedtest-cli", "--simple")
	output, err := cmd.CombinedOutput()

	var resultText string
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resultText = "⏱ Speed test timed out"
		} else {
			resultText = fmt.Sprintf("❌ Speed test failed:\n`%s`", err.Error())
		}
	} else {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var ping, download, upload string
		for _, line := range lines {
			if strings.HasPrefix(line, "Ping:") {
				ping = strings.TrimPrefix(line, "Ping: ")
			} else if strings.HasPrefix(line, "Download:") {
				download = strings.TrimPrefix(line, "Download: ")
			} else if strings.HasPrefix(line, "Upload:") {
				upload = strings.TrimPrefix(line, "Upload: ")
			}
		}

		resultText = fmt.Sprintf("🚀 *Speed Test Results*\n\n"+
			"📡 Ping: `%s`\n"+
			"⬇️ Download: `%s`\n"+
			"⬆️ Upload: `%s`",
			ping, download, upload)
	}

	edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID, resultText)
	edit.ParseMode = "Markdown"
	bot.Send(edit)
}

// ═══════════════════════════════════════════════════════════════════
//  POWER MANAGEMENT
// ═══════════════════════════════════════════════════════════════════

func getPowerMenuText() (string, *tgbotapi.InlineKeyboardMarkup) {
	text := "⚡ *Power Management*\n\nBe careful, these actions affect the physical server."
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Reboot NAS", "pre_confirm_reboot"),
			tgbotapi.NewInlineKeyboardButtonData("🛑 Shutdown NAS", "pre_confirm_shutdown"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "back_main"),
		),
	)
	return text, &kb
}

func askPowerConfirmation(bot *tgbotapi.BotAPI, chatID int64, msgID int, action string) {
	pendingActionMutex.Lock()
	pendingAction = action
	pendingActionMutex.Unlock()

	question := "🔄 *Reboot* the NAS?"
	if action == "shutdown" {
		question = "⚠️ *Shut down* the NAS?"
	}
	question += "\n\n_Are you sure?_"

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, do it", "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_power"),
		),
	)

	if msgID > 0 {
		editMessage(bot, chatID, msgID, question, &kb)
	} else {
		msg := tgbotapi.NewMessage(chatID, question)
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = kb
		bot.Send(msg)
	}
}

func handlePowerConfirm(bot *tgbotapi.BotAPI, chatID int64, msgID int, data string) {
	pendingActionMutex.Lock()
	action := pendingAction
	pendingAction = ""
	pendingActionMutex.Unlock()

	expectedAction := strings.TrimPrefix(data, "confirm_")
	if action == "" || action != expectedAction {
		editMessage(bot, chatID, msgID, "_Session expired — try again_", nil)
		return
	}

	cmd := "reboot"
	actionMsg := "Rebooting now..."
	if action == "shutdown" {
		cmd = "poweroff"
		actionMsg = "Shutting down... See you later!"
	}

	editMessage(bot, chatID, msgID, actionMsg, nil)

	go func() {
		time.Sleep(1 * time.Second)
		exec.Command(cmd).Run()
	}()
}

// ═══════════════════════════════════════════════════════════════════
//  MESSAGE HELPERS
// ═══════════════════════════════════════════════════════════════════

func sendMarkdown(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func sendWithKeyboard(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = getMainKeyboard()
	bot.Send(msg)
}

func editMessage(bot *tgbotapi.BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "Markdown"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard
	}
	bot.Send(edit)
}

func getMainKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData("🌡 Temp", "show_temp"),
			tgbotapi.NewInlineKeyboardButtonData("🌐 Net", "show_net"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Docker", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("📊 D-Stats", "show_dstats"),
			tgbotapi.NewInlineKeyboardButtonData("🔥 Top Proc", "show_top"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Power Actions", "show_power"),
		),
	)
}
