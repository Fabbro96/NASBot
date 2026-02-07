//go:build !fswatchdog
// +build !fswatchdog

package main

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

func handleCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
    if app == nil {
        slog.Error("App context is nil in handleCommand")
        return
    }
    if cmdRegistry.Execute(app, bot, msg) {
        return
    }
    bot.Send(tgbotapi.NewMessage(msg.Chat.ID, app.Tr("unknown_command")))
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery) {
    if app == nil {
        slog.Error("App context is nil in handleCallback")
        return
    }
    bot.Request(tgbotapi.NewCallback(query.ID, ""))
    chatID := query.Message.Chat.ID
    msgID := query.Message.MessageID
    data := query.Data

    if data == "set_lang_en" {
        app.Settings.SetLanguage("en")
        saveState(app)
        editMessage(bot, chatID, msgID, app.Tr("lang_set_en"), nil)
        return
    }
    if data == "set_lang_it" {
        app.Settings.SetLanguage("it")
        saveState(app)
        editMessage(bot, chatID, msgID, app.Tr("lang_set_it"), nil)
        return
    }
    if data == "settings_change_lang" {
        msg := tgbotapi.NewEditMessageText(chatID, msgID, app.Tr("lang_select"))
        kb := tgbotapi.NewInlineKeyboardMarkup(
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData("🇬🇧 "+app.Tr("lang_name_en"), "set_lang_en_settings"),
                tgbotapi.NewInlineKeyboardButtonData("🇮🇹 "+app.Tr("lang_name_it"), "set_lang_it_settings"),
            ),
            tgbotapi.NewInlineKeyboardRow(
                tgbotapi.NewInlineKeyboardButtonData(app.Tr("back"), "back_settings"),
            ),
        )
        msg.ReplyMarkup = &kb
        bot.Send(msg)
        return
    }
    if data == "set_lang_en_settings" {
        app.Settings.SetLanguage("en")
        saveState(app)
        text, kb := getSettingsMenuText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "set_lang_it_settings" {
        app.Settings.SetLanguage("it")
        saveState(app)
        text, kb := getSettingsMenuText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "settings_change_reports" {
        text, kb := getReportSettingsText(app)
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
        app.Settings.mu.Lock()
        app.Settings.ReportMode = mode
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getSettingsMenuText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "back_settings" {
        text, kb := getSettingsMenuText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "settings_change_quiet" {
        text, kb := getQuietHoursSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "quiet_enable" {
        app.Settings.mu.Lock()
        app.Settings.QuietHours.Enabled = true
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getQuietHoursSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "quiet_disable" {
        app.Settings.mu.Lock()
        app.Settings.QuietHours.Enabled = false
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getQuietHoursSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "settings_change_prune" {
        text, kb := getDockerPruneSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "prune_enable" {
        app.Settings.mu.Lock()
        app.Settings.DockerPrune.Enabled = true
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getDockerPruneSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "prune_disable" {
        app.Settings.mu.Lock()
        app.Settings.DockerPrune.Enabled = false
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getDockerPruneSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if data == "prune_change_schedule" {
        text, kb := getPruneScheduleText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }
    if strings.HasPrefix(data, "prune_day_") {
        day := strings.TrimPrefix(data, "prune_day_")
        app.Settings.mu.Lock()
        app.Settings.DockerPrune.Day = day
        app.Settings.mu.Unlock()
        saveState(app)
        text, kb := getDockerPruneSettingsText(app)
        editMessage(bot, chatID, msgID, text, &kb)
        return
    }

    if data == "confirm_reboot" || data == "confirm_shutdown" {
        handlePowerConfirm(app, bot, chatID, msgID, data)
        return
    }
    if data == "cancel_power" {
        editMessage(bot, chatID, msgID, app.Tr("cancelled"), nil)
        return
    }
    if data == "pre_confirm_reboot" || data == "pre_confirm_shutdown" {
        action := strings.TrimPrefix(data, "pre_confirm_")
        askPowerConfirmation(app, bot, chatID, msgID, action)
        return
    }

    if data == "confirm_restart_docker" {
        executeDockerServiceRestart(app, bot, chatID, msgID)
        return
    }
    if data == "cancel_restart_docker" {
        editMessage(bot, chatID, msgID, app.Tr("docker_restart_cancel"), nil)
        return
    }

    if data == "docker_restart_service" {
        askDockerRestartConfirmationEdit(app, bot, chatID, msgID)
        return
    }
    if data == "docker_restart_all" {
        askRestartAllContainersConfirmation(app, bot, chatID, msgID)
        return
    }
    if data == "confirm_restart_all" {
        executeRestartAllContainers(app, bot, chatID, msgID)
        return
    }
    if data == "cancel_restart_all" {
        text, kb := getDockerMenuText(app)
        editMessage(bot, chatID, msgID, text, kb)
        return
    }

    if strings.HasPrefix(data, "health_") {
        handleHealthCallback(app, bot, query, data)
        return
    }
    if strings.HasPrefix(data, "container_") {
        handleContainerCallback(app, bot, chatID, msgID, data)
        return
    }

    var text string
    var kb *tgbotapi.InlineKeyboardMarkup
    switch data {
    case "refresh_status":
        text = getStatusText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_temp":
        text = getTempText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_docker":
        text, kb = getDockerMenuText(app)
    case "show_dstats":
        text = getDockerStatsText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_top":
        text = getTopProcText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_net":
        text = getNetworkText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_report":
        text = generateReport(app, true, nil)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    case "show_power":
        text, kb = getPowerMenuText(app)
    case "back_main":
        text = getStatusText(app)
        mainKb := getMainKeyboard(app)
        kb = &mainKb
    default:
        return
    }
    editMessage(bot, chatID, msgID, text, kb)
}

func sendLanguageSelection(ctx *AppContext, bot BotAPI, chatID int64) {
    msg := tgbotapi.NewMessage(chatID, ctx.Tr("lang_select"))
    kb := tgbotapi.NewInlineKeyboardMarkup(
        tgbotapi.NewInlineKeyboardRow(
            tgbotapi.NewInlineKeyboardButtonData("🇬🇧 "+ctx.Tr("lang_name_en"), "set_lang_en"),
            tgbotapi.NewInlineKeyboardButtonData("🇮🇹 "+ctx.Tr("lang_name_it"), "set_lang_it"),
        ),
    )
    msg.ReplyMarkup = kb
    safeSend(bot, msg)
}

func sendSettingsMenu(ctx *AppContext, bot BotAPI, chatID int64) {
    text, kb := getSettingsMenuText(ctx)
    msg := tgbotapi.NewMessage(chatID, text)
    msg.ParseMode = "Markdown"
    msg.ReplyMarkup = kb
    safeSend(bot, msg)
}

func getSettingsMenuText(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
    langName := ctx.Tr("lang_name_en") + " 🇬🇧"
    if ctx.Settings.GetLanguage() == "it" {
        langName = ctx.Tr("lang_name_it") + " 🇮🇹"
    }

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
