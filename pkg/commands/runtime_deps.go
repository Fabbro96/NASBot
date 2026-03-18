package commands

import (
	"context"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ConfigPatchResult struct {
	Ignored   []string
	Corrected []string
}

type RuntimeDeps struct {
	SendDockerMenu               func(ctx *AppContext, bot BotAPI, chatID int64)
	SendWithKeyboard             func(ctx *AppContext, bot BotAPI, chatID int64, text string)
	GetDockerStatsText           func(ctx *AppContext) string
	HandleContainerCommand       func(ctx *AppContext, bot BotAPI, chatID int64, args string)
	HandleKillCommand            func(ctx *AppContext, bot BotAPI, chatID int64, args string)
	AskDockerRestartConfirmation func(ctx *AppContext, bot BotAPI, chatID int64)
	SendMarkdown                 func(bot BotAPI, chatID int64, text string)
	HandleSpeedtest              func(ctx *AppContext, bot BotAPI, chatID int64)
	AskPowerConfirmation         func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, action string)
	ExecuteForcedReboot          func(ctx *AppContext, bot BotAPI, chatID int64, msgID int, reason string)
	SendLanguageSelection        func(ctx *AppContext, bot BotAPI, chatID int64)
	SendSettingsMenu             func(ctx *AppContext, bot BotAPI, chatID int64)
	CallGeminiWithFallback       func(ctx *AppContext, prompt string, onModelChange func(string)) (string, error)
	GetTrendSummary              func(ctx *AppContext) (cpuGraph, ramGraph string)
	GetCachedContainerList       func(ctx *AppContext) []ContainerInfo
	ReadCPUTemp                  func() float64
	GetSmartDevices              func(ctx *AppContext) []string
	ReadDiskSMART                func(device string) (temp int, health string)
	Version                      string
	RunCommandOutput             func(ctx context.Context, name string, args ...string) ([]byte, error)
	RunCommandStdout             func(ctx context.Context, name string, args ...string) ([]byte, error)
	RunCommand                   func(ctx context.Context, name string, args ...string) error
	EditMessage                  func(bot BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup)
	SafeSend                     func(bot BotAPI, c tgbotapi.Chattable)
	HandleHealthCommand          func(ctx *AppContext, bot BotAPI, chatID int64)
	ApplyLatestRelease           func(ctx *AppContext, bot BotAPI, chatID int64, msgID int)
	GenerateReport               func(ctx *AppContext, includeAI bool, onModelChange func(string)) string
	GetConfigJSONSafe            func() (string, error)
	ApplyConfigPatch             func(patch map[string]interface{}) (ConfigPatchResult, error)
}

var runtimeDeps RuntimeDeps

func BindRuntime(deps RuntimeDeps) {
	runtimeDeps = deps
}

func sendDockerMenu(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.SendDockerMenu != nil {
		runtimeDeps.SendDockerMenu(ctx, bot, chatID)
	}
}

func sendWithKeyboard(ctx *AppContext, bot BotAPI, chatID int64, text string) {
	if runtimeDeps.SendWithKeyboard != nil {
		runtimeDeps.SendWithKeyboard(ctx, bot, chatID, text)
	}
}

func getDockerStatsText(ctx *AppContext) string {
	if runtimeDeps.GetDockerStatsText != nil {
		return runtimeDeps.GetDockerStatsText(ctx)
	}
	return ""
}

func handleContainerCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
	if runtimeDeps.HandleContainerCommand != nil {
		runtimeDeps.HandleContainerCommand(ctx, bot, chatID, args)
	}
}

func handleKillCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
	if runtimeDeps.HandleKillCommand != nil {
		runtimeDeps.HandleKillCommand(ctx, bot, chatID, args)
	}
}

func askDockerRestartConfirmation(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.AskDockerRestartConfirmation != nil {
		runtimeDeps.AskDockerRestartConfirmation(ctx, bot, chatID)
	}
}

func sendMarkdown(bot BotAPI, chatID int64, text string) {
	if runtimeDeps.SendMarkdown != nil {
		runtimeDeps.SendMarkdown(bot, chatID, text)
	}
}

func handleSpeedtest(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.HandleSpeedtest != nil {
		runtimeDeps.HandleSpeedtest(ctx, bot, chatID)
	}
}

func askPowerConfirmation(ctx *AppContext, bot BotAPI, chatID int64, msgID int, action string) {
	if runtimeDeps.AskPowerConfirmation != nil {
		runtimeDeps.AskPowerConfirmation(ctx, bot, chatID, msgID, action)
	}
}

func executeForcedReboot(ctx *AppContext, bot BotAPI, chatID int64, msgID int, reason string) {
	if runtimeDeps.ExecuteForcedReboot != nil {
		runtimeDeps.ExecuteForcedReboot(ctx, bot, chatID, msgID, reason)
	}
}

func sendLanguageSelection(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.SendLanguageSelection != nil {
		runtimeDeps.SendLanguageSelection(ctx, bot, chatID)
	}
}

func sendSettingsMenu(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.SendSettingsMenu != nil {
		runtimeDeps.SendSettingsMenu(ctx, bot, chatID)
	}
}

func callGeminiWithFallback(ctx *AppContext, prompt string, onModelChange func(string)) (string, error) {
	if runtimeDeps.CallGeminiWithFallback != nil {
		return runtimeDeps.CallGeminiWithFallback(ctx, prompt, onModelChange)
	}
	return "", nil
}

func getTrendSummary(ctx *AppContext) (cpuGraph, ramGraph string) {
	if runtimeDeps.GetTrendSummary != nil {
		return runtimeDeps.GetTrendSummary(ctx)
	}
	return "", ""
}

func getCachedContainerList(ctx *AppContext) []ContainerInfo {
	if runtimeDeps.GetCachedContainerList != nil {
		return runtimeDeps.GetCachedContainerList(ctx)
	}
	return nil
}

func readCPUTemp() float64 {
	if runtimeDeps.ReadCPUTemp != nil {
		return runtimeDeps.ReadCPUTemp()
	}
	return 0
}

func getSmartDevices(ctx *AppContext) []string {
	if runtimeDeps.GetSmartDevices != nil {
		return runtimeDeps.GetSmartDevices(ctx)
	}
	return nil
}

func readDiskSMART(device string) (temp int, health string) {
	if runtimeDeps.ReadDiskSMART != nil {
		return runtimeDeps.ReadDiskSMART(device)
	}
	return 0, ""
}

func getVersion() string {
	if runtimeDeps.Version != "" {
		return runtimeDeps.Version
	}
	return "dev"
}

func runCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtimeDeps.RunCommandOutput != nil {
		return runtimeDeps.RunCommandOutput(ctx, name, args...)
	}
	return nil, nil
}

func runCommandStdout(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtimeDeps.RunCommandStdout != nil {
		return runtimeDeps.RunCommandStdout(ctx, name, args...)
	}
	return nil, nil
}

func runCommand(ctx context.Context, name string, args ...string) error {
	if runtimeDeps.RunCommand != nil {
		return runtimeDeps.RunCommand(ctx, name, args...)
	}
	return nil
}

func editMessage(bot BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	if runtimeDeps.EditMessage != nil {
		runtimeDeps.EditMessage(bot, chatID, msgID, text, keyboard)
	}
}

func safeSend(bot BotAPI, c tgbotapi.Chattable) {
	if runtimeDeps.SafeSend != nil {
		runtimeDeps.SafeSend(bot, c)
	}
}

func handleHealthCommand(ctx *AppContext, bot BotAPI, chatID int64) {
	if runtimeDeps.HandleHealthCommand != nil {
		runtimeDeps.HandleHealthCommand(ctx, bot, chatID)
	}
}

func applyLatestRelease(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	if runtimeDeps.ApplyLatestRelease != nil {
		runtimeDeps.ApplyLatestRelease(ctx, bot, chatID, msgID)
	}
}

func generateReport(ctx *AppContext, includeAI bool, onModelChange func(string)) string {
	if runtimeDeps.GenerateReport != nil {
		return runtimeDeps.GenerateReport(ctx, includeAI, onModelChange)
	}
	return ""
}

func getConfigJSONSafe() (string, error) {
	if runtimeDeps.GetConfigJSONSafe != nil {
		return runtimeDeps.GetConfigJSONSafe()
	}
	return "", nil
}

func applyConfigPatch(patch map[string]interface{}) (ConfigPatchResult, error) {
	if runtimeDeps.ApplyConfigPatch != nil {
		return runtimeDeps.ApplyConfigPatch(patch)
	}
	return ConfigPatchResult{}, nil
}
