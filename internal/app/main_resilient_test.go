package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

// resilientProbe records every invocation goSafeResilient makes, so a test can
// tell a restart from a spin.
type resilientProbe struct {
	mu    sync.Mutex
	calls []time.Time
}

func (p *resilientProbe) record() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, time.Now())
	return len(p.calls)
}

func (p *resilientProbe) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

// waitForCalls blocks until the probe has been invoked at least n times, or the
// deadline passes. Returns false on timeout.
func (p *resilientProbe) waitForCalls(n int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p.count() >= n {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return p.count() >= n
}

// waitForQuiescence waits until the probe stops being invoked: the count is the
// same across two consecutive samples delay apart. Returns the final count.
func (p *resilientProbe) waitForQuiescence(samples int, delay, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		first := p.count()
		stable := true
		for i := 0; i < samples; i++ {
			time.Sleep(delay)
			if p.count() != first {
				stable = false
				break
			}
		}
		if stable {
			return first
		}
		if time.Now().After(deadline) {
			return p.count()
		}
	}
}

// startResilient runs goSafeResilient and guarantees the goroutine is gone by
// the time the test returns.
//
// The old version of this test used a 5 ms restart delay and simply returned
// after seeing the second invocation: the goroutine kept running for the rest of
// the process, incrementing a shared counter every 5 ms while other tests ran.
// Cancelling and waiting for quiescence is what makes the counter meaningless to
// the rest of the suite.
func startResilient(t *testing.T, name string, delay time.Duration, fn func(int)) *resilientProbe {
	t.Helper()

	runCtx, cancel := context.WithCancel(context.Background())
	probe := &resilientProbe{}
	goSafeResilient(name, runCtx, delay, func() { fn(probe.record()) })

	t.Cleanup(func() {
		cancel()
		probe.waitForQuiescence(3, 2*time.Millisecond, 2*time.Second)
	})
	return probe
}

// TestGoSafeResilientRestartsAfterPanic proves the restart is caused by the
// panic: the first invocation is observed panicking, and only then is the second
// one waited for. A loop that ignored panics entirely, or that called fn twice
// without any panic in between, cannot satisfy the ordering.
//
// Defect covered: without the recover in goSafeResilient, the panic escapes the
// goroutine and takes the whole bot process down, so one bad monitor read kills
// a NAS full of services.
func TestGoSafeResilientRestartsAfterPanic(t *testing.T) {
	panicked := make(chan struct{})
	restarted := make(chan struct{})

	probe := startResilient(t, "test-resilient", time.Millisecond, func(n int) {
		switch n {
		case 1:
			close(panicked)
			panic("boom")
		case 2:
			close(restarted)
		}
	})

	select {
	case <-panicked:
	case <-time.After(2 * time.Second):
		t.Fatalf("the first invocation never ran, %d calls", probe.count())
	}

	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("no restart after the panic, %d calls", probe.count())
	}

	if probe.count() < 2 {
		t.Fatalf("the restart did not reach fn again, %d calls", probe.count())
	}
}

// TestGoSafeResilientKeepsRestartingWithoutPanic is the other half of the
// contract, and the reason the loop exists at all: a monitor that returns early
// (its context was cancelled, its ticker ended) must be started again rather than
// silently disappearing, because nothing else would notice a monitor that is no
// longer watching.
func TestGoSafeResilientKeepsRestartingWithoutPanic(t *testing.T) {
	probe := startResilient(t, "test-resilient", time.Millisecond, func(int) {})

	if !probe.waitForCalls(3, 2*time.Second) {
		t.Fatalf("a returning fn must be restarted, got %d calls", probe.count())
	}
}

// TestGoSafeResilientHonoursRestartDelay is the guard against a tight loop.
//
// If the restart delay were dropped, a monitor that keeps panicking would be
// re-run thousands of times per second: the CPU would be pinned by the restart
// itself and the log file would grow faster than the disk. A restart delay of
// 250 ms means that, in the 200 ms after the second invocation, no third one may
// happen; a spin produces hundreds.
func TestGoSafeResilientHonoursRestartDelay(t *testing.T) {
	const delay = 250 * time.Millisecond

	probe := startResilient(t, "test-slow-restart", delay, func(int) {})

	if !probe.waitForCalls(2, 3*time.Second) {
		t.Fatalf("expected the first restart after %v, got %d calls", delay, probe.count())
	}

	before := probe.count()
	time.Sleep(200 * time.Millisecond) // shorter than delay, by construction
	if after := probe.count(); after != before {
		t.Errorf("fn ran %d more times within a window shorter than the %v restart delay: "+
			"the delay is not being honoured", after-before, delay)
	}
}

// TestGoSafeResilientStopsOnContextCancel pins the exit condition.
//
// Without it, cancelling the shutdown context would leave every monitor running
// forever: the bot would refuse to exit cleanly, and each restart would keep
// re-reading hardware and re-sending Telegram messages after shutdown started.
func TestGoSafeResilientStopsOnContextCancel(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	calls := 0

	goSafeResilient("test-cancel", runCtx, time.Millisecond, func() {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}

	deadline := time.Now().Add(2 * time.Second)
	for count() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if count() == 0 {
		t.Fatal("fn was never invoked")
	}

	cancel()

	// One further invocation may legitimately be in flight: the loop checks the
	// context *after* fn returns and *after* the sleep, so a cancel that lands in
	// the gap lets the next iteration start. What must not happen is for the count
	// to keep growing, so the assertion is quiescence, not an exact value.
	//
	// Two samples 50 ms apart, repeated until they agree, catch a loop that
	// ignores the cancellation: the restart delay is 1 ms, so a leak adds
	// thousands of calls in a single window.
	deadline = time.Now().Add(5 * time.Second)
	for {
		first := count()
		time.Sleep(50 * time.Millisecond)
		if count() == first {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the loop kept running after the context was cancelled: %d calls became %d",
				first, count())
		}
	}
}

// TestGoSafeResilientRunsFnOnceWithNoPanic is the floor: a well-behaved fn must
// be invoked, and the goroutine must not exit before the cancellation. A
// goSafeResilient that returned early (say, because a recover() in the wrong
// scope made every call look like a panic) would pass every test above that only
// counts calls, and fail this one.
func TestGoSafeResilientRunsFnOnceWithNoPanic(t *testing.T) {
	probe := startResilient(t, "test-plain", time.Hour, func(int) {})

	if !probe.waitForCalls(1, 2*time.Second) {
		t.Fatalf("fn was never invoked, %d calls", probe.count())
	}
	// The delay is an hour: a second call can only come from a loop that ignores
	// the delay, so exactly one call proves the goroutine is waiting, not spinning.
	time.Sleep(50 * time.Millisecond)
	if got := probe.count(); got != 1 {
		t.Errorf("fn ran %d times with a one hour restart delay, want 1", got)
	}
}
