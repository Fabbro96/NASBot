package app

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

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.Healthchecks = HealthchecksState{LastPingSuccess: true}
	ctx.Monitor.NetLastCheckTime = time.Now().Add(-30 * time.Second)
	ctx.Monitor.NetConsecutiveDegraded = 2
	ctx.Monitor.NetFailCount = 1
	ctx.Monitor.KwLastCheckTime = time.Now().Add(-45 * time.Second)
	ctx.Monitor.KwConsecutiveCheckErrors = 1
	ctx.Monitor.KwLastCheckError = "journalctl not found"
	ctx.Monitor.Mu.Unlock()

	out := getHealthchecksStats(ctx)
	for _, want := range []string{"*Watchdogs*", "Network:", "Kernel:", "Last error:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q in health stats, got: %s", want, out)
		}
	}
}

func TestGetHealthchecksPeriodRate(t *testing.T) {
	tests := []struct {
		name      string
		hc        HealthchecksState
		wantRate  float64
		wantTotal int
	}{
		{
			name: "normal period 100%",
			hc: HealthchecksState{
				TotalPings:           1100,
				SuccessfulPings:      1096,
				ReportBaseTotal:      1000,
				ReportBaseSuccessful: 996,
				LastPingSuccess:      true,
			},
			wantRate:  100.0,
			wantTotal: 100,
		},
		{
			name: "normal period with failure",
			hc: HealthchecksState{
				TotalPings:           1100,
				SuccessfulPings:      1094,
				ReportBaseTotal:      1000,
				ReportBaseSuccessful: 996,
				LastPingSuccess:      true, // recovering
			},
			wantRate:  98.0, // 98 successes out of 100
			wantTotal: 100,
		},
		{
			name: "no pings yet, but healthy",
			hc: HealthchecksState{
				TotalPings:           1000,
				SuccessfulPings:      996,
				ReportBaseTotal:      1000,
				ReportBaseSuccessful: 996,
				LastPingSuccess:      true,
			},
			wantRate:  100.0,
			wantTotal: 0,
		},
		{
			name: "no pings yet, failing",
			hc: HealthchecksState{
				TotalPings:           1000,
				SuccessfulPings:      996,
				ReportBaseTotal:      1000,
				ReportBaseSuccessful: 996,
				LastPingSuccess:      false,
			},
			wantRate:  0.0,
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate, total := getHealthchecksPeriodRate(tt.hc)
			if rate != tt.wantRate {
				t.Errorf("getHealthchecksPeriodRate() rate = %v, want %v", rate, tt.wantRate)
			}
			if total != tt.wantTotal {
				t.Errorf("getHealthchecksPeriodRate() total = %v, want %v", total, tt.wantTotal)
			}
		})
	}
}
