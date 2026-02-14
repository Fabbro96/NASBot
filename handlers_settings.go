//go:build !fswatchdog
// +build !fswatchdog

package main

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

	reportMode := ctx.Settings.GetReportMode()
	reportText := ctx.Tr("report_disabled")
	if reportMode == 1 {
		reportText = ctx.Tr("report_once")
	} else if reportMode == 2 {
		reportText = ctx.Tr("report_twice")
	}

	ctx.Settings.mu.RLock()
	quietEnabled := ctx.Settings.QuietHours.Enabled
	qStartH := ctx.Settings.QuietHours.Start.Hour
	qStartM := ctx.Settings.QuietHours.Start.Minute
	qEndH := ctx.Settings.QuietHours.End.Hour
	qEndM := ctx.Settings.QuietHours.End.Minute
	pruneEnabled := ctx.Settings.DockerPrune.Enabled
	pruneDay := ctx.Settings.DockerPrune.Day
	pruneHour := ctx.Settings.DockerPrune.Hour
	ctx.Settings.mu.RUnlock()

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
	)
	return text, kb
}

func getReportSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("report_settings_title")
	checkDisabled := " "
	checkOnce := " "
	checkTwice := " "
	mode := ctx.Settings.GetReportMode()
	if mode == 0 {
		checkDisabled = "✓"
	} else if mode == 1 {
		checkOnce = "✓"
	} else if mode == 2 {
		checkTwice = "✓"
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkDisabled+" "+ctx.Tr("report_disabled"), "set_reports_0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkOnce+" "+ctx.Tr("report_once"), "set_reports_1"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkTwice+" "+ctx.Tr("report_twice"), "set_reports_2"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_settings"),
		),
	)
	return text, kb
}

func getQuietHoursSettingsText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("quiet_settings_title")
	ctx.Settings.mu.RLock()
	enabled := ctx.Settings.QuietHours.Enabled
	startH := ctx.Settings.QuietHours.Start.Hour
	startM := ctx.Settings.QuietHours.Start.Minute
	endH := ctx.Settings.QuietHours.End.Hour
	endM := ctx.Settings.QuietHours.End.Minute
	ctx.Settings.mu.RUnlock()
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
	ctx.Settings.mu.RLock()
	enabled := ctx.Settings.DockerPrune.Enabled
	day := ctx.Settings.DockerPrune.Day
	hour := ctx.Settings.DockerPrune.Hour
	ctx.Settings.mu.RUnlock()
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
	ctx.Settings.mu.RLock()
	currentDay := ctx.Settings.DockerPrune.Day
	ctx.Settings.mu.RUnlock()
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
