package main

import (
	"strings"
	"testing"
	"time"
)

func TestGetHealthchecksStatsIncludesWatchdogs(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Settings.SetLanguage("en")
	ctx.Config.Healthchecks.Enabled = true
	ctx.Config.Healthchecks.PingURL = "https://hc-ping.com/test"

	ctx.Monitor.mu.Lock()
	ctx.Monitor.Healthchecks = HealthchecksState{LastPingSuccess: true}
	ctx.Monitor.NetLastCheckTime = time.Now().Add(-30 * time.Second)
	ctx.Monitor.NetConsecutiveDegraded = 2
	ctx.Monitor.NetFailCount = 1
	ctx.Monitor.KwLastCheckTime = time.Now().Add(-45 * time.Second)
	ctx.Monitor.KwConsecutiveCheckErrors = 1
	ctx.Monitor.KwLastCheckError = "journalctl not found"
	ctx.Monitor.mu.Unlock()

	out := getHealthchecksStats(ctx)
	for _, want := range []string{"*Watchdogs*", "Network:", "Kernel:", "Last error:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in health stats, got: %s", want, out)
		}
	}
}
