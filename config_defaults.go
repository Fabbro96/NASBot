package main

import "encoding/json"

func defaultConfigTemplate() Config {
	return Config{
		Paths:    PathsConfig{SSD: defaultPathSSD, HDD: defaultPathHDD},
		Timezone: "Europe/Rome",
		Reports: ReportsConfig{
			Enabled: true,
			Morning: ReportSchedule{Enabled: true, Hour: 7, Minute: 30},
			Evening: ReportSchedule{Enabled: true, Hour: 18, Minute: 30},
		},
		QuietHours: QuietHoursConfig{Enabled: true, StartHour: 23, StartMinute: 30, EndHour: 7, EndMinute: 0},
		Notifications: NotificationsConfig{
			CPU:     ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			RAM:     ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			Swap:    ResourceConfig{Enabled: false, WarningThreshold: 50, CriticalThreshold: 80},
			DiskSSD: ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			DiskHDD: ResourceConfig{Enabled: true, WarningThreshold: 90, CriticalThreshold: 95},
			DiskIO:  DiskIOConfig{Enabled: true, WarningThreshold: 95},
			SMART:   SmartConfig{Enabled: true},
		},
		Temperature:        TemperatureConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 85},
		CriticalContainers: []string{},
		StressTracking:     StressTrackingConfig{Enabled: true, DurationThresholdMinutes: 2},
		Docker: DockerConfig{
			Watchdog:                 DockerWatchdogConfig{Enabled: true, TimeoutMinutes: 2, AutoRestartService: true},
			WeeklyPrune:              DockerPruneConfig{Enabled: true, Day: "sunday", Hour: 4},
			AutoRestartOnRAMCritical: DockerAutoRestartConfig{Enabled: true, MaxRestartsPerHour: 3, RAMThreshold: 98},
		},
		Intervals:      IntervalsConfig{StatsSeconds: 5, MonitorSeconds: 30, CriticalAlertCooldownMins: 30},
		Cache:          CacheConfig{DockerTTLSeconds: 10},
		FSWatchdog:     FSWatchdogConfig{Enabled: true, CheckIntervalMins: 30, WarningThreshold: 85, CriticalThreshold: 90, DeepScanPaths: []string{"/"}, ExcludePatterns: []string{"/proc", "/sys", "/dev", "/run", "/snap"}, TopNFiles: 10},
		Healthchecks:   HealthchecksConfig{Enabled: false, PingURL: "", PeriodSeconds: 60, GraceSeconds: 60},
		KernelWatchdog: KernelWatchdogConfig{Enabled: true, CheckIntervalSecs: 60},
		NetworkWatchdog: NetworkWatchdogConfig{
			Enabled:              true,
			CheckIntervalSecs:    60,
			Targets:              []string{"1.1.1.1", "8.8.8.8"},
			DNSHost:              "google.com",
			Gateway:              "",
			FailureThreshold:     3,
			CooldownMins:         10,
			RecoveryNotify:       true,
			ForceRebootOnDown:    true,
			ForceRebootAfterMins: 3,
		},
		RaidWatchdog: RaidWatchdogConfig{Enabled: true, CheckIntervalSecs: 300, CooldownMins: 30, RecoveryNotify: true},
	}
}

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
		if !exists || currentValue == nil {
			configMap[key] = defaultValue
			changed = true
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
