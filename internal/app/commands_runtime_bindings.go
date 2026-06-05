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
		Version:                      func() string { return Version },
		RunCommandOutput:             runCommandOutput,
		RunCommandStdout:             runCommandStdout,
		RunCommand:                   runCommand,
		EditMessage:                  editMessage,
		SafeSend:                     safeSend,
		HandleHealthCommand:          handleHealthCommand,
		ApplyLatestRelease:           applyLatestRelease,
		CheckForUpdate: func(ctx *pcommands.AppContext) (pcommands.ReleaseInfo, bool, error) {
			rel, has, err := checkForUpdate(ctx)
			return pcommands.ReleaseInfo{
				Tag:       rel.Tag,
				URL:       rel.URL,
				AssetName: rel.AssetName,
				AssetURL:  rel.AssetURL,
				Changelog: rel.Changelog,
			}, has, err
		},
		FetchLatestRelease: func(ctx *pcommands.AppContext) (pcommands.ReleaseInfo, error) {
			rel, err := fetchLatestRelease(ctx)
			return pcommands.ReleaseInfo{
				Tag:       rel.Tag,
				URL:       rel.URL,
				AssetName: rel.AssetName,
				AssetURL:  rel.AssetURL,
				Changelog: rel.Changelog,
			}, err
		},
		GenerateReport:    generateReport,
		GetConfigJSONSafe: getConfigJSONSafe,
		ApplyConfigPatch: func(patch map[string]interface{}) (pcommands.ConfigPatchResult, error) {
			res, err := applyConfigPatch(patch)
			return pcommands.ConfigPatchResult{Ignored: res.Ignored, Corrected: res.Corrected}, err
		},
	})
}
