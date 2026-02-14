package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"
)

// Global computed values (Shared by main bot and watchdog)
var (
	// Global config object
	cfg Config

	// Default paths
	defaultPathSSD = "/Volume1"
	defaultPathHDD = "/Volume2"

	// Config file
	configFile = "config.json"
)

type ConfigPatchResult struct {
	Ignored   []string
	Corrected []string
}

// loadConfig reads config.json and populates the global Config struct
func loadConfig() {
	fileContent, err := os.ReadFile(configFile)
	if err != nil {
		slog.Error("Config file not found", "file", configFile, "err", err)
		fmt.Println("Error: config.json not found. Please create it based on config.example.json")
		os.Exit(1)
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(fileContent, &configMap); err != nil {
		slog.Error("Failed to parse config", "err", err)
		os.Exit(1)
	}

	defaultsAdded := fillMissingConfigFields(configMap)

	mergedContent, err := json.Marshal(configMap)
	if err != nil {
		slog.Error("Failed to serialize merged config", "err", err)
		os.Exit(1)
	}

	if err := json.Unmarshal(mergedContent, &cfg); err != nil {
		slog.Error("Failed to decode merged config", "err", err)
		os.Exit(1)
	}

	changes := sanitizeConfig(&cfg)
	if defaultsAdded || len(changes) > 0 {
		if err := saveConfig(&cfg); err != nil {
			slog.Error("Failed to save corrected config", "err", err)
		} else {
			slog.Warn("Config corrected", "defaults_added", defaultsAdded, "changes", changes)
		}
	}

	if cfg.Paths.SSD == "" {
		cfg.Paths.SSD = defaultPathSSD
	}
	if cfg.Paths.HDD == "" {
		cfg.Paths.HDD = defaultPathHDD
	}

	slog.Info("Configuration loaded successfully",
		"ssd", cfg.Paths.SSD,
		"hdd", cfg.Paths.HDD,
		"stats_interval", time.Duration(cfg.Intervals.StatsSeconds)*time.Second,
		"monitor_interval", time.Duration(cfg.Intervals.MonitorSeconds)*time.Second)
}

// getConfigJSONSafe returns the config as JSON string with hidden secrets
func getConfigJSONSafe() (string, error) {
	safeCfg := cfg
	safeCfg.BotToken = "REDACTED"
	safeCfg.GeminiAPIKey = "REDACTED"

	b, err := json.MarshalIndent(safeCfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func saveConfig(c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configFile, b, 0644)
}

func sanitizeConfig(c *Config) []string {
	changes := make([]string, 0)
	add := func(field string, val any) {
		changes = append(changes, fmt.Sprintf("%s -> %v", field, val))
	}
	clampIntField := func(field string, val *int, min, max int) {
		if v, changed := clampInt(*val, min, max); changed {
			*val = v
			add(field, v)
		}
	}
	clampFloatField := func(field string, val *float64, min, max float64) {
		if v, changed := clampFloat(*val, min, max); changed {
			*val = v
			add(field, fmt.Sprintf("%.2f", v))
		}
	}
	trimField := func(field string, val *string) {
		trimmed := strings.TrimSpace(*val)
		if trimmed != *val {
			*val = trimmed
			add(field, trimmed)
		}
	}

	trimField("timezone", &c.Timezone)
	trimField("paths.ssd", &c.Paths.SSD)
	trimField("paths.hdd", &c.Paths.HDD)
	if c.Paths.SSD == "" {
		c.Paths.SSD = defaultPathSSD
		add("paths.ssd", c.Paths.SSD)
	}
	if c.Paths.HDD == "" {
		c.Paths.HDD = defaultPathHDD
		add("paths.hdd", c.Paths.HDD)
	}

	// Reports
	if c.Reports.Enabled && !c.Reports.Morning.Enabled && !c.Reports.Evening.Enabled {
		c.Reports.Morning.Enabled = true
		add("reports.morning.enabled", true)
	}
	clampIntField("reports.morning.hour", &c.Reports.Morning.Hour, 0, 23)
	clampIntField("reports.morning.minute", &c.Reports.Morning.Minute, 0, 59)
	clampIntField("reports.evening.hour", &c.Reports.Evening.Hour, 0, 23)
	clampIntField("reports.evening.minute", &c.Reports.Evening.Minute, 0, 59)

	// Quiet hours
	clampIntField("quiet_hours.start_hour", &c.QuietHours.StartHour, 0, 23)
	clampIntField("quiet_hours.start_minute", &c.QuietHours.StartMinute, 0, 59)
	clampIntField("quiet_hours.end_hour", &c.QuietHours.EndHour, 0, 23)
	clampIntField("quiet_hours.end_minute", &c.QuietHours.EndMinute, 0, 59)
	if c.QuietHours.Enabled &&
		c.QuietHours.StartHour == c.QuietHours.EndHour &&
		c.QuietHours.StartMinute == c.QuietHours.EndMinute {
		c.QuietHours.Enabled = false
		add("quiet_hours.enabled", false)
	}

	// Notifications
	sanitizeResourceConfig(&c.Notifications.CPU, "notifications.cpu", clampFloatField, add)
	sanitizeResourceConfig(&c.Notifications.RAM, "notifications.ram", clampFloatField, add)
	sanitizeResourceConfig(&c.Notifications.Swap, "notifications.swap", clampFloatField, add)
	sanitizeResourceConfig(&c.Notifications.DiskSSD, "notifications.disk_ssd", clampFloatField, add)
	sanitizeResourceConfig(&c.Notifications.DiskHDD, "notifications.disk_hdd", clampFloatField, add)
	clampFloatField("notifications.disk_io.warning_threshold", &c.Notifications.DiskIO.WarningThreshold, 0, 100)

	// SMART devices
	if len(c.Notifications.SMART.Devices) > 0 {
		c.Notifications.SMART.Devices = normalizeStringList(c.Notifications.SMART.Devices)
		add("notifications.smart.devices", "normalized")
	}

	// Temperature
	clampFloatField("temperature.warning_threshold", &c.Temperature.WarningThreshold, 0, 120)
	clampFloatField("temperature.critical_threshold", &c.Temperature.CriticalThreshold, 0, 120)
	if c.Temperature.CriticalThreshold > 0 && c.Temperature.CriticalThreshold < c.Temperature.WarningThreshold {
		c.Temperature.CriticalThreshold = c.Temperature.WarningThreshold
		add("temperature.critical_threshold", fmt.Sprintf("%.2f", c.Temperature.CriticalThreshold))
	}

	// Critical containers
	if len(c.CriticalContainers) > 0 {
		c.CriticalContainers = normalizeStringList(c.CriticalContainers)
		add("critical_containers", "normalized")
	}

	// Stress tracking
	clampIntField("stress_tracking.duration_threshold_minutes", &c.StressTracking.DurationThresholdMinutes, 1, 1440)

	// Docker
	clampIntField("docker.watchdog.timeout_minutes", &c.Docker.Watchdog.TimeoutMinutes, 1, 120)
	clampIntField("docker.weekly_prune.hour", &c.Docker.WeeklyPrune.Hour, 0, 23)
	if day, changed := normalizeDay(c.Docker.WeeklyPrune.Day); changed {
		c.Docker.WeeklyPrune.Day = day
		add("docker.weekly_prune.day", day)
	}
	clampFloatField("docker.auto_restart_on_ram_critical.ram_threshold", &c.Docker.AutoRestartOnRAMCritical.RAMThreshold, 0, 100)
	clampIntField("docker.auto_restart_on_ram_critical.max_restarts_per_hour", &c.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour, 0, 100)

	// Intervals
	clampIntField("intervals.stats_seconds", &c.Intervals.StatsSeconds, 1, 3600)
	clampIntField("intervals.monitor_seconds", &c.Intervals.MonitorSeconds, 5, 3600)
	clampIntField("intervals.critical_alert_cooldown_minutes", &c.Intervals.CriticalAlertCooldownMins, 1, 1440)

	// Cache
	clampIntField("cache.docker_ttl_seconds", &c.Cache.DockerTTLSeconds, 1, 3600)

	// FS watchdog
	clampIntField("fs_watchdog.check_interval_minutes", &c.FSWatchdog.CheckIntervalMins, 1, 1440)
	clampFloatField("fs_watchdog.warning_threshold", &c.FSWatchdog.WarningThreshold, 0, 100)
	clampFloatField("fs_watchdog.critical_threshold", &c.FSWatchdog.CriticalThreshold, 0, 100)
	if c.FSWatchdog.CriticalThreshold > 0 && c.FSWatchdog.CriticalThreshold < c.FSWatchdog.WarningThreshold {
		c.FSWatchdog.CriticalThreshold = c.FSWatchdog.WarningThreshold
		add("fs_watchdog.critical_threshold", fmt.Sprintf("%.2f", c.FSWatchdog.CriticalThreshold))
	}
	clampIntField("fs_watchdog.top_n_files", &c.FSWatchdog.TopNFiles, 1, 1000)
	if len(c.FSWatchdog.DeepScanPaths) == 0 {
		c.FSWatchdog.DeepScanPaths = []string{"/"}
		add("fs_watchdog.deep_scan_paths", "/")
	} else {
		c.FSWatchdog.DeepScanPaths = normalizeStringList(c.FSWatchdog.DeepScanPaths)
		add("fs_watchdog.deep_scan_paths", "normalized")
	}
	if len(c.FSWatchdog.ExcludePatterns) > 0 {
		c.FSWatchdog.ExcludePatterns = normalizeStringList(c.FSWatchdog.ExcludePatterns)
		add("fs_watchdog.exclude_patterns", "normalized")
	}

	// Healthchecks
	clampIntField("healthchecks.period_seconds", &c.Healthchecks.PeriodSeconds, 10, 3600)
	clampIntField("healthchecks.grace_seconds", &c.Healthchecks.GraceSeconds, 10, 3600)
	trimField("healthchecks.ping_url", &c.Healthchecks.PingURL)
	if c.Healthchecks.GraceSeconds < c.Healthchecks.PeriodSeconds {
		c.Healthchecks.GraceSeconds = c.Healthchecks.PeriodSeconds
		add("healthchecks.grace_seconds", c.Healthchecks.GraceSeconds)
	}
	if c.Healthchecks.Enabled && c.Healthchecks.PingURL == "" {
		c.Healthchecks.Enabled = false
		add("healthchecks.enabled", false)
	}

	// Kernel watchdog
	clampIntField("kernel_watchdog.check_interval_seconds", &c.KernelWatchdog.CheckIntervalSecs, 10, 3600)

	// Network watchdog
	clampIntField("network_watchdog.check_interval_seconds", &c.NetworkWatchdog.CheckIntervalSecs, 10, 3600)
	clampIntField("network_watchdog.failure_threshold", &c.NetworkWatchdog.FailureThreshold, 1, 20)
	clampIntField("network_watchdog.cooldown_minutes", &c.NetworkWatchdog.CooldownMins, 1, 120)
	if c.NetworkWatchdog.ForceRebootAfterMins <= 0 {
		c.NetworkWatchdog.ForceRebootAfterMins = 3
		add("network_watchdog.force_reboot_after_minutes", c.NetworkWatchdog.ForceRebootAfterMins)
	} else {
		clampIntField("network_watchdog.force_reboot_after_minutes", &c.NetworkWatchdog.ForceRebootAfterMins, 1, 1440)
	}
	trimField("network_watchdog.dns_host", &c.NetworkWatchdog.DNSHost)
	trimField("network_watchdog.gateway", &c.NetworkWatchdog.Gateway)
	if c.NetworkWatchdog.DNSHost == "" {
		c.NetworkWatchdog.DNSHost = "google.com"
		add("network_watchdog.dns_host", c.NetworkWatchdog.DNSHost)
	}
	if len(c.NetworkWatchdog.Targets) > 0 {
		c.NetworkWatchdog.Targets = normalizeStringList(c.NetworkWatchdog.Targets)
		add("network_watchdog.targets", "normalized")
	}
	if len(c.NetworkWatchdog.Targets) == 0 {
		c.NetworkWatchdog.Targets = []string{"1.1.1.1", "8.8.8.8"}
		add("network_watchdog.targets", "default")
	}

	// Raid watchdog
	clampIntField("raid_watchdog.check_interval_seconds", &c.RaidWatchdog.CheckIntervalSecs, 30, 7200)
	clampIntField("raid_watchdog.cooldown_minutes", &c.RaidWatchdog.CooldownMins, 1, 1440)

	return changes
}

func sanitizeResourceConfig(rc *ResourceConfig, prefix string, clampFloatField func(string, *float64, float64, float64), add func(string, any)) {
	clampFloatField(prefix+".warning_threshold", &rc.WarningThreshold, 0, 100)
	clampFloatField(prefix+".critical_threshold", &rc.CriticalThreshold, 0, 100)
	if rc.CriticalThreshold > 0 && rc.CriticalThreshold < rc.WarningThreshold {
		rc.CriticalThreshold = rc.WarningThreshold
		add(prefix+".critical_threshold", fmt.Sprintf("%.2f", rc.CriticalThreshold))
	}
}

func clampInt(v, min, max int) (int, bool) {
	if v < min {
		return min, true
	}
	if v > max {
		return max, true
	}
	return v, false
}

func clampFloat(v, min, max float64) (float64, bool) {
	if v < min {
		return min, true
	}
	if v > max {
		return max, true
	}
	return v, false
}

func normalizeDay(day string) (string, bool) {
	d := strings.ToLower(strings.TrimSpace(day))
	if d == "" {
		return "sunday", true
	}
	switch d {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		return d, d != day
	default:
		return "sunday", true
	}
}

func normalizeStringList(items []string) []string {
	if len(items) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

// applyConfigPatch updates the config with a patch
func applyConfigPatch(patch map[string]interface{}) (ConfigPatchResult, error) {
	result := ConfigPatchResult{}
	fileContent, err := os.ReadFile(configFile)
	if err != nil {
		return result, err
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(fileContent, &configMap); err != nil {
		return result, err
	}

	// Ignore locked fields
	locked := map[string]struct{}{
		"bot_token":       {},
		"allowed_user_id": {},
	}
	ignored := make([]string, 0)
	for k := range patch {
		if _, isLocked := locked[k]; isLocked {
			ignored = append(ignored, k)
			delete(patch, k)
		}
	}
	if len(ignored) > 0 {
		sort.Strings(ignored)
	}
	if len(patch) == 0 {
		result.Ignored = ignored
		return result, nil
	}

	// Merge patch
	for k, v := range patch {
		configMap[k] = v
	}
	_ = fillMissingConfigFields(configMap)

	// Serialize merged map
	mergedContent, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return result, err
	}

	var updatedCfg Config
	if err := json.Unmarshal(mergedContent, &updatedCfg); err != nil {
		return result, err
	}
	corrected := sanitizeConfig(&updatedCfg)
	newContent, err := json.MarshalIndent(updatedCfg, "", "  ")
	if err != nil {
		return result, err
	}

	// Save
	if err := os.WriteFile(configFile, newContent, 0644); err != nil {
		return result, err
	}

	// Reload
	loadConfig()

	result.Ignored = ignored
	result.Corrected = corrected
	return result, nil
}
