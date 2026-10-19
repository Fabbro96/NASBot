package app

import (
	"testing"
	"time"

	"nasbot/pkg/model"
)

// Regression: during quiet hours no alert is sent, so the alert cooldown never
// started and every 10s check added a "CPU temp high" event, filling the
// 100-event report history in a few minutes.
func TestHandleTemperature_QuietHoursRecordsOneEventPerCooldown(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Cfg().Temperature = TemperatureConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 85}
	enableQuietHoursNow(t, ctx)

	bot := &fakeBot{}
	for i := 0; i < 50; i++ {
		handleTemperature(ctx, bot, 75)
	}

	if len(bot.sent) != 0 {
		t.Fatalf("expected no Telegram alert during quiet hours, got %d", len(bot.sent))
	}
	if n := len(ctx.State.GetEvents()); n != 1 {
		t.Fatalf("expected 1 temperature event per cooldown, got %d", n)
	}
}

func TestHandleTemperature_SendsOneAlertPerCooldown(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Cfg().Temperature = TemperatureConfig{Enabled: true, WarningThreshold: 70, CriticalThreshold: 85}

	bot := &fakeBot{}
	for i := 0; i < 10; i++ {
		handleTemperature(ctx, bot, 90)
	}

	if len(bot.sent) != 1 {
		t.Fatalf("expected exactly 1 alert, got %d", len(bot.sent))
	}
	if n := len(ctx.State.GetEvents()); n != 1 {
		t.Fatalf("expected 1 critical event, got %d", n)
	}
}

// Regression: the monitor loop recorded every alert of every 30s tick, so a
// CPU or disk that stayed above its threshold flushed the report history.
func TestRecordMonitorEvents_OneEventPerKeyPerCooldown(t *testing.T) {
	ctx := newTestAppContext()

	for i := 0; i < 20; i++ {
		recordMonitorEvents(ctx, []MonitorAlert{
			{Level: "warning", Key: "cpu", Message: "CPU high"},
			{Level: "warning", Key: "disk:/mnt/a", Message: "`/mnt/a` 91%"},
			{Level: "warning", Key: "disk:/mnt/b", Message: "`/mnt/b` 92%"},
		})
	}

	events := ctx.State.GetEvents()
	if len(events) != 3 {
		t.Fatalf("expected 1 event per key (3), got %d", len(events))
	}
	for _, e := range events {
		if e.Message == "`/mnt/a` 91%" {
			t.Fatalf("expected Markdown backticks to be stripped, got %q", e.Message)
		}
	}
}

func TestRecordMonitorEvents_LevelsHaveSeparateCooldowns(t *testing.T) {
	ctx := newTestAppContext()

	recordMonitorEvents(ctx, []MonitorAlert{{Level: "warning", Key: "ram", Message: "RAM high"}})
	recordMonitorEvents(ctx, []MonitorAlert{{Level: "critical", Key: "ram", Message: "RAM critical"}})
	recordMonitorEvents(ctx, []MonitorAlert{{Level: "critical", Key: "ram", Message: "RAM critical"}})

	if n := len(ctx.State.GetEvents()); n != 2 {
		t.Fatalf("expected a warning and a critical event, got %d", n)
	}
}

// Regression: during quiet hours the 10-minute alert cooldown never started,
// so a critical container that stayed down added an event on every check.
func TestCheckCriticalContainers_QuietHoursOneEventWhileDown(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Monitor.LastCriticalContainerAlert = map[string]time.Time{}
	ctx.Cfg().CriticalContainers = []string{"missing-db"}
	enableQuietHoursNow(t, ctx)

	bot := &fakeBot{}
	for i := 0; i < 10; i++ {
		checkCriticalContainers(ctx, bot)
	}

	if len(bot.sent) != 0 {
		t.Fatalf("expected no Telegram alert during quiet hours, got %d", len(bot.sent))
	}
	if n := len(ctx.State.GetEvents()); n != 1 {
		t.Fatalf("expected 1 event for a container that stays down, got %d", n)
	}
}

// enableQuietHoursNow sets a quiet-hours window around the current time.
func enableQuietHoursNow(t *testing.T, ctx *AppContext) {
	t.Helper()
	now := time.Now().In(ctx.State.TimeLocation)
	start := now.Add(-time.Hour)
	end := now.Add(time.Hour)
	ctx.Settings.SetQuietHours(model.QuietSettings{
		Enabled: true,
		Start:   model.TimePoint{Hour: start.Hour(), Minute: start.Minute()},
		End:     model.TimePoint{Hour: end.Hour(), Minute: end.Minute()},
	})
	if !ctx.IsQuietHours() {
		t.Fatalf("test setup: expected quiet hours to be active")
	}
}
