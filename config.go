package main

import (
"encoding/json"
"fmt"
"log/slog"
"os"
"time"
)

// Global computed values (Shared by main bot and watchdog)
var (
// Global config object
cfg Config

// Computed values
BotToken      string
AllowedUserID int64
PathSSD       = "/Volume1"
PathHDD       = "/Volume2"

// Intervals
IntervalStats   = 5 * time.Second
IntervalMonitor = 30 * time.Second

// Config file
configFile = "config.json"
)

// loadConfig reads config.json and populates the global Config struct
func loadConfig() {
	file, err := os.Open(configFile)
	if err != nil {
		slog.Error("Config file not found", "file", configFile, "err", err)
		fmt.Println("Error: config.json not found. Please create it based on config.example.json")
		os.Exit(1)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&cfg); err != nil {
		slog.Error("Failed to parse config", "err", err)
		os.Exit(1)
	}

	// Apply loaded configuration to shared globals
	BotToken = cfg.BotToken
	AllowedUserID = cfg.AllowedUserID

	if cfg.Paths.SSD != "" {
		PathSSD = cfg.Paths.SSD
	}
	if cfg.Paths.HDD != "" {
		PathHDD = cfg.Paths.HDD
	}

	if cfg.Intervals.StatsSeconds > 0 {
		IntervalStats = time.Duration(cfg.Intervals.StatsSeconds) * time.Second
	}
	if cfg.Intervals.MonitorSeconds > 0 {
		IntervalMonitor = time.Duration(cfg.Intervals.MonitorSeconds) * time.Second
	}

	slog.Info("Configuration loaded successfully",
"ssd", PathSSD,
"hdd", PathHDD,
"stats_interval", IntervalStats,
"monitor_interval", IntervalMonitor)
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

// applyConfigPatch updates the config with a patch
func applyConfigPatch(patch map[string]interface{}) ([]string, error) {
	fileContent, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(fileContent, &configMap); err != nil {
		return nil, err
	}

	// Merge patch
	for k, v := range patch {
		configMap[k] = v
	}

	// Serialize
	newContent, err := json.MarshalIndent(configMap, "", "  ")
	if err != nil {
		return nil, err
	}

	// Save
	if err := os.WriteFile(configFile, newContent, 0644); err != nil {
		return nil, err
	}

	// Reload
	loadConfig()
	
	// Track changed keys
	keys := make([]string, 0, len(patch))
	for k := range patch {
		keys = append(keys, k)
	}
	return keys, nil
}
