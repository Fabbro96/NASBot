package commands

import (
	"context"
	"errors"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ErrDepUnbound is returned by the runtime wrappers when the matching field of
// RuntimeDeps was never bound. Reporting success (or an empty result) in that
// case made /configset answer "updated" without writing anything, /top answer
// "no processes" and /logs answer "no logs" — a silent no-op instead of a bug.
var ErrDepUnbound = errors.New("runtime dependency not bound")

func unboundError(name string) error {
	return fmt.Errorf("%s: %w", name, ErrDepUnbound)
}

type ConfigPatchResult struct {
	Ignored   []string
	Corrected []string
}

type ReleaseInfo struct {
	Tag       string
	URL       string
	AssetName string
	AssetURL  string
	Changelog string
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
	GetDiskInfoText              func(ctx *AppContext) string
	Version                      func() string
	RunCommandOutput             func(ctx context.Context, name string, args ...string) ([]byte, error)
	RunCommandStdout             func(ctx context.Context, name string, args ...string) ([]byte, error)
	RunCommand                   func(ctx context.Context, name string, args ...string) error
	EditMessage                  func(bot BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup)
	SafeSend                     func(bot BotAPI, c tgbotapi.Chattable)
	HandleHealthCommand          func(ctx *AppContext, bot BotAPI, chatID int64)
	ApplyLatestRelease           func(ctx *AppContext, bot BotAPI, chatID int64, msgID int)
	CheckForUpdate               func(ctx *AppContext) (ReleaseInfo, bool, error)
	FetchLatestRelease           func(ctx *AppContext) (ReleaseInfo, error)
	GenerateReport               func(ctx *AppContext, includeAI bool, onModelChange func(string)) string
	GetConfigJSONSafe            func() (string, error)
	ApplyConfigPatch             func(patch map[string]interface{}) (ConfigPatchResult, error)
}

var runtimeDeps RuntimeDeps

func getDiskInfoText(ctx *AppContext) string {
	if runtimeDeps.GetDiskInfoText == nil {
		return "disk info unavailable"
	}
	return runtimeDeps.GetDiskInfoText(ctx)
}

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
	if runtimeDeps.CallGeminiWithFallback == nil {
		return "", unboundError("CallGeminiWithFallback")
	}
	return runtimeDeps.CallGeminiWithFallback(ctx, prompt, onModelChange)
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
	if runtimeDeps.ReadDiskSMART == nil {
		// Same shape as a real "smartctl not in sudoers / disk without SMART":
		// /temp must show "no data" instead of a green tick.
		return -1, "UNKNOWN"
	}
	return runtimeDeps.ReadDiskSMART(device)
}

func getVersion() string {
	if runtimeDeps.Version == nil {
		return "unknown"
	}
	return runtimeDeps.Version()
}

func runCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtimeDeps.RunCommandOutput == nil {
		return nil, unboundError("RunCommandOutput")
	}
	return runtimeDeps.RunCommandOutput(ctx, name, args...)
}

// runCommandStdout and runCommand have no production caller: they exist only for
// command_runner_test.go, which cannot be deleted from here. They no longer
// pretend to succeed when unbound — runCommandStdout falls back to the runner
// used in production and runCommand reports the missing binding.
func runCommandStdout(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtimeDeps.RunCommandStdout != nil {
		return runtimeDeps.RunCommandStdout(ctx, name, args...)
	}
	return runCommandOutput(ctx, name, args...)
}

func runCommand(ctx context.Context, name string, args ...string) error {
	if runtimeDeps.RunCommand == nil {
		return unboundError("RunCommand")
	}
	return runtimeDeps.RunCommand(ctx, name, args...)
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

func checkForUpdate(ctx *AppContext) (ReleaseInfo, bool, error) {
	if runtimeDeps.CheckForUpdate == nil {
		return ReleaseInfo{}, false, unboundError("CheckForUpdate")
	}
	return runtimeDeps.CheckForUpdate(ctx)
}

func fetchLatestRelease(ctx *AppContext) (ReleaseInfo, error) {
	if runtimeDeps.FetchLatestRelease == nil {
		return ReleaseInfo{}, unboundError("FetchLatestRelease")
	}
	return runtimeDeps.FetchLatestRelease(ctx)
}

func generateReport(ctx *AppContext, includeAI bool, onModelChange func(string)) string {
	if runtimeDeps.GenerateReport != nil {
		return runtimeDeps.GenerateReport(ctx, includeAI, onModelChange)
	}
	return ""
}

func getConfigJSONSafe() (string, error) {
	if runtimeDeps.GetConfigJSONSafe == nil {
		return "", unboundError("GetConfigJSONSafe")
	}
	return runtimeDeps.GetConfigJSONSafe()
}

func applyConfigPatch(patch map[string]interface{}) (ConfigPatchResult, error) {
	if runtimeDeps.ApplyConfigPatch == nil {
		return ConfigPatchResult{}, unboundError("ApplyConfigPatch")
	}
	return runtimeDeps.ApplyConfigPatch(patch)
}
