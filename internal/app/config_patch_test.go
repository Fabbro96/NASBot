package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestApplyConfigPatch_DeepMergePreservesNestedNotifications(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	originalConfigFile := configFile
	configFile = path
	defer func() {
		configFile = originalConfigFile
	}()

	initialConfig := Config{
		Notifications: NotificationsConfig{
			CPU:     ResourceConfig{Enabled: true, WarningThreshold: 80, CriticalThreshold: 90},
			RAM:     ResourceConfig{Enabled: true, WarningThreshold: 75, CriticalThreshold: 85},
			DiskSSD: ResourceConfig{Enabled: true, WarningThreshold: 82, CriticalThreshold: 92},
		},
	}
	initBytes, _ := json.MarshalIndent(initialConfig, "", "  ")
	if err := os.WriteFile(path, initBytes, 0600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	// Patch ONLY CPU notification threshold
	patch := map[string]interface{}{
		"notifications": map[string]interface{}{
			"cpu": map[string]interface{}{
				"warning_threshold":  85,
				"critical_threshold": 95,
			},
		},
	}

	_, err := applyConfigPatch(patch)
	if err != nil {
		t.Fatalf("apply config patch: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	// CPU was updated
	if saved.Notifications.CPU.WarningThreshold != 85 || saved.Notifications.CPU.CriticalThreshold != 95 {
		t.Fatalf("expected CPU thresholds 85/95, got %v/%v", saved.Notifications.CPU.WarningThreshold, saved.Notifications.CPU.CriticalThreshold)
	}
	// RAM and SSD were preserved and NOT overwritten by factory defaults!
	if saved.Notifications.RAM.WarningThreshold != 75 || saved.Notifications.RAM.CriticalThreshold != 85 {
		t.Fatalf("expected RAM thresholds 75/85 preserved, got %v/%v", saved.Notifications.RAM.WarningThreshold, saved.Notifications.RAM.CriticalThreshold)
	}
	if saved.Notifications.DiskSSD.WarningThreshold != 82 || saved.Notifications.DiskSSD.CriticalThreshold != 92 {
		t.Fatalf("expected SSD thresholds 82/92 preserved, got %v/%v", saved.Notifications.DiskSSD.WarningThreshold, saved.Notifications.DiskSSD.CriticalThreshold)
	}
}

// TestApplyConfigPatchConcurrentPatchesAreAllKept è la prova di configWriteMu.
//
// applyConfigPatch è una read-modify-write sul file: senza il lock, due patch
// partite insieme leggono entrambe lo stato precedente e l'ultima riscrittura
// cancella la chiave scritta dall'altra. Il guasto è un dato che sparisce da
// config.json senza alcun errore, ed è esattamente ciò che la sync periodica
// dei dispositivi SMART avrebbe messo in circolo (una patch ogni 10 minuti
// accanto a quelle dell'operatore).
//
// Gira sotto -race: la mutazione concorrente del file e della snapshot
// pubblicata è il motivo per cui il lock esiste.
func TestApplyConfigPatchConcurrentPatchesAreAllKept(t *testing.T) {
	path := writeCompleteConfig(t, nil)
	pinConfigPathAndSysfs(t, path, t.TempDir())

	// Un campo per routine, tutti dentro i clamp di sanitizeConfig: se una
	// patch venisse persa, il valore corrispondente mancherebbe alla fine.
	patches := []map[string]interface{}{
		{"reports": map[string]interface{}{"interval_days": 7}},
		{"intervals": map[string]interface{}{"stats_seconds": 11}},
		{"cache": map[string]interface{}{"docker_ttl_seconds": 22}},
		{"healthchecks": map[string]interface{}{"period_seconds": 33}},
		{"kernel_watchdog": map[string]interface{}{"check_interval_seconds": 44}},
		{"raid_watchdog": map[string]interface{}{"check_interval_seconds": 55}},
		{"network_watchdog": map[string]interface{}{"check_interval_seconds": 66}},
		{"docker": map[string]interface{}{"watchdog": map[string]interface{}{"timeout_minutes": 77}}},
	}

	var ready, done sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(patches))

	ready.Add(len(patches))
	for _, patch := range patches {
		done.Add(1)
		go func(patch map[string]interface{}) {
			defer done.Done()
			ready.Done()
			<-start
			if _, err := applyConfigPatch(patch); err != nil {
				errs <- err
			}
		}(patch)
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("applyConfigPatch: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved Config
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	// Ogni valore è diverso da quello di default del fixture: una patch persa
	// lascia il valore di default, non zero.
	want := map[string]int{
		"reports.interval_days":                   7,
		"intervals.stats_seconds":                 11,
		"cache.docker_ttl_seconds":                22,
		"healthchecks.period_seconds":             33,
		"kernel_watchdog.check_interval_seconds":  44,
		"raid_watchdog.check_interval_seconds":    55,
		"network_watchdog.check_interval_seconds": 66,
		"docker.watchdog.timeout_minutes":         77,
	}
	got := map[string]int{
		"reports.interval_days":                   saved.Reports.IntervalDays,
		"intervals.stats_seconds":                 saved.Intervals.StatsSeconds,
		"cache.docker_ttl_seconds":                saved.Cache.DockerTTLSeconds,
		"healthchecks.period_seconds":             saved.Healthchecks.PeriodSeconds,
		"kernel_watchdog.check_interval_seconds":  saved.KernelWatchdog.CheckIntervalSecs,
		"raid_watchdog.check_interval_seconds":    saved.RaidWatchdog.CheckIntervalSecs,
		"network_watchdog.check_interval_seconds": saved.NetworkWatchdog.CheckIntervalSecs,
		"docker.watchdog.timeout_minutes":         saved.Docker.Watchdog.TimeoutMinutes,
	}
	for field, expected := range want {
		if got[field] != expected {
			t.Errorf("%s = %d, want %d (a concurrent patch was lost)", field, got[field], expected)
		}
	}
}
