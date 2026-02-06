package main

import (
"encoding/json"
"fmt"
"log"
"os"
"time"
)

// Config holds all configuration from config.json
type Config struct {
	BotToken      string `json:"bot_token"`
	AllowedUserID int64  `json:"allowed_user_id"`
	GeminiAPIKey  string `json:"gemini_api_key"`

	Paths struct {
		SSD string `json:"ssd"`
		HDD string `json:"hdd"`
	} `json:"paths"`

	Timezone string `json:"timezone"`

	Reports struct {
		Enabled bool `json:"enabled"`
		Morning struct {
			Enabled bool `json:"enabled"`
			Hour    int  `json:"hour"`
			Minute  int  `json:"minute"`
		} `json:"morning"`
		Evening struct {
			Enabled bool `json:"enabled"`
			Hour    int  `json:"hour"`
			Minute  int  `json:"minute"`
		} `json:"evening"`
	} `json:"reports"`

	QuietHours struct {
		Enabled     bool `json:"enabled"`
		StartHour   int  `json:"start_hour"`
		StartMinute int  `json:"start_minute"`
		EndHour     int  `json:"end_hour"`
		EndMinute   int  `json:"end_minute"`
	} `json:"quiet_hours"`

	Notifications struct {
		CPU struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"cpu"`
		RAM struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"ram"`
		Swap struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"swap"`
		DiskSSD struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"disk_ssd"`
		DiskHDD struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"disk_hdd"`
		DiskIO struct {
			Enabled          bool    `json:"enabled"`
			WarningThreshold float64 `json:"warning_threshold"`
		} `json:"disk_io"`
		SMART struct {
			Enabled bool `json:"enabled"`
		} `json:"smart"`
	} `json:"notifications"`

	Temperature struct {
		Enabled           bool    `json:"enabled"`
		WarningThreshold  float64 `json:"warning_threshold"`
		CriticalThreshold float64 `json:"critical_threshold"`
	} `json:"temperature"`

	CriticalContainers []string `json:"critical_containers"`

	StressTracking struct {
		Enabled                  bool `json:"enabled"`
		DurationThresholdMinutes int  `json:"duration_threshold_minutes"`
	} `json:"stress_tracking"`

	Docker struct {
		Watchdog struct {
			Enabled            bool `json:"enabled"`
			TimeoutMinutes     int  `json:"timeout_minutes"`
			AutoRestartService bool `json:"auto_restart_service"`
		} `json:"watchdog"`
		WeeklyPrune struct {
			Enabled bool   `json:"enabled"`
			Day     string `json:"day"`
			Hour    int    `json:"hour"`
		} `json:"weekly_prune"`
		AutoRestartOnRAMCritical struct {
			Enabled            bool    `json:"enabled"`
			MaxRestartsPerHour int     `json:"max_restarts_per_hour"`
			RAMThreshold       float64 `json:"ram_threshold"`
		} `json:"auto_restart_on_ram_critical"`
	} `json:"docker"`

	Intervals struct {
		StatsSeconds              int `json:"stats_seconds"`
		MonitorSeconds            int `json:"monitor_seconds"`
		CriticalAlertCooldownMins int `json:"critical_alert_cooldown_minutes"`
	} `json:"intervals"`

	Cache struct {
		DockerTTLSeconds int `json:"docker_ttl_seconds"`
	} `json:"cache"`

	FSWatchdog struct {
		Enabled           bool     `json:"enabled"`
		CheckIntervalMins int      `json:"check_interval_minutes"`
		WarningThreshold  float64  `json:"warning_threshold"`
		CriticalThreshold float64  `json:"critical_threshold"`
		DeepScanPaths     []string `json:"deep_scan_paths"`
		ExcludePatterns   []string `json:"exclude_patterns"`
		TopNFiles         int      `json:"top_n_files"`
	} `json:"fs_watchdog"`

	Healthchecks struct {
		Enabled       bool   `json:"enabled"`
		PingURL       string `json:"ping_url"`
		PeriodSeconds int    `json:"period_seconds"`
		GraceSeconds  int    `json:"grace_seconds"`
	} `json:"healthchecks"`

	KernelWatchdog struct {
		Enabled           bool `json:"enabled"`
		CheckIntervalSecs int  `json:"check_interval_seconds"`
	} `json:"kernel_watchdog"`

	NetworkWatchdog struct {
		Enabled           bool     `json:"enabled"`
		CheckIntervalSecs int      `json:"check_interval_seconds"`
		Targets           []string `json:"targets"`
		DNSHost           string   `json:"dns_host"`
		Gateway           string   `json:"gateway"`
		FailureThreshold  int      `json:"failure_threshold"`
		CooldownMins      int      `json:"cooldown_minutes"`
		RecoveryNotify    bool     `json:"recovery_notify"`
	} `json:"network_watchdog"`

	RaidWatchdog struct {
		Enabled           bool `json:"enabled"`
		CheckIntervalSecs int  `json:"check_interval_seconds"`
		CooldownMins      int  `json:"cooldown_minutes"`
		RecoveryNotify    bool `json:"recovery_notify"`
	} `json:"raid_watchdog"`
}

// loadConfig reads configuration from config.json with smart defaults
func loadConfig() {
	data, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("❌ Error reading config.json: %v\nCreate the file by copying config.example.json", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("❌ Error parsing config.json: %v", err)
	}

	// Required fields
	BotToken = cfg.BotToken
	AllowedUserID = cfg.AllowedUserID

	if BotToken == "" {
		log.Fatal("❌ bot_token empty in config.json")
	}
	if AllowedUserID == 0 {
		log.Fatal("❌ allowed_user_id empty or invalid in config.json")
	}

	// Apply sensible defaults for missing config values
	applyConfigDefaults()
	applyConfigRuntime()

	log.Println("[✓] Config loaded from config.json")
}

// applyConfigDefaults sets sensible defaults for missing configuration
func applyConfigDefaults() {
	// Timezone
	if cfg.Timezone == "" {
		cfg.Timezone = "Europe/Rome"
	}

	// Reports defaults
	if cfg.Reports.Morning.Hour == 0 && cfg.Reports.Morning.Minute == 0 {
		cfg.Reports.Morning.Hour = 7
		cfg.Reports.Morning.Minute = 30
		cfg.Reports.Morning.Enabled = true
	}
	if cfg.Reports.Evening.Hour == 0 && cfg.Reports.Evening.Minute == 0 {
		cfg.Reports.Evening.Hour = 18
		cfg.Reports.Evening.Minute = 30
		cfg.Reports.Evening.Enabled = true
	}

	// Quiet hours defaults
	if !cfg.QuietHours.Enabled && cfg.QuietHours.StartHour == 0 {
		cfg.QuietHours.Enabled = true
		cfg.QuietHours.StartHour = 23
		cfg.QuietHours.StartMinute = 30
		cfg.QuietHours.EndHour = 7
		cfg.QuietHours.EndMinute = 0
	}

	// Notification defaults
	if cfg.Notifications.CPU.WarningThreshold == 0 {
		cfg.Notifications.CPU.Enabled = true
		cfg.Notifications.CPU.WarningThreshold = 90.0
		cfg.Notifications.CPU.CriticalThreshold = 95.0
	}
	if cfg.Notifications.RAM.WarningThreshold == 0 {
		cfg.Notifications.RAM.Enabled = true
		cfg.Notifications.RAM.WarningThreshold = 90.0
		cfg.Notifications.RAM.CriticalThreshold = 95.0
	}
	if cfg.Notifications.Swap.WarningThreshold == 0 {
		cfg.Notifications.Swap.WarningThreshold = 50.0
		cfg.Notifications.Swap.CriticalThreshold = 80.0
	}
	if cfg.Notifications.DiskSSD.WarningThreshold == 0 {
		cfg.Notifications.DiskSSD.Enabled = true
		cfg.Notifications.DiskSSD.WarningThreshold = 90.0
		cfg.Notifications.DiskSSD.CriticalThreshold = 95.0
	}
	if cfg.Notifications.DiskHDD.WarningThreshold == 0 {
		cfg.Notifications.DiskHDD.Enabled = true
		cfg.Notifications.DiskHDD.WarningThreshold = 90.0
		cfg.Notifications.DiskHDD.CriticalThreshold = 95.0
	}
	if cfg.Notifications.DiskIO.WarningThreshold == 0 {
		cfg.Notifications.DiskIO.Enabled = true
		cfg.Notifications.DiskIO.WarningThreshold = 95.0
	}
	if !cfg.Notifications.SMART.Enabled {
		cfg.Notifications.SMART.Enabled = true
	}

	// Stress tracking defaults
	if cfg.StressTracking.DurationThresholdMinutes == 0 {
		cfg.StressTracking.Enabled = true
		cfg.StressTracking.DurationThresholdMinutes = 2
	}

	// Docker defaults
	if cfg.Docker.Watchdog.TimeoutMinutes == 0 {
		cfg.Docker.Watchdog.Enabled = true
		cfg.Docker.Watchdog.TimeoutMinutes = 2
		cfg.Docker.Watchdog.AutoRestartService = true
	}
	if cfg.Docker.WeeklyPrune.Day == "" {
		cfg.Docker.WeeklyPrune.Enabled = true
		cfg.Docker.WeeklyPrune.Day = "sunday"
		cfg.Docker.WeeklyPrune.Hour = 4
	}
	if cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold == 0 {
		cfg.Docker.AutoRestartOnRAMCritical.Enabled = true
		cfg.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour = 3
		cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold = 98.0
	}

	// Intervals defaults
	if cfg.Intervals.StatsSeconds == 0 {
		cfg.Intervals.StatsSeconds = 5
	}
	if cfg.Intervals.MonitorSeconds == 0 {
		cfg.Intervals.MonitorSeconds = 30
	}
	if cfg.Intervals.CriticalAlertCooldownMins == 0 {
		cfg.Intervals.CriticalAlertCooldownMins = 30
	}

	// Temperature defaults
	if cfg.Temperature.WarningThreshold == 0 {
		cfg.Temperature.Enabled = true
		cfg.Temperature.WarningThreshold = 70.0
		cfg.Temperature.CriticalThreshold = 85.0
	}

	// Cache defaults
	if cfg.Cache.DockerTTLSeconds == 0 {
		cfg.Cache.DockerTTLSeconds = 10
	}

	// FS watchdog defaults
	if cfg.FSWatchdog.CheckIntervalMins == 0 && cfg.FSWatchdog.WarningThreshold == 0 && cfg.FSWatchdog.CriticalThreshold == 0 && len(cfg.FSWatchdog.DeepScanPaths) == 0 && len(cfg.FSWatchdog.ExcludePatterns) == 0 && cfg.FSWatchdog.TopNFiles == 0 {
		cfg.FSWatchdog.Enabled = true
		cfg.FSWatchdog.CheckIntervalMins = 30
		cfg.FSWatchdog.WarningThreshold = 85.0
		cfg.FSWatchdog.CriticalThreshold = 90.0
		cfg.FSWatchdog.DeepScanPaths = []string{"/", "/var", "/tmp"}
		cfg.FSWatchdog.ExcludePatterns = []string{"/proc", "/sys", "/dev", "/run"}
		cfg.FSWatchdog.TopNFiles = 10
	}

	// Kernel watchdog defaults
	kernelEmpty := cfg.KernelWatchdog.CheckIntervalSecs == 0 && !cfg.KernelWatchdog.Enabled
	if kernelEmpty {
		cfg.KernelWatchdog.Enabled = true
		cfg.KernelWatchdog.CheckIntervalSecs = 60
	} else if cfg.KernelWatchdog.CheckIntervalSecs == 0 {
		cfg.KernelWatchdog.CheckIntervalSecs = 60
	}

	// Network watchdog defaults
	networkEmpty := cfg.NetworkWatchdog.CheckIntervalSecs == 0 && len(cfg.NetworkWatchdog.Targets) == 0 &&
		cfg.NetworkWatchdog.DNSHost == "" && cfg.NetworkWatchdog.Gateway == "" &&
		cfg.NetworkWatchdog.FailureThreshold == 0 && cfg.NetworkWatchdog.CooldownMins == 0 &&
		!cfg.NetworkWatchdog.Enabled && !cfg.NetworkWatchdog.RecoveryNotify
	if networkEmpty {
		cfg.NetworkWatchdog.Enabled = true
		cfg.NetworkWatchdog.CheckIntervalSecs = 60
		cfg.NetworkWatchdog.Targets = []string{"1.1.1.1", "8.8.8.8"}
		cfg.NetworkWatchdog.DNSHost = "google.com"
		cfg.NetworkWatchdog.FailureThreshold = 3
		cfg.NetworkWatchdog.CooldownMins = 10
		cfg.NetworkWatchdog.RecoveryNotify = true
	} else {
		if cfg.NetworkWatchdog.CheckIntervalSecs == 0 {
			cfg.NetworkWatchdog.CheckIntervalSecs = 60
		}
		if len(cfg.NetworkWatchdog.Targets) == 0 {
			cfg.NetworkWatchdog.Targets = []string{"1.1.1.1", "8.8.8.8"}
		}
		if cfg.NetworkWatchdog.DNSHost == "" {
			cfg.NetworkWatchdog.DNSHost = "google.com"
		}
		if cfg.NetworkWatchdog.FailureThreshold == 0 {
			cfg.NetworkWatchdog.FailureThreshold = 3
		}
		if cfg.NetworkWatchdog.CooldownMins == 0 {
			cfg.NetworkWatchdog.CooldownMins = 10
		}
	}

	// RAID watchdog defaults
	raidEmpty := cfg.RaidWatchdog.CheckIntervalSecs == 0 && cfg.RaidWatchdog.CooldownMins == 0 &&
		!cfg.RaidWatchdog.Enabled && !cfg.RaidWatchdog.RecoveryNotify
	if raidEmpty {
		cfg.RaidWatchdog.Enabled = true
		cfg.RaidWatchdog.CheckIntervalSecs = 300
		cfg.RaidWatchdog.CooldownMins = 30
		cfg.RaidWatchdog.RecoveryNotify = true
	} else {
		if cfg.RaidWatchdog.CheckIntervalSecs == 0 {
			cfg.RaidWatchdog.CheckIntervalSecs = 300
		}
		if cfg.RaidWatchdog.CooldownMins == 0 {
			cfg.RaidWatchdog.CooldownMins = 30
		}
	}
}

// applyConfigRuntime updates derived runtime values from config
func applyConfigRuntime() {
	// Paths
	if cfg.Paths.SSD != "" {
		PathSSD = cfg.Paths.SSD
	}
	if cfg.Paths.HDD != "" {
		PathHDD = cfg.Paths.HDD
	}

	// Reports
	if !cfg.Reports.Enabled || (!cfg.Reports.Morning.Enabled && !cfg.Reports.Evening.Enabled) {
		reportMode = 0
	} else if cfg.Reports.Morning.Enabled && cfg.Reports.Evening.Enabled {
		reportMode = 2
	} else {
		reportMode = 1
	}
	if cfg.Reports.Morning.Enabled {
		reportMorningHour = cfg.Reports.Morning.Hour
		reportMorningMinute = cfg.Reports.Morning.Minute
	}
	if cfg.Reports.Evening.Enabled {
		reportEveningHour = cfg.Reports.Evening.Hour
		reportEveningMinute = cfg.Reports.Evening.Minute
		if !cfg.Reports.Morning.Enabled {
			reportMorningHour = cfg.Reports.Evening.Hour
			reportMorningMinute = cfg.Reports.Evening.Minute
		}
	}

	// Quiet hours
	quietHoursEnabled = cfg.QuietHours.Enabled
	quietStartHour = cfg.QuietHours.StartHour
	quietStartMinute = cfg.QuietHours.StartMinute
	quietEndHour = cfg.QuietHours.EndHour
	quietEndMinute = cfg.QuietHours.EndMinute

	// Docker prune schedule
	dockerPruneEnabled = cfg.Docker.WeeklyPrune.Enabled
	dockerPruneDay = cfg.Docker.WeeklyPrune.Day
	dockerPruneHour = cfg.Docker.WeeklyPrune.Hour

	// Intervals
	if cfg.Intervals.StatsSeconds > 0 {
		IntervalStats = time.Duration(cfg.Intervals.StatsSeconds) * time.Second
	}
	if cfg.Intervals.MonitorSeconds > 0 {
		IntervalMonitor = time.Duration(cfg.Intervals.MonitorSeconds) * time.Second
	}

	// Timezone
	if cfg.Timezone != "" {
		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			log.Printf("[w] Timezone %s not found, using UTC", cfg.Timezone)
			location = time.UTC
		} else {
			location = loc
		}
	}

	// FS watchdog config
	updateFSWatchdogConfig()
}

// saveConfig writes the current configuration to config.json
func saveConfig() error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("error serializing config: %w", err)
	}
	if err := os.WriteFile("config.json", data, 0644); err != nil {
		return fmt.Errorf("error writing config.json: %w", err)
	}
	return nil
}

// getConfigJSONSafe returns config JSON with bot credentials redacted
func getConfigJSONSafe() (string, error) {
	redacted := cfg
	redacted.BotToken = ""
	redacted.AllowedUserID = 0
	data, err := json.MarshalIndent(redacted, "", "  ")
	if err != nil {
		return "", fmt.Errorf("error serializing config: %w", err)
	}
	return string(data), nil
}

// applyConfigPatch merges the patch into current config and persists it
// bot_token and allowed_user_id are ignored for safety
func applyConfigPatch(patch map[string]interface{}) ([]string, error) {
	ignored := []string{}
	if _, ok := patch["bot_token"]; ok {
		delete(patch, "bot_token")
		ignored = append(ignored, "bot_token")
	}
	if _, ok := patch["allowed_user_id"]; ok {
		delete(patch, "allowed_user_id")
		ignored = append(ignored, "allowed_user_id")
	}
	if len(patch) == 0 {
		return ignored, fmt.Errorf("no editable fields found")
	}

	current := map[string]interface{}{}
	data, err := json.Marshal(cfg)
	if err != nil {
		return ignored, fmt.Errorf("error serializing current config: %w", err)
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return ignored, fmt.Errorf("error parsing current config: %w", err)
	}

	mergeMaps(current, patch)

	updated, err := json.Marshal(current)
	if err != nil {
		return ignored, fmt.Errorf("error serializing updated config: %w", err)
	}
	if err := json.Unmarshal(updated, &cfg); err != nil {
		return ignored, fmt.Errorf("error applying updated config: %w", err)
	}

	applyConfigRuntime()
	saveState()
	if err := saveConfig(); err != nil {
		return ignored, err
	}

	return ignored, nil
}

func mergeMaps(dst, src map[string]interface{}) {
	for key, value := range src {
		if valueMap, ok := value.(map[string]interface{}); ok {
			if existing, ok := dst[key].(map[string]interface{}); ok {
				mergeMaps(existing, valueMap)
				dst[key] = existing
			} else {
				dst[key] = valueMap
			}
		} else {
			dst[key] = value
		}
	}
}
