package app

import "encoding/json"

func defaultConfigTemplate() Config {
	return Config{
		Paths:    PathsConfig{SSD: defaultPathSSD},
		Timezone: "Europe/Rome",
		Reports: ReportsConfig{
			Enabled:      true,
			IntervalDays: 1,
			Times:        []TimeConfig{{Hour: 7, Minute: 30}, {Hour: 18, Minute: 30}},
		},
		QuietHours: QuietHoursConfig{Enabled: true, StartHour: 23, StartMinute: 30, EndHour: 7, EndMinute: 0},
		Notifications: NotificationsConfig{
			CPU:            ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			RAM:            ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			Swap:           ResourceConfig{Enabled: false, WarningThreshold: 50, CriticalThreshold: 80},
			DiskSSD:        ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			SecondaryDisks: map[string]ResourceConfig{},
			DiskIO:         DiskIOConfig{Enabled: true, WarningThreshold: 95},
			SMART:          SmartConfig{Enabled: true},
		},
		Temperature:        TemperatureConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 85},
		CriticalContainers: []string{},
		StressTracking:     StressTrackingConfig{Enabled: true, DurationThresholdMinutes: 2},
		Docker: DockerConfig{
			Watchdog:    DockerWatchdogConfig{Enabled: true, TimeoutMinutes: 2, AutoRestartService: true},
			WeeklyPrune: DockerPruneConfig{Enabled: true, Day: "sunday", Hour: 4},
			AutoRestartOnRAMCritical: DockerAutoRestartConfig{
				Enabled:                  true,
				MaxRestartsPerHour:       3,
				RAMThreshold:             98,
				HeavyContainerMemPercent: dockerHeavyMemPercent,
			},
		},
		Intervals:  IntervalsConfig{StatsSeconds: 5, MonitorSeconds: 30, CriticalAlertCooldownMins: 30},
		Cache:      CacheConfig{DockerTTLSeconds: 10},
		FSWatchdog: FSWatchdogConfig{Enabled: true, CheckIntervalMins: 30, WarningThreshold: 85, CriticalThreshold: 90, DeepScanPaths: []string{"/"}, ExcludePatterns: []string{"/proc", "/sys", "/dev", "/run", "/snap"}, TopNFiles: 10},
		Healthchecks: HealthchecksConfig{
			Enabled:       false,
			PingURL:       "",
			PeriodSeconds: 60,
			GraceSeconds:  60,
			// Enabled by default on purpose: if the NAS stops answering pings
			// the only recovery is a reboot. The reboot is announced even during
			// quiet hours, and forcedRebootAllowedLocked withholds it when the
			// outage is a network one (at most one reboot per episode, plus a
			// minimum interval between reboots). sanitizeConfig disables this
			// automatically when PingURL is empty.
			ForceRebootOnDown:    true,
			ForceRebootAfterMins: defaultForceRebootAfterMins,
		},
		KernelWatchdog: KernelWatchdogConfig{Enabled: true, CheckIntervalSecs: 60},
		NetworkWatchdog: NetworkWatchdogConfig{
			Enabled:              true,
			CheckIntervalSecs:    60,
			Targets:              []string{"9.9.9.9", "1.1.1.1"},
			DNSHost:              "quad9.net",
			Gateway:              "",
			FailureThreshold:     3,
			CooldownMins:         10,
			RecoveryNotify:       true,
			ForceRebootOnDown:    true,
			ForceRebootAfterMins: 3,
		},
		RaidWatchdog: RaidWatchdogConfig{Enabled: true, CheckIntervalSecs: 300, CooldownMins: 30, RecoveryNotify: true},
		Backup:       BackupConfig{TargetUserID: 0},
		AdBlock:      AdBlockConfig{Enabled: false, Type: "pihole", URL: "http://192.168.1.100", Token: ""},
		// AutoApply is off by default: the bot replacing its own binary over the
		// network is the largest supply-chain surface in the project. config.example.json
		// already shipped false, so the two defaults disagreed.
		Update: UpdateConfig{AutoApply: false, CheckIntervalHours: 1},
		// /cmd is off and its allowlist is empty. Both are load-bearing: the bot
		// runs as root inside a privileged container, so a general purpose shell
		// is not a capability this bot needs and the one thing that turns a
		// leaked bot token into a root shell on the NAS. Enabling it is an
		// explicit act in config.json, never a default.
		ShellCommand: ShellCommandConfig{
			Enabled:         false,
			AllowedBinaries: []string{},
			TimeoutSeconds:  defaultShellCommandTimeoutSeconds,
			MaxOutputChars:  shellCommandOutputChars,
		},
	}
}

// Shell command (/cmd) limits. The timeout must stay short: a command that
// hangs is a command that holds the reply and the process group open, and
// internal/cmdexec kills the whole group when the deadline expires.
//
// The output ceiling is one constant used as both the default and the clamp.
// Telegram rejects a message above 4096 characters and the reply spends part of
// that on the code span with the command, the title and the fences around the
// output. Default equal to ceiling is deliberate: a default above the clamp
// would make every freshly written config.json get rewritten on the next start,
// which is the "did anything change?" signal sanitizeConfig feeds the log.
const (
	defaultShellCommandTimeoutSeconds = 30
	minShellCommandTimeoutSeconds     = 1
	maxShellCommandTimeoutSeconds     = 300

	minShellCommandOutputChars = 200
	shellCommandOutputChars    = 3800
)

// defaultForceRebootAfterMins is the delay applied to a forced reboot when the
// configuration leaves it at 0. Fifteen minutes, not six: the pinger cannot
// tell a frozen NAS from a network outage, and a WAN renegotiation or an ISP
// flapping is usually gone in 2-5 minutes. A shorter window reboots the NAS for
// a problem that was about to fix itself, and then reboots it again on a link
// that has not settled yet. Must stay in sync with
// healthchecksForceRebootDefaultMins in internal/app/healthchecks.go.
const defaultForceRebootAfterMins = 15

func fillMissingConfigFields(configMap map[string]interface{}) bool {
	defaults := defaultConfigTemplate()
	defaultBytes, err := json.Marshal(defaults)
	if err != nil {
		return false
	}
	var defaultMap map[string]interface{}
	if err := json.Unmarshal(defaultBytes, &defaultMap); err != nil {
		return false
	}
	return fillMissingMap(configMap, defaultMap)
}

func fillMissingMap(configMap, defaultMap map[string]interface{}) bool {
	changed := false
	for key, defaultValue := range defaultMap {
		currentValue, exists := configMap[key]
		if !exists {
			// A nil default carries no information: a struct field that is
			// legitimately empty (an unset devices list, say) marshals to null,
			// so writing null back and calling it a change made every boot look
			// like a rewrite of config.json.
			if defaultValue != nil {
				configMap[key] = defaultValue
				changed = true
			}
			continue
		}
		if currentValue == nil && defaultValue != nil {
			configMap[key] = defaultValue
			changed = true
			continue
		}
		if currentValue == nil {
			continue
		}

		currentMap, currentIsMap := currentValue.(map[string]interface{})
		defaultSubMap, defaultIsMap := defaultValue.(map[string]interface{})
		if currentIsMap && defaultIsMap {
			if fillMissingMap(currentMap, defaultSubMap) {
				changed = true
			}
		}
	}
	return changed
}
