package model

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
	Update             UpdateConfig          `json:"update"`
	Backup             BackupConfig          `json:"backup"`
	AdBlock            AdBlockConfig         `json:"adblock"`
	ShellCommand       ShellCommandConfig    `json:"shell_command"`
}

// ShellCommandConfig gates /cmd (aliases: /shell, /exec).
//
// /cmd used to hand the whole message to `sh -c`. Inside a container that runs
// as root with privileged: true, pid: host and the docker socket mounted, that
// made a leaked bot token equivalent to a root shell on the NAS. A NAS
// monitoring bot has no use for a general purpose shell, so the capability is
// off by default and, when it is on, it runs only the binaries named here.
//
// AllowedBinaries is a list of binary NAMES, never of shell lines: an entry is
// resolved by internal/cmdexec through PATH and the system directories, and
// the arguments that follow it on the /cmd line are passed to the process as
// arguments. Nothing is ever re-parsed by a shell, so `/cmd sh -c id` runs
// nothing unless "sh" is in this list, which is a deliberate act by whoever
// writes config.json.
type ShellCommandConfig struct {
	// Enabled turns /cmd on. False by default.
	Enabled bool `json:"enabled"`
	// AllowedBinaries lists the binaries /cmd may execute, by name.
	AllowedBinaries []string `json:"allowed_binaries"`
	// TimeoutSeconds bounds a single execution, in seconds.
	TimeoutSeconds int `json:"timeout_seconds"`
	// MaxOutputChars caps how much of the output is sent back to the chat.
	MaxOutputChars int `json:"max_output_chars"`
}

type BackupConfig struct {
	TargetUserID int64 `json:"target_user_id"`
}

// UpdateConfig controls automatic update behavior.
type UpdateConfig struct {
	// If true, NASBot will automatically download and apply new GitHub releases
	// when detected, and then restart itself.
	AutoApply          bool `json:"auto_apply"`
	CheckIntervalHours int  `json:"check_interval_hours"`
}

type PathsConfig struct {
	SSD string `json:"ssd"`
}

type ReportsConfig struct {
	Enabled      bool         `json:"enabled"`
	IntervalDays int          `json:"interval_days"`
	Times        []TimeConfig `json:"times"`
}

type TimeConfig struct {
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
}

type QuietHoursConfig struct {
	Enabled     bool `json:"enabled"`
	StartHour   int  `json:"start_hour"`
	StartMinute int  `json:"start_minute"`
	EndHour     int  `json:"end_hour"`
	EndMinute   int  `json:"end_minute"`
}

type NotificationsConfig struct {
	CPU            ResourceConfig            `json:"cpu"`
	RAM            ResourceConfig            `json:"ram"`
	Swap           ResourceConfig            `json:"swap"`
	DiskSSD        ResourceConfig            `json:"disk_ssd"`
	SecondaryDisks map[string]ResourceConfig `json:"secondary_disks"`
	DiskIO         DiskIOConfig              `json:"disk_io"`
	SMART          SmartConfig               `json:"smart"`
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
	Enabled bool     `json:"enabled"`
	Devices []string `json:"devices"`
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
	// HeavyContainerMemPercent is the share of a container's own memory limit
	// above which the container is a candidate for auto-restart once system RAM
	// is critical. It was a bare 20 in the parsing loop, invisible to the user
	// while the feature is on by default.
	HeavyContainerMemPercent float64 `json:"heavy_container_mem_percent"`
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
	// ForceRebootOnDown enables the forced reboot when the pinger stays down
	// for longer than GraceSeconds.
	ForceRebootOnDown bool `json:"force_reboot_on_prolonged_down"`
	// ForceRebootAfterMins is how long the pinger must stay down before the
	// reboot fires. It is independent of the quiet hours, unlike the network
	// watchdog reboot.
	ForceRebootAfterMins int `json:"force_reboot_after_minutes"`
}

type KernelWatchdogConfig struct {
	Enabled           bool `json:"enabled"`
	CheckIntervalSecs int  `json:"check_interval_seconds"`
}

type NetworkWatchdogConfig struct {
	Enabled              bool     `json:"enabled"`
	CheckIntervalSecs    int      `json:"check_interval_seconds"`
	Targets              []string `json:"targets"`
	DNSHost              string   `json:"dns_host"`
	Gateway              string   `json:"gateway"`
	FailureThreshold     int      `json:"failure_threshold"`
	CooldownMins         int      `json:"cooldown_minutes"`
	RecoveryNotify       bool     `json:"recovery_notify"`
	ForceRebootOnDown    bool     `json:"force_reboot_on_prolonged_down"`
	ForceRebootAfterMins int      `json:"force_reboot_after_minutes"`
}

type RaidWatchdogConfig struct {
	Enabled           bool `json:"enabled"`
	CheckIntervalSecs int  `json:"check_interval_seconds"`
	CooldownMins      int  `json:"cooldown_minutes"`
	RecoveryNotify    bool `json:"recovery_notify"`
}

type AdBlockConfig struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"` // "pihole" or "adguard"
	URL     string `json:"url"`
	Token   string `json:"token"`
}
