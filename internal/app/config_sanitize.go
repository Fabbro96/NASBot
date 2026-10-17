package app

import (
	"fmt"
	"log/slog"
	"nasbot/internal/cmdexec"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

func sanitizeConfig(c *Config) []string {
	changes := make([]string, 0)
	// add records a correction only for a value that actually changed. Recording
	// a no-op made every boot look like a rewrite, which both filled the log
	// with noise and made the "did anything change?" check useless.
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
	// trimField records that a value was trimmed, never what it became. These
	// changes are logged at boot ("Config corrected") and returned to the
	// Telegram handler, and four of the fields passed through here are
	// credentials: bot_token, gemini_api_key, adblock.token and the healthchecks
	// ping URL, whose path is the secret. Recording the trimmed value put all four
	// into var/nasbot.log whenever a stray space triggered the correction — the
	// one leak the error sanitizer does not cover, because nothing was malformed.
	// The value is not diagnostic for a whitespace trim anyway: knowing *that* it
	// changed is the whole point of the entry.
	trimField := func(field string, val *string) {
		trimmed := strings.TrimSpace(*val)
		if trimmed != *val {
			*val = trimmed
			add(field, "trimmed")
		}
	}
	normalizeListField := func(field string, val *[]string) {
		normalized := normalizeStringList(*val)
		if slices.Equal(normalized, *val) {
			return
		}
		*val = normalized
		add(field, "normalized")
	}

	// Secrets. A token with a stray newline or trailing space fails the Telegram
	// API with an opaque 401, and an accidental space in gemini_api_key costs a
	// round trip to diagnose.
	trimField("bot_token", &c.BotToken)
	trimField("gemini_api_key", &c.GeminiAPIKey)
	trimField("adblock.url", &c.AdBlock.URL)
	trimField("adblock.token", &c.AdBlock.Token)

	// Timezone. An unusable zone used to be caught in runtime_main and silently
	// replaced with UTC, so every report and every quiet-hours window landed on
	// the wrong clock with nothing in the log beyond a warning.
	trimField("timezone", &c.Timezone)
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		if _, defaultErr := time.LoadLocation("UTC"); defaultErr == nil {
			c.Timezone = "UTC"
			add("timezone", "UTC")
		}
	}

	trimField("paths.ssd", &c.Paths.SSD)
	if c.Paths.SSD == "" {
		c.Paths.SSD = defaultPathSSD
		add("paths.ssd", c.Paths.SSD)
	} else if _, err := os.Stat(c.Paths.SSD); err != nil {
		// Not clamped and not recorded as a change: the path may be a volume
		// that is not mounted yet, and recording it would rewrite config.json on
		// every single boot. Worth saying out loud anyway, because disk
		// monitoring silently stops when the path is wrong.
		slog.Warn("paths.ssd is not reachable, disk monitoring may be silent", "ssd", c.Paths.SSD, "err", err)
	}

	// Reports
	clampIntField("reports.interval_days", &c.Reports.IntervalDays, 1, 365)
	if len(c.Reports.Times) == 0 {
		// An empty schedule is not "disabled": getNextReportTime reads it as
		// "no report for the next 365 days" and reports never fire again, with
		// no error anywhere. Restore the default like targets and deep_scan_paths.
		c.Reports.Times = defaultReportTimes()
		add("reports.times", "default")
	} else {
		validTimes := make([]TimeConfig, 0, len(c.Reports.Times))
		for _, t := range c.Reports.Times {
			h, changedH := clampInt(t.Hour, 0, 23)
			m, changedM := clampInt(t.Minute, 0, 59)
			if changedH || changedM {
				add("reports.times.adjusted", fmt.Sprintf("%02d:%02d -> %02d:%02d", t.Hour, t.Minute, h, m))
			}
			validTimes = append(validTimes, TimeConfig{Hour: h, Minute: m})
		}
		seen := make(map[TimeConfig]bool, len(validTimes))
		unique := validTimes[:0]
		for _, t := range validTimes {
			if seen[t] {
				add("reports.times.duplicate", fmt.Sprintf("%02d:%02d", t.Hour, t.Minute))
				continue
			}
			seen[t] = true
			unique = append(unique, t)
		}
		c.Reports.Times = unique
	}

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

	if c.Notifications.SecondaryDisks == nil {
		c.Notifications.SecondaryDisks = make(map[string]ResourceConfig)
	}
	for k, v := range c.Notifications.SecondaryDisks {
		vCopy := v
		sanitizeResourceConfig(&vCopy, "notifications.secondary_disks."+k, clampFloatField, add)
		c.Notifications.SecondaryDisks[k] = vCopy
	}

	clampFloatField("notifications.disk_io.warning_threshold", &c.Notifications.DiskIO.WarningThreshold, 0, 100)

	// SMART devices
	normalizeListField("notifications.smart.devices", &c.Notifications.SMART.Devices)

	// Temperature
	clampFloatField("temperature.warning_threshold", &c.Temperature.WarningThreshold, 0, 120)
	clampFloatField("temperature.critical_threshold", &c.Temperature.CriticalThreshold, 0, 120)
	if c.Temperature.CriticalThreshold > 0 && c.Temperature.CriticalThreshold < c.Temperature.WarningThreshold {
		c.Temperature.CriticalThreshold = c.Temperature.WarningThreshold
		add("temperature.critical_threshold", fmt.Sprintf("%.2f", c.Temperature.CriticalThreshold))
	}

	// Critical containers
	normalizeListField("critical_containers", &c.CriticalContainers)

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
	// A zero here means "not configured": it is the value every config written
	// before the field existed decodes to, and clamping it to 1 would make every
	// container in the system heavy and turn a documented fallback into a restart
	// storm. It is normalized to the default instead, and the reader keeps its own
	// fallback for a Config built in memory.
	if c.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent <= 0 {
		c.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent = dockerHeavyMemPercent
		add("docker.auto_restart_on_ram_critical.heavy_container_mem_percent", dockerHeavyMemPercent)
	} else {
		clampFloatField("docker.auto_restart_on_ram_critical.heavy_container_mem_percent",
			&c.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent, 1, 100)
	}

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
		normalizeListField("fs_watchdog.deep_scan_paths", &c.FSWatchdog.DeepScanPaths)
	}
	normalizeListField("fs_watchdog.exclude_patterns", &c.FSWatchdog.ExcludePatterns)

	// Healthchecks
	clampIntField("healthchecks.period_seconds", &c.Healthchecks.PeriodSeconds, 10, 3600)
	clampIntField("healthchecks.grace_seconds", &c.Healthchecks.GraceSeconds, 10, 3600)
	trimField("healthchecks.ping_url", &c.Healthchecks.PingURL)
	if c.Healthchecks.PingURL != "" && !isHTTPURL(c.Healthchecks.PingURL) {
		c.Healthchecks.PingURL = ""
		add("healthchecks.ping_url", "dropped (not an http/https URL)")
	}
	if c.Healthchecks.GraceSeconds < c.Healthchecks.PeriodSeconds {
		c.Healthchecks.GraceSeconds = c.Healthchecks.PeriodSeconds
		add("healthchecks.grace_seconds", c.Healthchecks.GraceSeconds)
	}
	if c.Healthchecks.Enabled && c.Healthchecks.PingURL == "" {
		c.Healthchecks.Enabled = false
		add("healthchecks.enabled", false)
	}
	// A forced reboot needs a positive delay: at 0 it would fire the moment the
	// pinger misses a single probe. The value is normalized whether or not the
	// feature is on, so enabling it later cannot inherit a 0.
	if c.Healthchecks.ForceRebootAfterMins <= 0 {
		c.Healthchecks.ForceRebootAfterMins = defaultForceRebootAfterMins
		add("healthchecks.force_reboot_after_minutes", c.Healthchecks.ForceRebootAfterMins)
	} else {
		clampIntField("healthchecks.force_reboot_after_minutes", &c.Healthchecks.ForceRebootAfterMins, 1, 1440)
	}
	// Only relevant once healthchecks.io is actually in use: with no ping URL
	// there is nothing to watch, and the flag is inert. Checking it while the
	// feature is off would rewrite config.json on every boot (the default
	// template ships the flag on with an empty URL).
	if c.Healthchecks.Enabled && c.Healthchecks.ForceRebootOnDown && c.Healthchecks.PingURL == "" {
		c.Healthchecks.ForceRebootOnDown = false
		add("healthchecks.force_reboot_on_prolonged_down", false)
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
	if !isPingHostName(c.NetworkWatchdog.Gateway) && c.NetworkWatchdog.Gateway != "" {
		add("network_watchdog.gateway", "dropped "+c.NetworkWatchdog.Gateway)
		c.NetworkWatchdog.Gateway = ""
	}
	if c.NetworkWatchdog.DNSHost == "" {
		c.NetworkWatchdog.DNSHost = "quad9.net"
		add("network_watchdog.dns_host", c.NetworkWatchdog.DNSHost)
	}
	if len(c.NetworkWatchdog.Targets) > 0 {
		normalizeListField("network_watchdog.targets", &c.NetworkWatchdog.Targets)
		validTargets := c.NetworkWatchdog.Targets[:0]
		for _, tgt := range c.NetworkWatchdog.Targets {
			if isPingHostName(tgt) {
				validTargets = append(validTargets, tgt)
			} else {
				add("network_watchdog.targets", "dropped "+tgt)
			}
		}
		c.NetworkWatchdog.Targets = validTargets
	}
	if len(c.NetworkWatchdog.Targets) == 0 {
		c.NetworkWatchdog.Targets = []string{"9.9.9.9", "1.1.1.1"}
		add("network_watchdog.targets", "default")
	}

	// Raid watchdog
	clampIntField("raid_watchdog.check_interval_seconds", &c.RaidWatchdog.CheckIntervalSecs, 30, 7200)
	clampIntField("raid_watchdog.cooldown_minutes", &c.RaidWatchdog.CooldownMins, 1, 1440)

	// Update
	clampIntField("update.check_interval_hours", &c.Update.CheckIntervalHours, 1, 168)

	// Backup: a negative target id is not a Telegram user id. Zero is the
	// documented "same as allowed_user_id" sentinel, so it stays.
	if c.Backup.TargetUserID < 0 {
		c.Backup.TargetUserID = 0
		add("backup.target_user_id", 0)
	}

	// AdBlock
	switch strings.ToLower(strings.TrimSpace(c.AdBlock.Type)) {
	case "pihole", "adguard":
		normalized := strings.ToLower(strings.TrimSpace(c.AdBlock.Type))
		if normalized != c.AdBlock.Type {
			c.AdBlock.Type = normalized
			add("adblock.type", normalized)
		}
	default:
		c.AdBlock.Type = "pihole"
		add("adblock.type", "pihole")
	}

	// Shell command (/cmd). The allowlist is a security boundary, so it is
	// validated rather than merely trimmed: an entry that is not a bare binary
	// name is dropped and the drop is reported, because a silently accepted
	// entry that the executor would refuse at run time reads as "allowlisted
	// but broken" and invites a retry with a path.
	normalizeListField("shell_command.allowed_binaries", &c.ShellCommand.AllowedBinaries)
	if kept, dropped := filterShellBinaries(c.ShellCommand.AllowedBinaries); len(dropped) > 0 {
		c.ShellCommand.AllowedBinaries = kept
		add("shell_command.allowed_binaries", "dropped "+strings.Join(dropped, ", "))
	}
	if c.ShellCommand.Enabled {
		for _, name := range c.ShellCommand.AllowedBinaries {
			if cmdexec.IsEscapeBinary(name) {
				slog.Warn("shell_command.allowed_binaries contains a binary that can run arbitrary code; "+
					"the allowlist is not a boundary for it", "binary", name)
			}
		}
	}
	if c.ShellCommand.Enabled && len(c.ShellCommand.AllowedBinaries) == 0 {
		// "Enabled" with nothing to run is indistinguishable from broken at the
		// chat, and it is a state nobody chose on purpose. Refuse it here so the
		// reason is written to config.json once, at load time.
		c.ShellCommand.Enabled = false
		add("shell_command.enabled", false)
	}
	if c.ShellCommand.TimeoutSeconds <= 0 {
		c.ShellCommand.TimeoutSeconds = defaultShellCommandTimeoutSeconds
		add("shell_command.timeout_seconds", c.ShellCommand.TimeoutSeconds)
	} else {
		clampIntField("shell_command.timeout_seconds", &c.ShellCommand.TimeoutSeconds,
			minShellCommandTimeoutSeconds, maxShellCommandTimeoutSeconds)
	}
	if c.ShellCommand.MaxOutputChars <= 0 {
		c.ShellCommand.MaxOutputChars = shellCommandOutputChars
		add("shell_command.max_output_chars", c.ShellCommand.MaxOutputChars)
	} else {
		clampIntField("shell_command.max_output_chars", &c.ShellCommand.MaxOutputChars,
			minShellCommandOutputChars, shellCommandOutputChars)
	}

	sort.Strings(changes)
	return changes
}

// maxShellBinaryNameLen is the length ceiling for an allowlist entry. No real
// binary comes close; a longer entry is a path or an argument list, both of
// which the executor would not accept as a command name anyway.
const maxShellBinaryNameLen = 64

// filterShellBinaries splits an allowlist into the entries that are plain
// binary names and the ones that are not.
//
// A valid entry is a single path-free name: no slash (so neither an absolute
// path nor a relative traversal can name a different executable than the one
// the operator wrote), no whitespace, and no shell metacharacter. Rejecting
// the metacharacters is not about the shell — nothing re-parses these strings
// — but so that a list entry can never be a fragment of a command line
// written in the belief that it would be interpreted.
func filterShellBinaries(entries []string) (kept, dropped []string) {
	kept = make([]string, 0, len(entries))
	for _, entry := range entries {
		if isShellBinaryName(entry) && !cmdexec.IsDeniedBinary(entry) {
			kept = append(kept, entry)
			continue
		}
		dropped = append(dropped, entry)
	}
	return kept, dropped
}

// isShellBinaryName reports whether entry is a bare executable name.
func isShellBinaryName(entry string) bool {
	if entry == "" || len(entry) > maxShellBinaryNameLen {
		return false
	}
	// "." and ".." are not binaries, and any separator would let the entry
	// escape the PATH lookup that resolves it.
	if entry == "." || entry == ".." || strings.ContainsAny(entry, `/\`) {
		return false
	}
	for _, r := range entry {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
}

// defaultReportTimes returns a copy of the default report schedule.
func defaultReportTimes() []TimeConfig {
	return []TimeConfig{{Hour: 7, Minute: 30}, {Hour: 18, Minute: 30}}
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

// isHTTPURL reports whether raw is an absolute http or https URL with a host.
func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// isPingHostName accepts an IP or hostname made only of letters, digits, dots,
// dashes, underscores and colons (IPv6), and never one starting with "-" which
// ping would take for an option.
func isPingHostName(h string) bool {
	if h == "" || len(h) > 253 || strings.HasPrefix(h, "-") {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '-' || r == '_' || r == ':':
		default:
			return false
		}
	}
	return true
}
