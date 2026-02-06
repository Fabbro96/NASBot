package main

type Config struct {
	BotToken           string                `json:"bot_token"`
	AllowedUserID      int64                 `json:"allowed_user_id"`
	GeminiAPIKey       string                `json:"gemini_api_key"`
	Paths              PathsConfig           `json:"paths"`
	Timezone           string                `json:"timezone"`
	Reports            ReportsConfig         `json:"reports"`
	QuietHours         QuietHoursConfig      `json:"quiet_hours"`
	Notifications      NotificationsConfig   `json:"notifications"`
	Temperature        TemperatureConfig     `json:"temperature"`
	CriticalContainers []string              `json:"critical_containers"`
	StressTracking     StressTrackingConfig  `json:"stress_tracking"`
	Docker             DockerConfig          `json:"docker"`
	Intervals          IntervalsConfig       `json:"intervals"`
	Cache              CacheConfig           `json:"cache"`
	FSWatchdog         FSWatchdogConfig      `json:"fs_watchdog"`
	Healthchecks       HealthchecksConfig    `json:"healthchecks"`
	KernelWatchdog     KernelWatchdogConfig  `json:"kernel_watchdog"`
	NetworkWatchdog    NetworkWatchdogConfig `json:"network_watchdog"`
	RaidWatchdog       RaidWatchdogConfig    `json:"raid_watchdog"`
}

type PathsConfig struct {
	SSD string `json:"ssd"`
	HDD string `json:"hdd"`
}

type ReportsConfig struct {
	Enabled bool           `json:"enabled"`
	Morning ReportSchedule `json:"morning"`
	Evening ReportSchedule `json:"evening"`
}

type ReportSchedule struct {
	Enabled bool `json:"enabled"`
	Hour    int  `json:"hour"`
	Minute  int  `json:"minute"`
}

type QuietHoursConfig struct {
	Enabled     bool `json:"enabled"`
	StartHour   int  `json:"start_hour"`
	StartMinute int  `json:"start_minute"`
	EndHour     int  `json:"end_hour"`
	EndMinute   int  `json:"end_minute"`
}

type NotificationsConfig struct {
	CPU     ResourceConfig `json:"cpu"`
	RAM     ResourceConfig `json:"ram"`
	Swap    ResourceConfig `json:"swap"`
	DiskSSD ResourceConfig `json:"disk_ssd"`
	DiskHDD ResourceConfig `json:"disk_hdd"`
	DiskIO  DiskIOConfig   `json:"disk_io"`
	SMART   SmartConfig    `json:"smart"`
}

type ResourceConfig struct {
	Enabled           bool    `json:"enabled"`
	WarningThreshold  float64 `json:"warning_threshold"`
	CriticalThreshold float64 `json:"critical_threshold"`
}

// TemperatureConfig reuses ResourceConfig structure as it has the same fields
type TemperatureConfig struct {
	Enabled           bool    `json:"enabled"`
	WarningThreshold  float64 `json:"warning_threshold"`
	CriticalThreshold float64 `json:"critical_threshold"`
}

type DiskIOConfig struct {
	Enabled          bool    `json:"enabled"`
	WarningThreshold float64 `json:"warning_threshold"`
}

type SmartConfig struct {
	Enabled bool `json:"enabled"`
}

type StressTrackingConfig struct {
	Enabled                  bool `json:"enabled"`
	DurationThresholdMinutes int  `json:"duration_threshold_minutes"`
}

type DockerConfig struct {
	Watchdog                 DockerWatchdogConfig    `json:"watchdog"`
	WeeklyPrune              DockerPruneConfig       `json:"weekly_prune"`
	AutoRestartOnRAMCritical DockerAutoRestartConfig `json:"auto_restart_on_ram_critical"`
}

type DockerWatchdogConfig struct {
	Enabled            bool `json:"enabled"`
	TimeoutMinutes     int  `json:"timeout_minutes"`
	AutoRestartService bool `json:"auto_restart_service"`
}

type DockerPruneConfig struct {
	Enabled bool   `json:"enabled"`
	Day     string `json:"day"`
	Hour    int    `json:"hour"`
}

type DockerAutoRestartConfig struct {
	Enabled            bool    `json:"enabled"`
	MaxRestartsPerHour int     `json:"max_restarts_per_hour"`
	RAMThreshold       float64 `json:"ram_threshold"`
}

type IntervalsConfig struct {
	StatsSeconds              int `json:"stats_seconds"`
	MonitorSeconds            int `json:"monitor_seconds"`
	CriticalAlertCooldownMins int `json:"critical_alert_cooldown_minutes"`
}

type CacheConfig struct {
	DockerTTLSeconds int `json:"docker_ttl_seconds"`
}

type FSWatchdogConfig struct {
	Enabled           bool     `json:"enabled"`
	CheckIntervalMins int      `json:"check_interval_minutes"`
	WarningThreshold  float64  `json:"warning_threshold"`
	CriticalThreshold float64  `json:"critical_threshold"`
	DeepScanPaths     []string `json:"deep_scan_paths"`
	ExcludePatterns   []string `json:"exclude_patterns"`
	TopNFiles         int      `json:"top_n_files"`
}

type HealthchecksConfig struct {
	Enabled       bool   `json:"enabled"`
	PingURL       string `json:"ping_url"`
	PeriodSeconds int    `json:"period_seconds"`
	GraceSeconds  int    `json:"grace_seconds"`
}

type KernelWatchdogConfig struct {
	Enabled           bool `json:"enabled"`
	CheckIntervalSecs int  `json:"check_interval_seconds"`
}

type NetworkWatchdogConfig struct {
	Enabled           bool     `json:"enabled"`
	CheckIntervalSecs int      `json:"check_interval_seconds"`
	Targets           []string `json:"targets"`
	DNSHost           string   `json:"dns_host"`
	Gateway           string   `json:"gateway"`
	FailureThreshold  int      `json:"failure_threshold"`
	CooldownMins      int      `json:"cooldown_minutes"`
	RecoveryNotify    bool     `json:"recovery_notify"`
}

type RaidWatchdogConfig struct {
	Enabled           bool `json:"enabled"`
	CheckIntervalSecs int  `json:"check_interval_seconds"`
	CooldownMins      int  `json:"cooldown_minutes"`
	RecoveryNotify    bool `json:"recovery_notify"`
}
