package app

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// maxPIDLen bounds a PID taken from callback data. Linux caps pid_t at 2^22, and
// the bound keeps `proc_kill_<pid>` inside Telegram's 64 byte callback limit.
const maxPIDLen = 7

// thresholdResourceKinds is the closed set of resources the thresholds menu can
// address, besides disks.
//
// The set is closed on purpose: with an open set a forged `thresh_inc_w_foo`
// built an empty patch, applyConfigPatch accepted it and the user was told the
// threshold had been updated although nothing had changed.
var thresholdResourceKinds = map[string]bool{
	"cpu":  true,
	"ram":  true,
	"ssd":  true,
	"temp": true,
}

// pruneWeekdays is the closed set of days the weekly prune can run on.
var pruneWeekdays = map[string]bool{
	"monday":    true,
	"tuesday":   true,
	"wednesday": true,
	"thursday":  true,
	"friday":    true,
	"saturday":  true,
	"sunday":    true,
}

// parsePID validates a PID taken from callback data.
//
// strconv.Atoi used to be the only check, and it happily accepts "-1": the
// handler then ran `kill -15 -1`, which signals every process the bot user owns.
func parsePID(raw string) (int, bool) {
	if raw == "" || len(raw) > maxPIDLen {
		return 0, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// rejectCallback tells the user the press did nothing and keeps the payload out
// of the chat: a rejected payload is forged or left over from an older release,
// so the only useful record is in the log.
//
// userText is the specific complaint when there is one, otherwise the generic
// "cancelled". Never answer a rejected payload with silence: the button was
// pressed and the user must be told it did nothing.
func rejectCallback(ctx *AppContext, bot BotAPI, chatID int64, kind, data, userText string) {
	slog.Warn("Rejected callback payload", "kind", kind, "data", truncate(data, 96))
	if userText == "" {
		userText = ctx.Tr("cancelled")
	}
	safeSend(bot, tgbotapi.NewMessage(chatID, userText))
}

// notifyPatchFailure tells the user the configuration change was not saved.
//
// The dictionary has no generic "settings not saved" key (reported to the audit
// owner), so the message carries the reason, which is the part the user can act
// on, instead of a success line that would be a lie.
func notifyPatchFailure(ctx *AppContext, bot BotAPI, chatID int64, err error) {
	slog.Error("Config patch failed", "err", sanitizeErr(err))
	safeSend(bot, tgbotapi.NewMessage(chatID, "❌ `"+truncate(sanitizeErr(err).Error(), 200)+"`"))
}

// applySettingsPatch writes a patch to config.json and reports what happened.
//
// It returns false when the change did not reach the disk, so no caller can
// answer "updated" for a patch that applyConfigPatch dropped or refused.
func applySettingsPatch(ctx *AppContext, bot BotAPI, chatID int64, patch map[string]interface{}) bool {
	if len(patch) == 0 {
		slog.Warn("Refusing to apply an empty config patch")
		return false
	}
	res, err := applyConfigPatch(patch)
	switch {
	case err != nil:
		notifyPatchFailure(ctx, bot, chatID, err)
		return false
	case len(res.Ignored) > 0:
		// applyConfigPatch refuses any key that is not a field of Config, so a
		// non-empty Ignored means part of what the user asked for never reached
		// the disk. Saying "updated" would be a lie.
		slog.Warn("Config patch refused some fields", "fields", res.Ignored)
		safeSend(bot, tgbotapi.NewMessage(chatID, "❌ `"+strings.Join(res.Ignored, ", ")+"`"))
		return false
	case len(res.Corrected) > 0:
		// Written, but sanitizeConfig clamped or filled something: the value the
		// user asked for is not the value now on disk.
		slog.Warn("Config patch corrected by sanitizer", "fields", res.Corrected)
	}
	return true
}

// resolveThresholdResource validates a resource id taken from callback data and
// returns the canonical id plus the mount it refers to, if any.
//
// For disks the mount must be one the bot knows about. The prefix match also
// accepts a mount cut short by the 64 byte callback limit enforced in
// getThresholdResourceText, which is the only way a long mount name reaches here.
func resolveThresholdResource(ctx *AppContext, res string) (canonical string, mount string, ok bool) {
	if res == "" || len(res) > 96 {
		return "", "", false
	}
	if thresholdResourceKinds[res] {
		return res, "", true
	}
	raw, isDisk := strings.CutPrefix(res, "disk:")
	if !isDisk || raw == "" {
		return "", "", false
	}
	mount = longestKnownMountPrefix(ctx, raw)
	if mount == "" {
		return "", "", false
	}
	return "disk:" + mount, mount, true
}

// longestKnownMountPrefix returns the known mount equal to raw, or the longest
// known mount raw starts with.
func longestKnownMountPrefix(ctx *AppContext, raw string) string {
	best := ""
	consider := func(name string) {
		if name != "" && strings.HasPrefix(raw, name) && len(name) > len(best) {
			best = name
		}
	}
	if cfg := ctx.Cfg(); cfg != nil {
		for name := range cfg.Notifications.SecondaryDisks {
			consider(name)
		}
	}
	if stats, ready := ctx.Stats.Get(); ready {
		for name := range stats.SecondaryVols {
			consider(name)
		}
	}
	return best
}

// parseThresholdAction splits `<actionPrefix><w|c>_<resource>` into its level and
// its resource. The level is exactly one character, so a payload cannot smuggle a
// different resource in by omitting the separator.
func parseThresholdAction(data, actionPrefix string) (level, res string, ok bool) {
	rest, found := strings.CutPrefix(data, actionPrefix)
	if !found || len(rest) < 3 || rest[1] != '_' {
		return "", "", false
	}
	switch rest[0] {
	case 'w', 'c':
	default:
		return "", "", false
	}
	return string(rest[0]), rest[2:], true
}

// handleThresholdStep applies one ➖/➕ step to a threshold.
//
// The resource is validated before anything is written and the outcome of the
// write is checked: the screen is redrawn from the reloaded configuration, so
// showing it after a failed write tells the user the value did not move, and the
// reason arrives as a separate message.
func handleThresholdStep(ctx *AppContext, bot BotAPI, chatID int64, msgID int, action, level, res string) {
	canonical, mount, ok := resolveThresholdResource(ctx, res)
	if !ok {
		rejectCallback(ctx, bot, chatID, "thresh_step", res, "")
		return
	}
	cfg := ctx.Cfg()
	if cfg == nil {
		return
	}

	currentVal, ok := thresholdValue(cfg, canonical, mount, level)
	if !ok {
		rejectCallback(ctx, bot, chatID, "thresh_step", res, "")
		return
	}

	newVal := currentVal
	if action == "inc" {
		newVal += 5.0
	} else {
		newVal -= 5.0
	}
	if newVal < 0 {
		newVal = 0
	}
	if max := thresholdMax(canonical); newVal > max {
		newVal = max
	}

	patch := thresholdPatch(cfg, canonical, mount, level, newVal)
	// The outcome is deliberately not short circuited: the screen is redrawn
	// either way, and after a failed write it is redrawn from the unchanged
	// configuration, which is exactly what the user needs to see. The reason has
	// already been sent as its own message.
	applySettingsPatch(ctx, bot, chatID, patch)

	text, kb := getThresholdResourceText(ctx, canonical)
	editMessage(bot, chatID, msgID, text, &kb)
}

// thresholdMax is the ceiling of a resource: percentages stop at 100, the CPU
// temperature at 120 °C.
func thresholdMax(res string) float64 {
	if res == "temp" {
		return 120
	}
	return 100
}

// thresholdValue reads the current value of one threshold of one resource.
func thresholdValue(cfg *Config, res, mount, level string) (float64, bool) {
	warning := level == "w"
	if strings.HasPrefix(res, "disk:") {
		diskCfg, ok := cfg.Notifications.SecondaryDisks[mount]
		if !ok {
			return 0, false
		}
		if warning {
			return diskCfg.WarningThreshold, true
		}
		return diskCfg.CriticalThreshold, true
	}
	switch res {
	case "cpu":
		if warning {
			return cfg.Notifications.CPU.WarningThreshold, true
		}
		return cfg.Notifications.CPU.CriticalThreshold, true
	case "ram":
		if warning {
			return cfg.Notifications.RAM.WarningThreshold, true
		}
		return cfg.Notifications.RAM.CriticalThreshold, true
	case "ssd":
		if warning {
			return cfg.Notifications.DiskSSD.WarningThreshold, true
		}
		return cfg.Notifications.DiskSSD.CriticalThreshold, true
	case "temp":
		if warning {
			return cfg.Temperature.WarningThreshold, true
		}
		return cfg.Temperature.CriticalThreshold, true
	}
	return 0, false
}

// thresholdPatch builds the patch for one threshold of one resource.
//
// The published configuration is only read, never written: the patch is derived
// from copies and handed to applyConfigPatch, which reloads the file.
func thresholdPatch(cfg *Config, res, mount, level string, value float64) map[string]interface{} {
	apply := func(node map[string]interface{}) {
		if level == "w" {
			node["warning_threshold"] = value
		} else {
			node["critical_threshold"] = value
		}
	}

	switch {
	case res == "temp":
		node := map[string]interface{}{}
		apply(node)
		return map[string]interface{}{"temperature": node}

	case strings.HasPrefix(res, "disk:"):
		// deepMerge replaces a map wholesale only when the destination is not a
		// map, so the whole secondary_disks map is rebuilt to keep the other
		// mounts. The source is cfg, read-only.
		// No capacity hint from len(cfg): the map holds a handful of mounts, so
		// the hint is worth nothing, and deriving an allocation size from
		// configuration is exactly the flow CodeQL flags.
		disks := make(map[string]interface{})
		for name, diskCfg := range cfg.Notifications.SecondaryDisks {
			disks[name] = map[string]interface{}{
				"enabled":            diskCfg.Enabled,
				"warning_threshold":  diskCfg.WarningThreshold,
				"critical_threshold": diskCfg.CriticalThreshold,
			}
		}
		node, ok := disks[mount].(map[string]interface{})
		if !ok {
			node = map[string]interface{}{
				"enabled":            true,
				"warning_threshold":  90.0,
				"critical_threshold": 95.0,
			}
			disks[mount] = node
		}
		apply(node)
		return map[string]interface{}{
			"notifications": map[string]interface{}{"secondary_disks": disks},
		}

	case res == "cpu":
		node := map[string]interface{}{}
		apply(node)
		return map[string]interface{}{"notifications": map[string]interface{}{"cpu": node}}

	case res == "ram":
		node := map[string]interface{}{}
		apply(node)
		return map[string]interface{}{"notifications": map[string]interface{}{"ram": node}}

	case res == "ssd":
		node := map[string]interface{}{}
		apply(node)
		return map[string]interface{}{"notifications": map[string]interface{}{"disk_ssd": node}}
	}
	return map[string]interface{}{}
}

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
		kb := getLanguageSelectionKeyboard(ctx, true)
		editMessage(bot, chatID, msgID, ctx.Tr("lang_select"), &kb)
		return true
	}
	if data == "settings_change_reports" {
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_enable" {
		ctx.Settings.SetReportsEnabled(true)
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_disable" {
		ctx.Settings.SetReportsEnabled(false)
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_mode_days" {
		if len(ctx.Settings.GetReportsDays()) == 0 {
			ctx.Settings.SetReportsDays([]int{1, 2, 3, 4, 5})
		}
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_mode_interval" {
		ctx.Settings.SetReportsDays([]int{})
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_preset_all" {
		ctx.Settings.SetReportsDays([]int{0, 1, 2, 3, 4, 5, 6})
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_preset_workdays" {
		ctx.Settings.SetReportsDays([]int{1, 2, 3, 4, 5})
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_preset_weekend" {
		ctx.Settings.SetReportsDays([]int{0, 6})
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if strings.HasPrefix(data, "report_toggle_day_") {
		var day int
		if _, err := fmt.Sscanf(data, "report_toggle_day_%d", &day); err == nil && day >= 0 && day <= 6 {
			ctx.Settings.ToggleReportDay(day)
			saveState(ctx)
			text, kb := getReportSettingsText(ctx)
			editMessage(bot, chatID, msgID, text, &kb)
		}
		return true
	}
	if data == "report_interval_inc" || data == "report_interval_dec" {
		_, interval, _ := ctx.Settings.GetReportsSettings()
		if data == "report_interval_inc" {
			interval++
		} else if interval > 1 {
			interval--
		}
		ctx.Settings.SetReportInterval(interval)
		saveState(ctx)
		text, kb := getReportSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "report_add_time" {
		ctx.Bot.SetPendingAction("add_report_time")
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("type_time_prompt")))
		return true
	}
	if strings.HasPrefix(data, "report_del_time_") {
		var idx int
		if _, err := fmt.Sscanf(data, "report_del_time_%d", &idx); err == nil {
			ctx.Settings.RemoveReportTime(idx)
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
		canonical, _, ok := resolveThresholdResource(ctx, res)
		if !ok {
			rejectCallback(ctx, bot, chatID, "thresh_edit", data, "")
			return true
		}
		text, kb := getThresholdResourceText(ctx, canonical)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if strings.HasPrefix(data, "thresh_custom_") {
		// The resource is validated here so a forged payload cannot become the
		// pending action and be answered later as if it were legitimate.
		if _, _, ok := parseThresholdAction(data, "thresh_custom_"); !ok {
			rejectCallback(ctx, bot, chatID, "thresh_custom", data, "")
			return true
		}
		ctx.Bot.SetPendingAction(data)
		safeSend(bot, tgbotapi.NewMessage(chatID, ctx.Tr("thresh_custom_prompt")))
		return true
	}
	if strings.HasPrefix(data, "thresh_inc_") || strings.HasPrefix(data, "thresh_dec_") {
		level, res, ok := parseThresholdAction(data, "thresh_inc_")
		if !ok {
			level, res, ok = parseThresholdAction(data, "thresh_dec_")
		}
		if !ok {
			rejectCallback(ctx, bot, chatID, "thresh_step", data, "")
			return true
		}
		action := "dec"
		if strings.HasPrefix(data, "thresh_inc_") {
			action = "inc"
		}
		handleThresholdStep(ctx, bot, chatID, msgID, action, level, res)
		return true
	}

	if data == "settings_change_backup" {
		text, kb := getBackupSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "backup_set_uid" {
		ctx.Bot.SetPendingAction("set_backup_uid")
		msg := tgbotapi.NewMessage(chatID, ctx.Tr("backup_prompt"))
		msg.ParseMode = "Markdown"
		safeSend(bot, msg)
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
		ctx.Settings.SetQuietHoursEnabled(true)
		saveState(ctx)
		text, kb := getQuietHoursSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "quiet_disable" {
		ctx.Settings.SetQuietHoursEnabled(false)
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
		ctx.Settings.SetDockerPruneEnabled(true)
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}
	if data == "prune_disable" {
		ctx.Settings.SetDockerPruneEnabled(false)
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
		day := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(data, "prune_day_")))
		// The weekday used to be stored verbatim from the payload and then
		// persisted to state.json, so any string typed by hand became the prune
		// weekday. normalizeDay never saw it: it only sanitizes config.json.
		if !pruneWeekdays[day] {
			rejectCallback(ctx, bot, chatID, "prune_day", data,
				fmt.Sprintf(ctx.Tr("prune_day_invalid"), truncate(day, 32)))
			return true
		}
		ctx.Settings.SetDockerPruneDay(day)
		saveState(ctx)
		text, kb := getDockerPruneSettingsText(ctx)
		editMessage(bot, chatID, msgID, text, &kb)
		return true
	}

	return false
}

func handleAIAnalyzeCritical(ctx *AppContext, bot BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, _ string) bool {
	msg := tgbotapi.NewMessage(chatID, ctx.Tr("ai_gathering_context"))
	sentMsg, err := bot.Send(msg)
	if err != nil {
		slog.Error("Failed to post the AI analysis placeholder", "err", sanitizeErr(err))
		return true
	}
	goSafe("ai-analyze-critical", func() {
		diagnosis, errDiag := AnalyzeCriticalAlerts(ctx, func(model string) {})
		if errDiag != nil {
			edit := newMarkdownEdit(chatID, sentMsg.MessageID,
				fmt.Sprintf("❌ %s: %s", ctx.Tr("docker_ai_error"), sanitizeErr(errDiag)), nil)
			safeSend(bot, edit)
			return
		}
		editMessage(bot, chatID, sentMsg.MessageID, diagnosis, nil)
	})
	return true
}

func handleProcManage(ctx *AppContext, bot BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, data string) bool {
	pid := strings.TrimPrefix(data, "proc_manage_")
	// The PID is echoed back into the keyboard, so an unchecked payload could
	// push proc_kill_<pid> past Telegram's 64 byte callback limit and make the
	// whole keyboard undeliverable.
	if _, ok := parsePID(pid); !ok {
		rejectCallback(ctx, bot, chatID, "proc_manage", data, ctx.Tr("err_invalid_pid"))
		return true
	}
	text := fmt.Sprintf("⚙️ *%s*\n\n%s `%s`?", ctx.Tr("proc_mgr_title"), ctx.Tr("proc_mgr_prompt"), pid)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🛑 "+ctx.Tr("proc_sigterm"), "proc_kill_term_"+pid),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💀 "+ctx.Tr("proc_sigkill"), "proc_kill_kill_"+pid),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("cancel"), "proc_refresh"),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
	return true
}

func handleProcKill(ctx *AppContext, bot BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, data string) bool {
	parts := strings.Split(data, "_")
	if len(parts) >= 4 {
		signal := parts[2]
		rawPID := parts[3]

		// A negative or signed PID is not a process: `kill -15 -1` signals every
		// process owned by the bot user. parsePID accepts digits only and > 0.
		pid, ok := parsePID(rawPID)
		if !ok {
			rejectCallback(ctx, bot, chatID, "proc_kill", data, ctx.Tr("err_invalid_pid"))
			editProcessMenu(bot, chatID, msgID, ctx)
			return true
		}

		sigArg := "-15"
		if signal == "kill" {
			sigArg = "-9"
		}

		ctxExec, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		err := runCommand(ctxExec, "kill", sigArg, strconv.Itoa(pid))

		if err != nil {
			slog.Error("Kill failed", "pid", pid, "signal", sigArg, "err", sanitizeErr(err))
			safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ %s %d: %s", ctx.Tr("proc_kill_err"), pid, sanitizeErr(err))))
		} else {
			safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf("✅ %s %d.", ctx.Tr("proc_killed_ok"), pid)))
		}
	}
	editProcessMenu(bot, chatID, msgID, ctx)
	return true
}

// editProcessMenu redraws the process menu, tolerating a message that cannot be
// edited (for example a freshly sent one).
func editProcessMenu(bot BotAPI, chatID int64, msgID int, ctx *AppContext) {
	text, kb := getProcessesMenu(ctx)
	if msgID > 0 {
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	msg := newMarkdownMessage(chatID, text)
	msg.ReplyMarkup = kb
	safeSend(bot, msg)
}

func handleProcRefresh(ctx *AppContext, bot BotAPI, chatID int64, msgID int, _ *tgbotapi.CallbackQuery, _ string) bool {
	editProcessMenu(bot, chatID, msgID, ctx)
	return true
}
