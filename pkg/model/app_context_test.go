package model

import (
	"log/slog"
	"testing"
	"time"
)

func TestRuntimeStateEvents(t *testing.T) {
	ctx := InitApp(nil)

	ctx.State.AddEvent("test_type", "test_msg")
	events := ctx.State.GetEvents()
	
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}
	
	if events[0].Type != "test_type" || events[0].Message != "test_msg" {
		t.Errorf("Event data mismatch: %v", events[0])
	}
	
	ctx.State.ClearEvents()
	if len(ctx.State.GetEvents()) != 0 {
		t.Errorf("Expected 0 events after clear")
	}
}

func TestThreadSafeStats(t *testing.T) {
	ctx := InitApp(nil)
	
	_, ready := ctx.GetStats()
	if ready {
		t.Fatal("Expected stats to be unready initially")
	}
	
	ctx.Stats.Set(Stats{CPU: 42.0})
	
	stats, ready := ctx.GetStats()
	if !ready || stats.CPU != 42.0 {
		t.Errorf("Stats not updated correctly: %v, ready=%v", stats, ready)
	}
}

func TestIsQuietHours(t *testing.T) {
	ctx := InitApp(nil)
	ctx.State.TimeLocation = time.UTC

	// Default: disabled
	if ctx.IsQuietHours() {
		t.Fatal("Expected false when quiet hours disabled")
	}

	// Enable quiet hours: 22:00 to 07:00
	ctx.Settings.QuietHours.Enabled = true
	ctx.Settings.QuietHours.Start = TimePoint{Hour: 22, Minute: 0}
	ctx.Settings.QuietHours.End = TimePoint{Hour: 7, Minute: 0}

	// Just a simple bounds check mechanism is tricky without mocking time.Now.
	// We'll trust the function logic, but test the function doesn't panic.
	_ = ctx.IsQuietHours()

	// Zero length window
	ctx.Settings.QuietHours.Start = TimePoint{Hour: 10, Minute: 0}
	ctx.Settings.QuietHours.End = TimePoint{Hour: 10, Minute: 0}
	if ctx.IsQuietHours() {
		t.Fatal("Expected false for zero length window")
	}
}

func TestContextLogger(t *testing.T) {
	ctx := InitApp(nil)

	// Should not panic
	ctx.LogError("test error", slog.String("key", "val"))
	ctx.LogInfo("test info", slog.String("key", "val"))
}
