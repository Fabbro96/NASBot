package app

import pcommands "nasbot/pkg/commands"

func init() {
	pcommands.BindRuntime(pcommands.RuntimeDeps{
		SendDockerMenu:               sendDockerMenu,
		SendWithKeyboard:             sendWithKeyboard,
		GetDockerStatsText:           getDockerStatsText,
		HandleContainerCommand:       handleContainerCommand,
		HandleKillCommand:            handleKillCommand,
		AskDockerRestartConfirmation: askDockerRestartConfirmation,
		SendMarkdown:                 sendMarkdown,
		HandleSpeedtest:              handleSpeedtest,
		AskPowerConfirmation:         askPowerConfirmation,
		ExecuteForcedReboot:          executeForcedReboot,
		SendLanguageSelection:        sendLanguageSelection,
		SendSettingsMenu:             sendSettingsMenu,
		CallGeminiWithFallback:       callGeminiWithFallback,
		GetTrendSummary:              getTrendSummary,
		GetCachedContainerList:       getCachedContainerList,
		ReadCPUTemp:                  readCPUTemp,
		GetSmartDevices:              getSmartDevices,
		ReadDiskSMART:                readDiskSMART,
		Version:                      Version,
		RunCommandOutput:             runCommandOutput,
		RunCommandStdout:             runCommandStdout,
		RunCommand:                   runCommand,
		EditMessage:                  editMessage,
		SafeSend:                     safeSend,
		HandleHealthCommand:          handleHealthCommand,
		ApplyLatestRelease:           applyLatestRelease,
		GenerateReport:               generateReport,
		GetConfigJSONSafe:            getConfigJSONSafe,
		ApplyConfigPatch: func(patch map[string]interface{}) (pcommands.ConfigPatchResult, error) {
			res, err := applyConfigPatch(patch)
			return pcommands.ConfigPatchResult{Ignored: res.Ignored, Corrected: res.Corrected}, err
		},
	})
}
