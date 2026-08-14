package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestState_QuietHoursMidnightPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	ctx := newTestAppContext()
	ctx.Settings.QuietHours.Enabled = true
	ctx.Settings.QuietHours.Start = TimePoint{Hour: 0, Minute: 0}
	ctx.Settings.QuietHours.End = TimePoint{Hour: 7, Minute: 30}

	saveState(ctx)

	// Create fresh context and load state
	ctx2 := newTestAppContext()
	loadState(ctx2)

	if !ctx2.Settings.QuietHours.Enabled {
		t.Fatalf("expected quiet hours to be enabled after reload")
	}
	if ctx2.Settings.QuietHours.Start.Hour != 0 || ctx2.Settings.QuietHours.Start.Minute != 0 {
		t.Fatalf("expected start 00:00, got %02d:%02d", ctx2.Settings.QuietHours.Start.Hour, ctx2.Settings.QuietHours.Start.Minute)
	}
	if ctx2.Settings.QuietHours.End.Hour != 7 || ctx2.Settings.QuietHours.End.Minute != 30 {
		t.Fatalf("expected end 07:30, got %02d:%02d", ctx2.Settings.QuietHours.End.Hour, ctx2.Settings.QuietHours.End.Minute)
	}
}

func TestState_ReportDaysPersistence(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	ctx := newTestAppContext()
	ctx.Settings.ReportsEnabled = true
	ctx.Settings.ReportInterval = 1
	ctx.Settings.ReportTimes = []TimePoint{{Hour: 8, Minute: 0}}
	ctx.Settings.SetReportsDays([]int{1, 4}) // Monday, Thursday

	saveState(ctx)

	// Ensure file was written
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("expected state file to exist: %v", err)
	}

	// Create fresh context and load state
	ctx2 := newTestAppContext()
	loadState(ctx2)

	if !ctx2.Settings.ReportsEnabled {
		t.Fatalf("expected reports enabled")
	}
	days := ctx2.Settings.GetReportsDays()
	if len(days) != 2 || days[0] != 1 || days[1] != 4 {
		t.Fatalf("expected report days [1, 4], got %v", days)
	}
}
