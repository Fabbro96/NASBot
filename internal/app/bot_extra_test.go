package app

import (
	"nasbot/pkg/model"
	"testing"
)

func TestParseUptime_Hours(t *testing.T) {
	if got := parseUptime("Up 2 hours"); got != "2 hours" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Seconds(t *testing.T) {
	if got := parseUptime("Up 12 seconds"); got != "12 seconds" {
		t.Errorf("failed")
	}
}

func TestParseUptime_JustUp(t *testing.T) {
	if got := parseUptime("Up"); got != "running" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Exited(t *testing.T) {
	if got := parseUptime("Exited (0) 2 hours ago"); got != "stopped" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Created(t *testing.T) {
	if got := parseUptime("Created"); got != "stopped" {
		t.Errorf("failed")
	}
}

func TestTruncate_Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("failed")
	}
}

func TestTruncate_Long(t *testing.T) {
	if got := truncate("hello world", 5); got != "hell~" {
		t.Errorf("failed: got %q", got)
	}
}

func TestTruncate_Empty(t *testing.T) {
	if got := truncate("", 5); got != "" {
		t.Errorf("failed")
	}
}

func TestCommandExists_Ls(t *testing.T) {
	if !commandExists("ls") {
		t.Errorf("failed")
	}
}

func TestCommandExists_NonExistent(t *testing.T) {
	if commandExists("nonexistent_123456") {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_NilCtx(t *testing.T) {
	devs := getSmartDevices(nil)
	if len(devs) != 2 || devs[0] != "sda" {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_EmptyCtx(t *testing.T) {
	ctx := &AppContext{Config: &Config{}}
	devs := getSmartDevices(ctx)
	if len(devs) != 2 || devs[0] != "sda" {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_CustomCtx(t *testing.T) {
	ctx := &AppContext{Config: &Config{Notifications: NotificationsConfig{SMART: SmartConfig{Devices: []string{"nvme0n1"}}}}}
	devs := getSmartDevices(ctx)
	if len(devs) != 1 || devs[0] != "nvme0n1" {
		t.Errorf("failed")
	}
}

func TestSafeSend_NilBot(t *testing.T) {
	// Should not panic
	safeSend(nil, nil)
}

func TestStateLoadSave_Integration(t *testing.T) {
	ctx := model.InitApp(nil)
	ctx.State.AddEvent("test", "test_alert_1")

	// Save
	saveState(ctx)

	// Load into new ctx
	newCtx := model.InitApp(nil)
	loadState(newCtx)

	events := newCtx.State.GetEvents()
	if len(events) != 1 || events[0].Message != "test_alert_1" {
		t.Errorf("expected alert history to be loaded")
	}
}
