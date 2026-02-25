package main

import (
	"sync"
	"testing"
	"time"
)

func runWithTimeout(t *testing.T, timeout time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatalf("operation timed out (possible deadlock)")
	}
}

func TestNoDeadlock_RuntimeStateConcurrentAccess(t *testing.T) {
	ctx := newTestAppContext()

	runWithTimeout(t, 2*time.Second, func() {
		var wg sync.WaitGroup
		for i := 0; i < 30; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ctx.State.AddEvent("info", "evt")
				_ = ctx.State.GetEvents()
				if i%5 == 0 {
					ctx.State.ClearEvents()
				}
			}(i)
		}
		wg.Wait()
	})
}

func TestNoDeadlock_BotContextConcurrentAccess(t *testing.T) {
	ctx := newTestAppContext()

	runWithTimeout(t, 2*time.Second, func() {
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ctx.Bot.SetPendingAction("x")
				_ = ctx.Bot.GetPendingAction()
				if i%3 == 0 {
					ctx.Bot.ClearPendingAction()
				}
			}(i)
		}
		wg.Wait()
	})
}

func TestNoDeadlock_HealthStatsWhileMonitorMutates(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Config.Healthchecks.Enabled = true
	ctx.Config.Healthchecks.PingURL = "https://hc-ping.com/test"
	ctx.Settings.SetLanguage("en")

	runWithTimeout(t, 2*time.Second, func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(2)
			go func(i int) {
				defer wg.Done()
				ctx.Monitor.mu.Lock()
				ctx.Monitor.NetConsecutiveDegraded = i % 3
				ctx.Monitor.NetFailCount = i % 2
				ctx.Monitor.KwConsecutiveCheckErrors = i % 4
				ctx.Monitor.KwLastCheckError = "e"
				ctx.Monitor.Healthchecks.LastPingSuccess = i%2 == 0
				ctx.Monitor.mu.Unlock()
			}(i)
			go func() {
				defer wg.Done()
				_ = getHealthchecksStats(ctx)
			}()
		}
		wg.Wait()
	})
}
