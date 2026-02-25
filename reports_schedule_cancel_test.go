package main

import (
	"context"
	"testing"
	"time"
)

func TestSleepWithContextCancelled(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()

	if ok := sleepWithContext(c, 2*time.Second); ok {
		t.Fatalf("sleepWithContext should return false when context is already canceled")
	}
}

func TestPeriodicReportStopsOnCancelledContext(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Config.Intervals.StatsSeconds = 1
	ctx.Settings.ReportMode = 1

	c, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		periodicReport(ctx, &fakeBot{}, c)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("periodicReport did not stop promptly on canceled context")
	}
}
