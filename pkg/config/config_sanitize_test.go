package config

import "testing"

func TestSanitizeConfig_DefaultsAndClamps(t *testing.T) {
	cfg := Config{
		Paths: PathsConfig{},
		Reports: ReportsConfig{
			Enabled:      true,
			IntervalDays: -5,
			Times:        []TimeConfig{{Hour: 25, Minute: 65}, {Hour: -1, Minute: 30}},
		},
		QuietHours: QuietHoursConfig{
			Enabled:     true,
			StartHour:   7,
			StartMinute: 0,
			EndHour:     7,
			EndMinute:   0,
		},
		Notifications: NotificationsConfig{
			CPU:    ResourceConfig{Enabled: true, WarningThreshold: 80, CriticalThreshold: 50},
			RAM:    ResourceConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 60},
			DiskIO: DiskIOConfig{Enabled: true, WarningThreshold: 150},
		},
		Temperature: TemperatureConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 60},
		Healthchecks: HealthchecksConfig{
			Enabled:       true,
			PingURL:       "",
			PeriodSeconds: 30,
			GraceSeconds:  10,
		},
		NetworkWatchdog: NetworkWatchdogConfig{
			CheckIntervalSecs:    5,
			FailureThreshold:     0,
			CooldownMins:         0,
			ForceRebootAfterMins: 0,
			DNSHost:              "",
			Targets:              nil,
		},
		FSWatchdog: FSWatchdogConfig{
			CheckIntervalMins: 0,
			WarningThreshold:  -1,
			CriticalThreshold: 200,
			DeepScanPaths:     nil,
			ExcludePatterns:   nil,
			TopNFiles:         0,
		},
	}

	sanitizeConfig(&cfg)

	if cfg.Paths.SSD != defaultPathSSD || cfg.Paths.HDD != defaultPathHDD {
		t.Fatalf("expected default paths, got ssd=%q hdd=%q", cfg.Paths.SSD, cfg.Paths.HDD)
	}
	if cfg.Reports.IntervalDays != 1 {
		t.Errorf("Expected Reports.IntervalDays to be clamped to 1, got %d", cfg.Reports.IntervalDays)
	}
	if len(cfg.Reports.Times) != 2 {
		t.Fatalf("Expected 2 Times, got %d", len(cfg.Reports.Times))
	}
	if cfg.Reports.Times[0].Hour != 23 {
		t.Errorf("Expected Reports.Times[0].Hour to be clamped to 23, got %d", cfg.Reports.Times[0].Hour)
	}
	if cfg.Reports.Times[0].Minute != 59 {
		t.Errorf("Expected Reports.Times[0].Minute to be clamped to 59, got %d", cfg.Reports.Times[0].Minute)
	}
	if cfg.Reports.Times[1].Hour != 0 {
		t.Errorf("Expected Reports.Times[1].Hour to be clamped to 0, got %d", cfg.Reports.Times[1].Hour)
	}
	if cfg.Reports.Times[1].Minute != 30 {
		t.Errorf("Expected Reports.Times[1].Minute to be kept at 30, got %d", cfg.Reports.Times[1].Minute)
	}
	if cfg.QuietHours.Enabled {
		t.Fatalf("expected quiet hours disabled when start=end")
	}
	if cfg.Notifications.CPU.CriticalThreshold != cfg.Notifications.CPU.WarningThreshold {
		t.Fatalf("cpu critical should match warning when lower")
	}
	if cfg.Notifications.RAM.CriticalThreshold != cfg.Notifications.RAM.WarningThreshold {
		t.Fatalf("ram critical should match warning when lower")
	}
	if cfg.Notifications.DiskIO.WarningThreshold != 100 {
		t.Fatalf("disk io warning should be clamped to 100, got %.1f", cfg.Notifications.DiskIO.WarningThreshold)
	}
	if cfg.Temperature.CriticalThreshold != cfg.Temperature.WarningThreshold {
		t.Fatalf("temperature critical should match warning when lower")
	}
	if cfg.Healthchecks.Enabled {
		t.Fatalf("healthchecks should be disabled when ping_url empty")
	}
	if cfg.Healthchecks.GraceSeconds != cfg.Healthchecks.PeriodSeconds {
		t.Fatalf("healthchecks grace should be >= period, got grace=%d period=%d", cfg.Healthchecks.GraceSeconds, cfg.Healthchecks.PeriodSeconds)
	}
	if len(cfg.NetworkWatchdog.Targets) == 0 {
		t.Fatalf("network watchdog targets should default when empty")
	}
	if cfg.NetworkWatchdog.DNSHost == "" {
		t.Fatalf("network watchdog dns_host should default when empty")
	}
	if cfg.NetworkWatchdog.ForceRebootAfterMins != 3 {
		t.Fatalf("network watchdog force reboot after should default to 3, got %d", cfg.NetworkWatchdog.ForceRebootAfterMins)
	}
	if len(cfg.FSWatchdog.DeepScanPaths) == 0 || cfg.FSWatchdog.DeepScanPaths[0] != "/" {
		t.Fatalf("fs watchdog deep_scan_paths should default to /")
	}
	if cfg.FSWatchdog.CheckIntervalMins < 1 || cfg.FSWatchdog.TopNFiles < 1 {
		t.Fatalf("fs watchdog fields should be clamped")
	}
}

func TestSanitizeConfig_NormalizesLists(t *testing.T) {
	cfg := Config{
		CriticalContainers: []string{"  plex  ", "", "plex", "db"},
		Notifications: NotificationsConfig{
			SMART: SmartConfig{Devices: []string{" sda ", "", "sda", "sdb"}},
		},
		FSWatchdog: FSWatchdogConfig{
			DeepScanPaths:     []string{"/", " / ", ""},
			ExcludePatterns:   []string{"/proc", "", "/proc"},
			TopNFiles:         10,
			CheckIntervalMins: 10,
			WarningThreshold:  80,
			CriticalThreshold: 90,
		},
		NetworkWatchdog: NetworkWatchdogConfig{
			Targets:              []string{" 9.9.9.9 ", "", "9.9.9.9", "1.1.1.1"},
			CheckIntervalSecs:    60,
			FailureThreshold:     3,
			CooldownMins:         10,
			ForceRebootAfterMins: 2000,
			DNSHost:              "quad9.net",
		},
	}

	sanitizeConfig(&cfg)

	if len(cfg.CriticalContainers) != 2 || cfg.CriticalContainers[0] != "plex" || cfg.CriticalContainers[1] != "db" {
		t.Fatalf("critical containers not normalized: %#v", cfg.CriticalContainers)
	}
	if len(cfg.Notifications.SMART.Devices) != 2 || cfg.Notifications.SMART.Devices[0] != "sda" || cfg.Notifications.SMART.Devices[1] != "sdb" {
		t.Fatalf("smart devices not normalized: %#v", cfg.Notifications.SMART.Devices)
	}
	if len(cfg.FSWatchdog.DeepScanPaths) != 1 || cfg.FSWatchdog.DeepScanPaths[0] != "/" {
		t.Fatalf("deep scan paths not normalized: %#v", cfg.FSWatchdog.DeepScanPaths)
	}
	if len(cfg.FSWatchdog.ExcludePatterns) != 1 || cfg.FSWatchdog.ExcludePatterns[0] != "/proc" {
		t.Fatalf("exclude patterns not normalized: %#v", cfg.FSWatchdog.ExcludePatterns)
	}
	if len(cfg.NetworkWatchdog.Targets) != 2 || cfg.NetworkWatchdog.Targets[0] != "9.9.9.9" || cfg.NetworkWatchdog.Targets[1] != "1.1.1.1" {
		t.Fatalf("network targets not normalized: %#v", cfg.NetworkWatchdog.Targets)
	}
	if cfg.NetworkWatchdog.ForceRebootAfterMins != 1440 {
		t.Fatalf("network force reboot minutes should be clamped to 1440, got %d", cfg.NetworkWatchdog.ForceRebootAfterMins)
	}
}

func TestNormalizeDay(t *testing.T) {
	day, changed := normalizeDay("Funday")
	if day != "sunday" || !changed {
		t.Fatalf("expected fallback to sunday with change, got day=%q changed=%v", day, changed)
	}

	day, changed = normalizeDay("  Monday ")
	if day != "monday" || !changed {
		t.Fatalf("expected monday normalized with change, got day=%q changed=%v", day, changed)
	}

	day, changed = normalizeDay("tuesday")
	if day != "tuesday" || changed {
		t.Fatalf("expected tuesday unchanged, got day=%q changed=%v", day, changed)
	}
}

func TestNormalizeStringList(t *testing.T) {
	items := []string{" a ", "", "a", "b", "b", " c"}
	result := normalizeStringList(items)
	if len(result) != 3 || result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Fatalf("unexpected normalized list: %#v", result)
	}
}
