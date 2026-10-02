package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// configFileName is the default config file name, also used to build the
	// backup file name.
	configFileName = "config.json"

	// configFileMode is the permission the config file must have: it holds
	// bot_token and gemini_api_key.
	configFileMode fs.FileMode = 0o600
)

var (
	// cfg is a legacy mirror of the configuration, kept so that runtime_main.go
	// can hand &cfg to InitApp and so that the standalone watchdog binary can
	// read the configuration without an AppContext.
	//
	// It is a mirror, not the source of truth: the source of truth is the
	// snapshot published by loadConfig through AppContext.SetConfig (bot) and
	// FSWatchdog.publishConfig (watchdog). Reading cfg directly is unsafe once
	// a reload can happen, because a reload rewrites it in place; new code must
	// use currentCfg(), which returns the published snapshot.
	cfgMu sync.RWMutex
	cfg   Config

	// Default paths
	defaultPathSSD = "/Volume1"

	// Config file
	configFile = configFileName
)

// ConfigPatchResult reports what applyConfigPatch refused to apply and what it
// had to correct on the way.
type ConfigPatchResult struct {
	// Ignored lists the patch keys that were refused: locked fields and keys
	// that Config does not declare. Callers must show these to the user, a
	// silently dropped key reads as "applied" but did nothing.
	Ignored []string
	// Corrected lists the sanitizing clamps applied to the written file.
	Corrected []string
}

// loadConfig reads the configuration, sanitizes it and returns the new
// snapshot. The returned Config is never modified afterwards, so publishing it
// with SetConfig is safe while other goroutines are reading.
//
// It is called both at bootstrap (no AppContext yet, so it also fills cfg for
// InitApp) and on reload (AppContext present, so it publishes a new pointer).
// A broken configuration is fatal here: starting with invented settings would
// send alerts to the wrong chat or watch the wrong disks.
//
// A *missing* file is the one exception: it is not fatal here, it yields
// defaultConfigTemplate(). See missingConfigTemplate for why, and
// validateRequiredFields — still fatal, still in this function — for what
// happens when the defaults have no credentials: the bot stops with the names of
// the missing fields, and the test binary of this package gets far enough to run.
func loadConfig() *Config {
	path, err := resolveConfigPath()
	if err != nil {
		slog.Error("Invalid configuration path", "err", err)
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	// Remember the resolved path for the log lines of the watchdog binary, but
	// only at bootstrap: writing a package variable during a reload is exactly
	// the shared mutable state this design is removing.
	if app == nil {
		configFile = path
	}

	configMap, err := readConfigFile(path)
	if err != nil {
		// Only ENOENT is recoverable. A file that exists but cannot be read, or
		// cannot be parsed with a usable backup, is a real fault: continuing
		// there would replace the operator's settings with the defaults.
		if errors.Is(err, fs.ErrNotExist) {
			return publishDefaultConfig(path, err)
		}
		slog.Error("Failed to read config", "file", path, "err", err)
		fmt.Printf("Error: cannot read %s: %v\n", path, err)
		fmt.Println("Create it starting from config.example.json and set bot_token and allowed_user_id.")
		os.Exit(1)
	}

	loaded, defaultsAdded, changes, err := decodeConfigMap(configMap)
	if err != nil {
		slog.Error("Failed to decode config", "file", path, "err", err)
		fmt.Printf("Error: %s is not valid: %v\n", path, err)
		os.Exit(1)
	}

	// Only write when something actually changed: rewriting on every boot
	// touched the file needlessly and reported a correction that was a no-op.
	if defaultsAdded || len(changes) > 0 {
		if err := writeConfigFile(path, loaded, configMap); err != nil {
			slog.Error("Failed to save corrected config", "err", err)
		} else {
			slog.Warn("Config corrected", "defaults_added", defaultsAdded, "changes", changes)
		}
	} else {
		hardenConfigPermissions(path)
	}

	if err := validateRequiredFields(loaded); err != nil {
		slog.Error("Configuration incomplete", "err", err)
		fmt.Printf("Error: %v\n", err)
		fmt.Printf("Set %s in %s, then start NASBot again.\n", missingFieldNames(loaded), path)
		os.Exit(1)
	}

	if loaded.Paths.SSD == "" {
		loaded.Paths.SSD = defaultPathSSD
	}
	if loaded.Notifications.SecondaryDisks == nil {
		loaded.Notifications.SecondaryDisks = make(map[string]ResourceConfig)
	}

	publishConfig(loaded)

	slog.Info("Configuration loaded successfully",
		"ssd", loaded.Paths.SSD,
		"stats_interval", time.Duration(loaded.Intervals.StatsSeconds)*time.Second,
		"monitor_interval", time.Duration(loaded.Intervals.MonitorSeconds)*time.Second)

	return loaded
}

// publishDefaultConfig publishes defaultConfigTemplate() when no config file is
// there to read, and returns it.
//
// Why this is not fatal: this package's init() calls loadConfig, so every test
// binary of internal/app ran it. With no config.json in the working directory
// (config.json is gitignored, it holds the bot token) the binary called
// os.Exit(1) from init and the whole package reported FAIL with zero tests run:
// `go test ./internal/app/` was green only under scripts/ci_guard.sh, which
// copies config.example.json first. A file that is absent cannot be wrong, so it
// is not treated as a broken configuration.
//
// What is deliberately NOT done here: writing the defaults to disk. A generated
// config.json would be a file the operator never chose, and /configset would
// then patch and rewrite that file — on a NAS where the real one lives on
// another volume, overwriting the actual configuration with the defaults plus a
// patch. Better to run on defaults and to leave the decision to the operator.
func publishDefaultConfig(path string, cause error) *Config {
	loaded := defaultConfigTemplate()
	sanitizeConfig(&loaded)

	slog.Warn("No config file, starting from built-in defaults",
		"file", path, "cause", cause,
		"hint", "copy config.example.json to "+path+" and set bot_token and allowed_user_id")
	fmt.Printf("Warning: %s not found, starting from built-in defaults.\n", path)
	fmt.Println("Copy config.example.json to " + path + " and set bot_token and allowed_user_id.")
	fmt.Println("The file has NOT been created: /configset would rewrite it instead of your configuration.")

	// validateRequiredFields is deliberately not called here: this function is
	// the one loadConfig path that must return, so that a test binary with no
	// config.json reaches its tests. A real bot stops one step later, in
	// RunBot, where "no credentials" is exactly the reason to stop.
	if err := validateRequiredFields(&loaded); err != nil {
		slog.Warn("Built-in defaults are not runnable", "missing", missingFieldNames(&loaded))
	} else {
		slog.Info("Running on built-in defaults (no config file)", "ssd", loaded.Paths.SSD)
	}

	publishConfig(&loaded)
	return &loaded
}

// missingFieldNames returns the required fields the configuration does not
// satisfy, for a message that names them. Empty when the configuration is
// complete.
func missingFieldNames(c *Config) string {
	var missing []string
	if strings.TrimSpace(c.BotToken) == "" {
		missing = append(missing, "bot_token")
	}
	if c.AllowedUserID <= 0 {
		missing = append(missing, fmt.Sprintf("allowed_user_id=%d", c.AllowedUserID))
	}
	return strings.Join(missing, " and ")
}

// publishConfig makes loaded the configuration every reader sees.
//
// At bootstrap there is no AppContext yet, so the package-level cfg is filled
// for InitApp. On reload the snapshot is published through SetConfig and cfg is
// left alone: it is only ever read by code that has not been migrated off the
// deprecated field, and writing it would mutate a struct those readers hold.
func publishConfig(loaded *Config) {
	if app != nil {
		// A reload. Swap the published pointer and leave cfg alone: InitApp was
		// given &cfg, so writing here would mutate the snapshot every reader
		// is holding, which is the race this design exists to remove.
		app.SetConfig(loaded)
	} else {
		// Bootstrap, or the standalone watchdog binary, which has no
		// AppContext. cfg is not published anywhere yet, so writing it is safe.
		cfgMu.Lock()
		cfg = *loaded
		cfgMu.Unlock()
	}

	// The watchdog binary has no AppContext; the singleton keeps its own
	// snapshot so its loop reads the same values as everything else.
	if watchdog := GetFSWatchdog(); watchdog != nil {
		watchdog.publishConfig(loaded)
	}
}

// currentCfg returns the configuration snapshot readers should use. It is valid
// even before loadConfig has run, so a caller can never observe a zero Config.
func currentCfg() *Config {
	if app != nil {
		if snapshot := app.Cfg(); snapshot != nil {
			return snapshot
		}
	}
	if watchdog := GetFSWatchdog(); watchdog != nil {
		if snapshot := watchdog.Cfg(); snapshot != nil {
			return snapshot
		}
	}
	cfgMu.RLock()
	defer cfgMu.RUnlock()
	snapshot := cfg
	return &snapshot
}

// resolveConfigPath returns the config file to use.
//
// NASBOT_CONFIG is honoured only if it passes validateConfigPath: saveConfig
// rewrites this file in place, so accepting an arbitrary path would let
// NASBot overwrite whatever it points at. A rejected path is fatal rather than
// silently falling back to ./config.json, because starting with a different
// configuration than the operator asked for is worse than not starting.
func resolveConfigPath() (string, error) {
	if envPath := strings.TrimSpace(os.Getenv("NASBOT_CONFIG")); envPath != "" {
		if err := validateConfigPath(envPath); err != nil {
			return "", fmt.Errorf("NASBOT_CONFIG=%q rejected: %w", envPath, err)
		}
		return envPath, nil
	}

	if filepath.IsAbs(configFile) {
		return configFile, nil
	}

	candidates := []string{
		configFile,
		filepath.Join("..", configFile),
		filepath.Join("..", "..", configFile),
	}

	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return configFile, nil
}

// validateConfigPath checks that an explicitly configured config file is safe to
// read secrets from and to overwrite.
func validateConfigPath(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("file does not exist")
		}
		return fmt.Errorf("not reachable: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file (mode %s)", info.Mode())
	}
	base := filepath.Base(path)
	if base != configFileName && filepath.Ext(base) != ".json" {
		return fmt.Errorf("file name %q is not a JSON config file", base)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("permissions %04o expose bot_token to other users, want 0600", mode)
	}
	return nil
}

// validateRequiredFields rejects a configuration NASBot cannot run with. These
// are hard errors, not warnings: with allowed_user_id = 0 the bot ignores every
// incoming message and stays silently deaf, which looks like "Telegram is down"
// to whoever installed it.
func validateRequiredFields(c *Config) error {
	if strings.TrimSpace(c.BotToken) == "" {
		return errors.New("bot_token is empty: set it in the config file")
	}
	if c.AllowedUserID <= 0 {
		return fmt.Errorf("allowed_user_id is %d: it must be a positive Telegram user id, otherwise every message is ignored", c.AllowedUserID)
	}
	return nil
}

// readConfigFile reads and parses the config file, falling back to the .bak copy
// written before the last rewrite if the main file is not valid JSON.
func readConfigFile(path string) (map[string]interface{}, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(fileContent, &configMap); err != nil {
		restored, restoreErr := restoreFromBackup(path)
		if restoreErr != nil {
			return nil, fmt.Errorf("%w (no usable backup: %v)", err, restoreErr)
		}
		return restored, nil
	}
	return configMap, nil
}

// restoreFromBackup replaces a corrupt config file with its backup and returns
// the parsed backup. Without this a crash during a rewrite left bot_token
// truncated, Unmarshal failed and the bot exited in a restart loop with no way
// out but editing the file by hand.
func restoreFromBackup(path string) (map[string]interface{}, error) {
	bakPath := path + ".bak"
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		return nil, err
	}
	var restored map[string]interface{}
	if err := json.Unmarshal(bakContent, &restored); err != nil {
		return nil, err
	}
	slog.Error("Config file was not valid JSON, restored from backup",
		"file", path, "backup", bakPath, "restored_keys", len(restored))
	return restored, nil
}

// decodeConfigMap fills in the missing defaults, decodes a brand new Config and
// sanitizes it. It never touches an already published snapshot.
func decodeConfigMap(configMap map[string]interface{}) (*Config, bool, []string, error) {
	defaultsAdded := fillMissingConfigFields(configMap)

	mergedContent, err := json.Marshal(configMap)
	if err != nil {
		return nil, defaultsAdded, nil, err
	}

	loaded := &Config{}
	if err := json.Unmarshal(mergedContent, loaded); err != nil {
		return nil, defaultsAdded, nil, err
	}

	changes := sanitizeConfig(loaded)
	return loaded, defaultsAdded, changes, nil
}

// hardenConfigPermissions tightens an existing config file to 0600.
//
// os.WriteFile only applies its mode when it creates the file, so a config.json
// created by `cp config.example.json config.json` kept whatever mode cp gave it
// and bot_token stayed world-readable for the whole life of the install.
func hardenConfigPermissions(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm() == configFileMode {
		return
	}
	if err := os.Chmod(path, configFileMode); err != nil {
		slog.Warn("Failed to restrict config file permissions", "file", path, "err", err)
		return
	}
	slog.Warn("Restricted config file permissions", "file", path, "from", info.Mode().Perm().String(), "to", configFileMode.String())
}

// writeConfigFile persists c, preserving the keys of the original file that
// Config does not declare.
//
// The write goes to a temporary file in the same directory, is fsynced, and is
// then renamed over the target, so an interrupted write can never truncate
// bot_token. The previous content is kept as <path>.bak. The final chmod is
// explicit because Rename carries the temporary file's mode, not the target's.
func writeConfigFile(path string, c *Config, original map[string]interface{}) error {
	typedContent, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	// Merge the unrecognized keys of the file back in before writing.
	var merged map[string]interface{}
	if err := json.Unmarshal(typedContent, &merged); err != nil {
		return err
	}
	mergeUnknownKeys(merged, original)
	finalContent, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}

	if err := backupConfigFile(path); err != nil {
		slog.Warn("Failed to back up config file", "file", path, "err", err)
	}
	return writeFileAtomic(path, finalContent, configFileMode)
}

// backupConfigFile copies the current config file to <path>.bak.
func backupConfigFile(path string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return writeFileAtomic(path+".bak", current, configFileMode)
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory, then renames it into place.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nasbot-config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	// Remove the temporary file on any failure: a rename would have moved it
	// away already, and a leftover .tmp in the config directory is litter that
	// the next start-up would ignore.
	defer func() {
		if tmp != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// fsync before rename: rename is atomic, the content it points at is not
	// durable until the file's own data reaches the disk.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// mergeUnknownKeys copies the keys of src that dest does not declare into dest,
// recursing into sections both sides know about. Keys of unknown sections are
// copied whole.
//
// Marshalling Config alone drops every custom key, so the first rewrite after
// an upgrade silently deleted whatever the user had added by hand.
func mergeUnknownKeys(dest, src map[string]interface{}) {
	for k, v := range src {
		if existing, ok := dest[k]; ok {
			destMap, destIsMap := existing.(map[string]interface{})
			srcMap, srcIsMap := v.(map[string]interface{})
			if destIsMap && srcIsMap {
				mergeUnknownKeys(destMap, srcMap)
			}
			continue
		}
		dest[k] = v
	}
}

// getConfigJSONSafe returns the current config as JSON with the secrets redacted.
func getConfigJSONSafe() (string, error) {
	current := currentCfg()
	if current == nil {
		return "", errors.New("configuration not loaded yet")
	}

	safeCfg := *current
	safeCfg.BotToken = "REDACTED"
	safeCfg.GeminiAPIKey = "REDACTED"
	safeCfg.AdBlock.Token = "REDACTED"
	safeCfg.Healthchecks.PingURL = redactPingURL(safeCfg.Healthchecks.PingURL)

	b, err := json.MarshalIndent(safeCfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// redactPingURL hides the UUID in a healthchecks.io ping URL. That UUID is the
// only thing that lets anyone publish to this check, so printing it in chat
// hands out the uptime of the NAS.
func redactPingURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return "REDACTED"
	}
	return (&url.URL{
		Scheme:   parsed.Scheme,
		Host:     parsed.Host,
		Path:     "/REDACTED",
		RawQuery: "",
	}).String()
}

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
		c.Reports.Times = validTimes
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
	if c.NetworkWatchdog.DNSHost == "" {
		c.NetworkWatchdog.DNSHost = "quad9.net"
		add("network_watchdog.dns_host", c.NetworkWatchdog.DNSHost)
	}
	if len(c.NetworkWatchdog.Targets) > 0 {
		normalizeListField("network_watchdog.targets", &c.NetworkWatchdog.Targets)
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
		if isShellBinaryName(entry) {
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

// lockedConfigPaths are the keys /configset must never write. bot_token and
// allowed_user_id are credentials: they are the access control of this bot, and
// letting a chat rewrite them turns a stolen allowed_user_id into full control.
// auto_apply downloads and runs a release without asking: it is not a setting, it
// is remote code execution.
var lockedConfigPaths = map[string]struct{}{
	"bot_token":         {},
	"allowed_user_id":   {},
	"update.auto_apply": {},
	// Enabling /cmd from a chat message is execution of code, not a setting:
	// the same class as update.auto_apply. It stays an explicit edit of
	// config.json followed by a restart.
	"shell_command": {},
}

// applyConfigPatch applies a JSON patch to the config file and republishes the
// configuration.
//
// The patch is filtered against the keys Config actually declares, so an
// arbitrary path can no longer reach a field nobody intended to expose. Every
// refused key is reported back: a silently dropped key looks applied to the user
// who sent it.
func applyConfigPatch(patch map[string]interface{}) (ConfigPatchResult, error) {
	result := ConfigPatchResult{}
	if len(patch) == 0 {
		return result, nil
	}

	path, err := resolveConfigPath()
	if err != nil {
		return result, err
	}

	configMap, err := readConfigFile(path)
	if err != nil {
		return result, err
	}

	allowed, ignored := filterPatchKeys(patch, "")
	sort.Strings(ignored)
	result.Ignored = ignored
	if len(allowed) == 0 {
		return result, nil
	}

	// Merge patch
	deepMerge(configMap, allowed)
	fillMissingConfigFields(configMap)

	updated, _, corrected, err := decodeConfigMap(configMap)
	if err != nil {
		return result, err
	}

	if err := writeConfigFile(path, updated, configMap); err != nil {
		return result, err
	}

	publishConfig(updated)

	result.Corrected = corrected
	return result, nil
}

// filterPatchKeys drops every key of patch that is not a field of Config, plus
// the locked ones, and reports their dotted paths.
func filterPatchKeys(patch map[string]interface{}, prefix string) (map[string]interface{}, []string) {
	allowed := make(map[string]interface{}, len(patch))
	ignored := make([]string, 0)

	for k, v := range patch {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		if _, locked := lockedConfigPaths[path]; locked {
			ignored = append(ignored, path)
			continue
		}

		kind, known := configPathKinds[path]
		if !known {
			ignored = append(ignored, path)
			continue
		}

		switch kind {
		case configPathFreeForm:
			clean, subIgnored := filterFreeFormMap(v, path)
			ignored = append(ignored, subIgnored...)
			if len(clean) > 0 {
				allowed[k] = clean
			}
		case configPathSection:
			nested, ok := v.(map[string]interface{})
			if !ok {
				// A scalar where a section belongs: refuse rather than replace the
				// whole section with a string.
				ignored = append(ignored, path)
				continue
			}
			subAllowed, subIgnored := filterPatchKeys(nested, path)
			ignored = append(ignored, subIgnored...)
			if len(subAllowed) > 0 {
				allowed[k] = subAllowed
			}
		case configPathLeaf:
			if _, isMap := v.(map[string]interface{}); isMap {
				ignored = append(ignored, path)
				continue
			}
			allowed[k] = v
		}
	}

	return allowed, ignored
}

// filterFreeFormMap checks the entries of a map keyed by data against the fields
// of its value type. The keys are mount points and stay as they are; only the
// contents of each entry are checked.
func filterFreeFormMap(v interface{}, prefix string) (map[string]interface{}, []string) {
	nested, ok := v.(map[string]interface{})
	if !ok {
		return nil, []string{prefix}
	}
	allowed := make(map[string]interface{}, len(nested))
	ignored := make([]string, 0)
	for key, entry := range nested {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			ignored = append(ignored, prefix+"."+key)
			continue
		}
		clean := make(map[string]interface{}, len(entryMap))
		for field, value := range entryMap {
			fieldPath := prefix + "." + field
			if _, known := configPathKinds[fieldPath]; !known {
				ignored = append(ignored, fieldPath)
				continue
			}
			clean[field] = value
		}
		if len(clean) > 0 {
			allowed[key] = clean
		}
	}
	return allowed, ignored
}

// configPathKind describes what may appear under a known configuration path.
type configPathKind uint8

const (
	// configPathLeaf is a scalar or a list.
	configPathLeaf configPathKind = iota
	// configPathSection is a nested struct: its keys must be known fields.
	configPathSection
	// configPathFreeForm is a map keyed by data (a mount point), whose own keys
	// are arbitrary but whose entries are still checked.
	configPathFreeForm
)

// configPathKinds maps every dotted JSON path of Config to what it accepts. It
// is derived from the struct tags, so a new field is writable by default and a
// typo is refused, with no second list to keep in sync by hand.
var configPathKinds = buildConfigPathKinds()

func buildConfigPathKinds() map[string]configPathKind {
	kinds := make(map[string]configPathKind)
	collectConfigPaths(reflect.TypeOf(Config{}), "", kinds)
	return kinds
}

func collectConfigPaths(t reflect.Type, prefix string, out map[string]configPathKind) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		switch {
		case isMapOfStruct(field.Type):
			// A map keyed by data (a mount point). The entries are not fields, but
			// what sits inside each entry is: register the value type's fields so
			// an entry cannot smuggle in an unknown key either.
			out[path] = configPathFreeForm
			collectConfigPaths(field.Type.Elem(), path, out)
		case isStructLike(field.Type):
			out[path] = configPathSection
			collectConfigPaths(field.Type, path, out)
		default:
			out[path] = configPathLeaf
		}
	}
}

func isMapOfStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Map && isStructLike(t.Elem())
}

func isStructLike(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

func deepMerge(dest, src map[string]interface{}) {
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			if destMap, ok := dest[k].(map[string]interface{}); ok {
				deepMerge(destMap, vMap)
				continue
			}
		}
		dest[k] = v
	}
}

// describeCallbackRef renders a stable short token for a mount path.
//
// Inline callback data is capped at 64 bytes by Telegram. Encoding the mount
// inline and cutting it to fit was wrong three times over: the cut can land in
// the middle of a UTF-8 rune, which makes Telegram reject the whole keyboard;
// two mounts sharing the first bytes collide onto the same entry, so the buttons
// write to whichever one the config map happened to resolve; and the rebuilt
// buttons create a second, truncated entry in config.json.
var callbackRefs = struct {
	mu   sync.RWMutex
	refs map[string]string
}{refs: map[string]string{}}

func callbackRefFor(mount string) string {
	callbackRefs.mu.RLock()
	ref, ok := callbackRefs.refs[mount]
	callbackRefs.mu.RUnlock()
	if ok {
		return ref
	}

	// The token is the index in the sorted mount list, so it is stable for the
	// lifetime of the process and never collides between two mounts.
	ref = mountCallbackRef(mount)

	callbackRefs.mu.Lock()
	callbackRefs.refs[mount] = ref
	callbackRefs.mu.Unlock()
	return ref
}

// mountCallbackRef builds the token for mount from a hash of the path. A hash is
// used instead of a plain index so that adding a disk does not renumber the
// callbacks of the others, which would break keyboards already on screen.
func mountCallbackRef(mount string) string {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(mount))
	return "d" + strconv.FormatUint(uint64(sum.Sum32()%1_000_000), 36)
}

// resolveCallbackRef maps a token or a raw mount path back to the mount path.
// Tokens are tried first; anything else is treated as a literal path so that a
// keyboard built before the upgrade keeps working.
func resolveCallbackRef(ref string) string {
	if mount := lookupCallbackMounts(ref); mount != "" {
		return mount
	}
	return ref
}

// lookupCallbackMounts returns the mount registered under ref, or "".
func lookupCallbackMounts(ref string) string {
	callbackRefs.mu.RLock()
	defer callbackRefs.mu.RUnlock()
	for mount, known := range callbackRefs.refs {
		if known == ref {
			return mount
		}
	}
	return ""
}

// registerCallbackMounts records the mount -> token pairs of the mounts on
// screen, so the tokens resolve when a button is pressed.
func registerCallbackMounts(mounts []string) {
	for _, mount := range mounts {
		callbackRefFor(mount)
	}
}

// callbackDataFits reports whether payload is within Telegram's limit.
func callbackDataFits(payload string) bool {
	return len(payload) <= telegramCallbackDataLimit
}

// telegramCallbackDataLimit is the hard cap Telegram puts on inline callback
// data.
const telegramCallbackDataLimit = 64
