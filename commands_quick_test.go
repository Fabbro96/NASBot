package main

import (
	"strings"
	"testing"
)

func TestGetQuickTextIncludesWatchdogSemaphores(t *testing.T) {
	ctx := newTestAppContext()

	ctx.Monitor.mu.Lock()
	ctx.Monitor.KwConsecutiveCheckErrors = 1
	ctx.Monitor.NetConsecutiveDegraded = 1
	ctx.Monitor.mu.Unlock()

	out := getQuickText(ctx)
	if !strings.Contains(out, "WD K🔴 N🟡") {
		t.Fatalf("expected watchdog semaphores in quick text, got: %s", out)
	}
}
