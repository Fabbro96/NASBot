//go:build !fswatchdog
// +build !fswatchdog

package app

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func sendLanguageSelection(ctx *AppContext, bot BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, ctx.Tr("lang_select"))
	kb := getLanguageSelectionKeyboard(ctx, false)
	msg.ReplyMarkup = kb
	safeSend(bot, msg)
}

type supportedLanguage struct {
	Code    string
	Flag    string
	NameKey string
}

var supportedLanguages = []supportedLanguage{
	{Code: "en", Flag: "🇬🇧", NameKey: "lang_name_en"},
	{Code: "it", Flag: "🇮🇹", NameKey: "lang_name_it"},
	{Code: "es", Flag: "🇪🇸", NameKey: "lang_name_es"},
	{Code: "de", Flag: "🇩🇪", NameKey: "lang_name_de"},
	{Code: "zh", Flag: "🇨🇳", NameKey: "lang_name_zh"},
	{Code: "uk", Flag: "🇺🇦", NameKey: "lang_name_uk"},
}

var supportedLanguageByCode = func() map[string]supportedLanguage {
	byCode := make(map[string]supportedLanguage, len(supportedLanguages))
	for _, lang := range supportedLanguages {
		byCode[lang.Code] = lang
	}
	return byCode
}()

func getLanguageSelectionKeyboard(ctx *AppContext, forSettings bool) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, 4)

	for i := 0; i < len(supportedLanguages); i += 2 {
		row := make([]tgbotapi.InlineKeyboardButton, 0, 2)
		for j := i; j < i+2 && j < len(supportedLanguages); j++ {
			lang := supportedLanguages[j]
			callback := "set_lang_" + lang.Code
			if forSettings {
				callback += "_settings"
			}
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(lang.Flag+" "+ctx.Tr(lang.NameKey), callback))
		}
		rows = append(rows, row)
	}

	if forSettings {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		))
	}

	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func parseLanguageCallbackData(data string) (lang string, fromSettings bool, ok bool) {
	if !strings.HasPrefix(data, "set_lang_") {
		return "", false, false
	}

	base := strings.TrimPrefix(data, "set_lang_")
	if strings.HasSuffix(base, "_settings") {
		base = strings.TrimSuffix(base, "_settings")
		fromSettings = true
	}

	if _, exists := supportedLanguageByCode[base]; exists {
		return base, fromSettings, true
	}
	return "", false, false
}

func languageSetKey(lang string) string {
	if _, exists := supportedLanguageByCode[lang]; exists {
		return "lang_set_" + lang
	}
	return "lang_set_en"
}

func languageNameKey(lang string) string {
	if meta, exists := supportedLanguageByCode[lang]; exists {
		return meta.NameKey
	}
	return "lang_name_en"
}

func languageFlag(lang string) string {
	if meta, exists := supportedLanguageByCode[lang]; exists {
		return meta.Flag
	}
	return "🌐"
}

func sendSettingsMenu(ctx *AppContext, bot BotAPI, chatID int64) {
	text, kb := getSettingsMenuText(ctx)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	safeSend(bot, msg)
}

func getSettingsMenuText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	currentLang := ctx.Settings.GetLanguage()
	langName := ctx.Tr(languageNameKey(currentLang)) + " " + languageFlag(currentLang)

	repEnabled, repInterval, repTimes := ctx.Settings.GetReportsSettings()
	reportText := ctx.Tr("report_disabled")
	if repEnabled {
		reportText = fmt.Sprintf(ctx.Tr("report_enabled_fmt"), repInterval, len(repTimes))
	}

	ctx.Settings.Mu.RLock()
	quietEnabled := ctx.Settings.QuietHours.Enabled
	qStartH := ctx.Settings.QuietHours.Start.Hour
	qStartM := ctx.Settings.QuietHours.Start.Minute
	qEndH := ctx.Settings.QuietHours.End.Hour
	qEndM := ctx.Settings.QuietHours.End.Minute
	pruneEnabled := ctx.Settings.DockerPrune.Enabled
	pruneDay := ctx.Settings.DockerPrune.Day
	pruneHour := ctx.Settings.DockerPrune.Hour
	ctx.Settings.Mu.RUnlock()

	quietText := ctx.Tr("quiet_disabled")
	if quietEnabled {
		quietText = fmt.Sprintf("%02d:%02d - %02d:%02d", qStartH, qStartM, qEndH, qEndM)
	}

	pruneText := ctx.Tr("prune_disabled")
	if pruneEnabled {
		pruneText = fmt.Sprintf("%s %02d:00", ctx.Tr(pruneDay), pruneHour)
	}

	text := fmt.Sprintf("%s\n\n", ctx.Tr("settings_title"))
	text += fmt.Sprintf("🌐 %s: %s\n", ctx.Tr("settings_lang"), langName)
	text += fmt.Sprintf("📨 %s: %s\n", ctx.Tr("settings_reports"), reportText)
	text += fmt.Sprintf("🌙 %s: %s\n", ctx.Tr("settings_quiet"), quietText)
	text += fmt.Sprintf("🧹 %s: %s\n", ctx.Tr("settings_prune"), pruneText)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 "+ctx.Tr("settings_lang"), "settings_change_lang"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📨 "+ctx.Tr("settings_reports"), "settings_change_reports"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌙 "+ctx.Tr("settings_quiet"), "settings_change_quiet"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧹 "+ctx.Tr("settings_prune"), "settings_change_prune"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚠️ "+ctx.Tr("settings_thresholds"), "settings_change_thresholds"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💻 "+ctx.Tr("settings_wol"), "settings_change_wol"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📦 "+ctx.Tr("settings_backup"), "settings_change_backup"),
		),
	)
	return text, kb
}

func getReportSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("report_settings_title")
	enabled, interval, times := ctx.Settings.GetReportsSettings()
	
	if !enabled {
		text += "\n" + ctx.Tr("status_disabled")
	} else {
		text += "\n" + fmt.Sprintf(ctx.Tr("report_freq_fmt"), interval)
	}

	rows := [][]tgbotapi.InlineKeyboardButton{}

	toggleText := ctx.Tr("enable")
	toggleData := "report_enable"
	if enabled {
		toggleText = ctx.Tr("disable")
		toggleData = "report_disable"
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData),
	))

	if enabled {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➖", "report_interval_dec"),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf(ctx.Tr("report_interval_btn"), interval), "noop"),
			tgbotapi.NewInlineKeyboardButtonData("➕", "report_interval_inc"),
		))

		timeRow := []tgbotapi.InlineKeyboardButton{}
		for i, tp := range times {
			btnText := fmt.Sprintf("✖ %02d:%02d", tp.Hour, tp.Minute)
			btnData := fmt.Sprintf("report_del_time_%d", i)
			timeRow = append(timeRow, tgbotapi.NewInlineKeyboardButtonData(btnText, btnData))
			if len(timeRow) == 2 {
				rows = append(rows, timeRow)
				timeRow = []tgbotapi.InlineKeyboardButton{}
			}
		}
		if len(timeRow) > 0 {
			rows = append(rows, timeRow)
		}

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ "+ctx.Tr("add_time"), "report_add_time"),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return text, kb
}

func getThresholdsMenuText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("thresholds_settings_title")
	
	// Read current thresholds from Config
	cfg := ctx.Config
	cpuW, cpuC := cfg.Notifications.CPU.WarningThreshold, cfg.Notifications.CPU.CriticalThreshold
	ramW, ramC := cfg.Notifications.RAM.WarningThreshold, cfg.Notifications.RAM.CriticalThreshold
	ssdW, ssdC := cfg.Notifications.DiskSSD.WarningThreshold, cfg.Notifications.DiskSSD.CriticalThreshold
	tempW, tempC := cfg.Temperature.WarningThreshold, cfg.Temperature.CriticalThreshold

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🌡 CPU: %.0f%% / %.0f%%", cpuW, cpuC), "thresh_edit_cpu"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🧠 RAM: %.0f%% / %.0f%%", ramW, ramC), "thresh_edit_ram"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("💾 Disk SSD: %.0f%% / %.0f%%", ssdW, ssdC), "thresh_edit_ssd"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("🔥 Temp: %.0f°C / %.0f°C", tempW, tempC), "thresh_edit_temp"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		),
	}
	
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func getThresholdResourceText(ctx *AppContext, resource string) (string, tgbotapi.InlineKeyboardMarkup) {
	text := fmt.Sprintf(ctx.Tr("thresh_edit_title"), strings.ToUpper(resource))
	
	cfg := ctx.Config
	var w, c float64
	var unit string
	
	switch resource {
	case "cpu":
		w, c = cfg.Notifications.CPU.WarningThreshold, cfg.Notifications.CPU.CriticalThreshold
		unit = "%"
	case "ram":
		w, c = cfg.Notifications.RAM.WarningThreshold, cfg.Notifications.RAM.CriticalThreshold
		unit = "%"
	case "ssd":
		w, c = cfg.Notifications.DiskSSD.WarningThreshold, cfg.Notifications.DiskSSD.CriticalThreshold
		unit = "%"
	case "temp":
		w, c = cfg.Temperature.WarningThreshold, cfg.Temperature.CriticalThreshold
		unit = "°C"
	}
	
	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➖ Warning", fmt.Sprintf("thresh_dec_w_%s", resource)),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%.0f%s", w, unit), "noop"),
			tgbotapi.NewInlineKeyboardButtonData("➕ Warning", fmt.Sprintf("thresh_inc_w_%s", resource)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➖ Critical", fmt.Sprintf("thresh_dec_c_%s", resource)),
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%.0f%s", c, unit), "noop"),
			tgbotapi.NewInlineKeyboardButtonData("➕ Critical", fmt.Sprintf("thresh_inc_c_%s", resource)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "settings_change_thresholds"),
		),
	}
	
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func getQuietHoursSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("quiet_settings_title")
	ctx.Settings.Mu.RLock()
	enabled := ctx.Settings.QuietHours.Enabled
	startH := ctx.Settings.QuietHours.Start.Hour
	startM := ctx.Settings.QuietHours.Start.Minute
	endH := ctx.Settings.QuietHours.End.Hour
	endM := ctx.Settings.QuietHours.End.Minute
	ctx.Settings.Mu.RUnlock()
	if enabled {
		text += fmt.Sprintf(ctx.Tr("quiet_currently"), startH, startM, endH, endM)
	} else {
		text += ctx.Tr("disabled") + "\n"
	}
	toggleText := ctx.Tr("enable")
	toggleData := "quiet_enable"
	if enabled {
		toggleText = ctx.Tr("disable")
		toggleData = "quiet_disable"
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		),
	)
	return text, kb
}

func getDockerPruneSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("prune_settings_title")
	ctx.Settings.Mu.RLock()
	enabled := ctx.Settings.DockerPrune.Enabled
	day := ctx.Settings.DockerPrune.Day
	hour := ctx.Settings.DockerPrune.Hour
	ctx.Settings.Mu.RUnlock()
	if enabled {
		dayName := ctx.Tr(day)
		text += fmt.Sprintf("%s: %s %02d:00\n", ctx.Tr("schedule"), dayName, hour)
	} else {
		text += ctx.Tr("disabled") + "\n"
	}
	toggleText := ctx.Tr("enable")
	toggleData := "prune_enable"
	if enabled {
		toggleText = ctx.Tr("disable")
		toggleData = "prune_disable"
	}
	rows := [][]tgbotapi.InlineKeyboardButton{{tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData)}}
	if enabled {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("schedule"), "prune_change_schedule")})
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings")})
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return text, kb
}

func getPruneScheduleText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("prune_schedule_title")
	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	ctx.Settings.Mu.RLock()
	currentDay := ctx.Settings.DockerPrune.Day
	ctx.Settings.Mu.RUnlock()
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, day := range days {
		check := " "
		if currentDay == day {
			check = "✓"
		}
		rows = append(rows, []tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData(check+" "+ctx.Tr(day), "prune_day_"+day)})
	}
	rows = append(rows, []tgbotapi.InlineKeyboardButton{tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "settings_change_prune")})
	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return text, kb
}

func getWOLSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := "💻 *Wake-on-LAN (WOL)*\n\nInserisci il MAC Address del computer che vuoi accendere usando il comando `/wake`."
	
	mac := ctx.Config.WakeOnLan.MacAddress
	if mac == "" {
		text += "\n\nAttualmente: _Non configurato_"
	} else {
		text += fmt.Sprintf("\n\nAttualmente: `%s`", mac)
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Modifica MAC", "wol_set_mac"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		),
	}
	
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func getBackupSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := "📦 *Backup Configurazioni*\n\nSeleziona il Telegram User ID (Chat ID) a cui verranno inviati i backup generati col comando `/backup`."
	
	uid := ctx.Config.Backup.TargetUserID
	if uid == 0 {
		text += fmt.Sprintf("\n\nAttualmente: _Default_ (`%d`)", ctx.Config.AllowedUserID)
	} else {
		text += fmt.Sprintf("\n\nAttualmente: `%d`", uid)
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 Modifica ID Destinatario", "backup_set_uid"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		),
	}
	
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}
