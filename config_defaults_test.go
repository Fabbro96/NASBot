package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFillMissingConfigFields_PreservesExistingValues(t *testing.T) {
	cfgMap := map[string]interface{}{
		"timezone": "UTC",
		"reports": map[string]interface{}{
			"enabled": false,
		},
	}

	changed := fillMissingConfigFields(cfgMap)
	if !changed {
		t.Fatalf("expected missing fields to be added")
	}

	reports, ok := cfgMap["reports"].(map[string]interface{})
	if !ok {
		t.Fatalf("reports section missing")
	}
	if enabled, _ := reports["enabled"].(bool); enabled {
		t.Fatalf("existing explicit value should be preserved")
	}
	if _, ok := reports["morning"]; !ok {
		t.Fatalf("expected missing nested default field reports.morning")
	}
	if _, ok := cfgMap["network_watchdog"]; !ok {
		t.Fatalf("expected network_watchdog defaults to be added")
	}
}

func TestLoadConfig_AddsMissingFieldsToLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	legacy := map[string]interface{}{
		"bot_token":       "token",
		"allowed_user_id": 123,
		"reports": map[string]interface{}{
			"enabled": false,
		},
	}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	oldConfigFile := configFile
	configFile = path
	t.Cleanup(func() { configFile = oldConfigFile })

	loadConfig()

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var savedMap map[string]interface{}
	if err := json.Unmarshal(saved, &savedMap); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}

	if _, ok := savedMap["network_watchdog"]; !ok {
		t.Fatalf("expected missing top-level defaults to be persisted")
	}
	reports, _ := savedMap["reports"].(map[string]interface{})
	if enabled, _ := reports["enabled"].(bool); enabled {
		t.Fatalf("legacy explicit reports.enabled=false should be preserved")
	}
	if _, ok := reports["morning"]; !ok {
		t.Fatalf("expected nested missing defaults in reports section")
	}
}
