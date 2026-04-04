package app

import (
	"strings"
	"testing"
)

func TestPowerSourceFromReason(t *testing.T) {
	tests := []struct {
		reason string
		want   string
	}{
		{reason: "manual-force-command", want: "command"},
		{reason: "manual-force-button", want: "button"},
		{reason: "network-down-timeout", want: "watchdog"},
		{reason: "oom-loop-threshold", want: "watchdog"},
		{reason: "unknown", want: "system"},
	}

	for _, tt := range tests {
		if got := powerSourceFromReason(tt.reason); got != tt.want {
			t.Fatalf("powerSourceFromReason(%q) = %q, want %q", tt.reason, got, tt.want)
		}
	}
}

func TestAddPowerLifecycleEventAddsStructuredEntry(t *testing.T) {
	ctx := newTestAppContext()

	addPowerLifecycleEvent(ctx, "reboot", true, "command", "reboot", "manual-force-command")

	events := ctx.State.GetEvents()
	if len(events) == 0 {
		t.Fatalf("expected at least one event")
	}

	msg := events[len(events)-1].Message
	checks := []string{
		"Power event:",
		"action=reboot",
		"forced=yes",
		"source=command",
		"cmd=\"reboot\"",
		"reason=manual-force-command",
	}

	for _, c := range checks {
		if !strings.Contains(msg, c) {
			t.Fatalf("event message missing %q: %s", c, msg)
		}
	}
}
