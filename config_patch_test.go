package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyConfigPatch_SanitizesAndIgnoresLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	originalConfigFile := configFile
	configFile = path
	defer func() {
		configFile = originalConfigFile
	}()

	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	patch := map[string]interface{}{
		"bot_token": "should_be_ignored",
		"quiet_hours": map[string]interface{}{
			"enabled":      true,
			"start_hour":   7,
			"start_minute": 0,
			"end_hour":     7,
			"end_minute":   0,
		},
		"notifications": map[string]interface{}{
			"cpu": map[string]interface{}{
				"warning_threshold":  90,
				"critical_threshold": 50,
			},
		},
	}

	result, err := applyConfigPatch(patch)
	if err != nil {
		t.Fatalf("apply config patch: %v", err)
	}

	if !containsString(result.Ignored, "bot_token") {
		t.Fatalf("expected bot_token to be ignored: %#v", result.Ignored)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	if saved.QuietHours.Enabled {
		t.Fatalf("expected quiet hours disabled when start=end")
	}
	if saved.Notifications.CPU.CriticalThreshold != saved.Notifications.CPU.WarningThreshold {
		t.Fatalf("expected cpu critical to match warning after sanitize")
	}
	if !containsString(result.Corrected, "quiet_hours.enabled") {
		t.Fatalf("expected corrected quiet_hours.enabled in result: %#v", result.Corrected)
	}
}

func containsString(list []string, needle string) bool {
	for _, v := range list {
		if v == needle || strings.HasPrefix(v, needle+" ") {
			return true
		}
	}
	return false
}
