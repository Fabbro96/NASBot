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
