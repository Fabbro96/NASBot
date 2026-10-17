package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"sync"
	"sync/atomic"
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
	// bootCfg is the configuration loaded before an AppContext exists: it is
	// what InitApp is built from, and what the standalone watchdog binary reads
	// without an AppContext. After bootstrap the source of truth is the
	// snapshot published through AppContext.SetConfig (bot) and
	// FSWatchdog.publishConfig (watchdog); readers use currentCfg().
	bootCfg atomic.Pointer[Config]

	// configWriteMu serializza ogni riscrittura di config.json: la
	// read-modify-write di applyConfigPatch (usata dalla UI e dalla sync
	// periodica dei dispositivi SMART) e la write di loadConfig. Senza questo
	// mutex due patch simultanee si leggono a vicenda il file e l'ultima
	// cancella la chiave scritta dall'altra, un dato perso che nessun errore
	// segnala. Non è uno dei mutex con deadlock detection: non viene mai
	// acquisito mentre si tiene un altro lock, non c'è un ordine di lock da
	// violare.
	configWriteMu sync.Mutex

	// Default paths
	defaultPathSSD = "/Volume1"

	// Config file
	configFile = configFileName
)

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

	// Sync dei dispositivi SMART con i dischi realmente presenti in sysfs.
	// Prima della decisione di scrittura, così una lista cambiata entra in
	// "qualcosa è cambiato" e riusa la writeConfigFile qui sotto: nessun
	// percorso di scrittura dedicato. Una detection vuota o fallita non
	// modifica nulla (syncSMARTDevicesAtBoot), quindi non forza nemmeno la
	// riscrittura. Saltata sotto go test: vedi smartBootSyncAtLoad.
	if smartBootSyncAtLoad {
		changes = append(changes, syncSMARTDevicesAtBoot(loaded)...)
	}

	// Only write when something actually changed: rewriting on every boot
	// touched the file needlessly and reported a correction that was a no-op.
	//
	// Il lock resta preso fino a dopo publishConfig, con lo stesso percorso
	// di applyConfigPatch: tra la write del file e la pubblicazione della
	// snapshot non ci deve passare nessuna patch, che altrimenti leggerebbe
	// (e riscriverebbe) il file con un contenuto che il caricamento sta per
	// sostituire. publishConfig prende cfgMu solo dopo configWriteMu, ordine
	// già stabilito da applyConfigPatch, quindi qui non si inverte nulla.
	configWriteMu.Lock()
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
	configWriteMu.Unlock()

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
// At bootstrap there is no AppContext yet, so bootCfg is filled for InitApp. On
// reload the snapshot is published through SetConfig and bootCfg is left alone.
func publishConfig(loaded *Config) {
	if app != nil {
		// A reload. Swap the published pointer; the snapshots readers hold are
		// never modified.
		app.SetConfig(loaded)
	} else {
		// Bootstrap, or the standalone watchdog binary, which has no
		// AppContext. Nothing else holds this pointer yet.
		bootCfg.Store(loaded)
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
	if boot := bootCfg.Load(); boot != nil {
		return boot
	}
	return &Config{}
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
