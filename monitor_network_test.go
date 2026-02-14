package main

import (
	"testing"
	"time"
)

func TestNetworkForceRebootAfter(t *testing.T) {
	cfg := &Config{}
	if got := networkForceRebootAfter(cfg); got != 0 {
		t.Fatalf("expected disabled duration 0, got %v", got)
	}

	cfg.NetworkWatchdog.ForceRebootOnDown = true
	cfg.NetworkWatchdog.ForceRebootAfterMins = 3
	if got := networkForceRebootAfter(cfg); got != 3*time.Minute {
		t.Fatalf("expected 3m, got %v", got)
	}

	cfg.NetworkWatchdog.ForceRebootAfterMins = 0
	if got := networkForceRebootAfter(cfg); got != 3*time.Minute {
		t.Fatalf("expected fallback 3m, got %v", got)
	}
}
