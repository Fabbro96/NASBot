package app

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

func SetupCallbackRegistry() *CallbackRegistry {
	r := NewCallbackRegistry()

	// Main Menu Navigation
	r.RegisterExact("refresh_status", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getStatusText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("back_main", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getStatusText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("show_temp", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getTempText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("show_docker", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		text, kb := getDockerMenuText(ctx)
		editMessage(bot, chatID, msgID, text, kb)
		return true
	}))
	r.RegisterExact("show_dstats", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getDockerStatsText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("show_top", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getTopProcText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("show_net", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, getNetworkText(ctx), &mainKb)
		return true
	}))
	r.RegisterExact("show_report", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		mainKb := getMainKeyboard(ctx)
		editMessage(bot, chatID, msgID, generateReport(ctx, true, nil), &mainKb)
		return true
	}))
	r.RegisterExact("show_power", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		text, kb := getPowerMenuText(ctx)
		editMessage(bot, chatID, msgID, text, kb)
		return true
	}))

	// Power / Docker Operations
	r.RegisterExact("update_apply_latest", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		applyLatestRelease(ctx, bot, chatID, msgID)
		return true
	}))
	r.RegisterExact("update_cancel", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		editMessage(bot, chatID, msgID, ctx.Tr("update_cancel_text"), nil)
		return true
	}))
	r.RegisterExact("confirm_reboot", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		handlePowerConfirm(ctx, bot, chatID, msgID, data)
		return true
	}))
	r.RegisterExact("confirm_shutdown", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		handlePowerConfirm(ctx, bot, chatID, msgID, data)
		return true
	}))
	r.RegisterExact("cancel_power", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		editMessage(bot, chatID, msgID, ctx.Tr("cancelled"), nil)
		return true
	}))
	r.RegisterPrefix("pre_confirm_reboot", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		askPowerConfirmation(ctx, bot, chatID, msgID, "reboot")
		return true
	}))
	r.RegisterPrefix("pre_confirm_shutdown", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		askPowerConfirmation(ctx, bot, chatID, msgID, "shutdown")
		return true
	}))
	r.RegisterExact("force_reboot_now", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		executeForcedReboot(ctx, bot, chatID, msgID, "manual-force-button")
		return true
	}))

	r.RegisterExact("confirm_restart_docker", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		executeDockerServiceRestart(ctx, bot, chatID, msgID)
		return true
	}))
	r.RegisterExact("cancel_restart_docker", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		editMessage(bot, chatID, msgID, ctx.Tr("docker_restart_cancel"), nil)
		return true
	}))
	r.RegisterExact("docker_restart_service", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		askDockerRestartConfirmationEdit(ctx, bot, chatID, msgID)
		return true
	}))
	r.RegisterExact("docker_restart_all", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		askRestartAllContainersConfirmation(ctx, bot, chatID, msgID)
		return true
	}))
	r.RegisterExact("confirm_restart_all", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		executeRestartAllContainers(ctx, bot, chatID, msgID)
		return true
	}))
	r.RegisterExact("cancel_restart_all", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		text, kb := getDockerMenuText(ctx)
		editMessage(bot, chatID, msgID, text, kb)
		return true
	}))

	// Prefixes
	r.RegisterPrefix("health_", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		handleHealthCallback(ctx, bot, query, data)
		return true
	}))
	r.RegisterPrefix("adblock_", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		handleAdBlockCallback(ctx, bot, chatID, msgID, data)
		return true
	}))
	r.RegisterPrefix("container_", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		handleContainerCallback(ctx, bot, chatID, msgID, data)
		return true
	}))

	r.RegisterExact("ai_analyze_critical", CallbackFunc(handleAIAnalyzeCritical))
	r.RegisterPrefix("proc_manage_", CallbackFunc(handleProcManage))
	r.RegisterPrefix("proc_kill_", CallbackFunc(handleProcKill))
	r.RegisterExact("proc_refresh", CallbackFunc(handleProcRefresh))

	// Delegate settings to the existing logic initially, to avoid a 1000 line registry
	r.RegisterPrefix("", CallbackFunc(func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, query *tgbotapi.CallbackQuery, data string) bool {
		return handleSettingsCallback(ctx, bot, chatID, msgID, data)
	}))

	return r
}
