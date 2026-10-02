package app

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recordingRunner captures every command a test asked for instead of executing
// it. It is what makes the OOM path testable at all: monitor_kernel.go reaches
// runCommand(ctx, "reboot") once oomLoopThreshold OOM kills fall inside the
// window, and /usr/bin/reboot exists on the machine running the suite.
type recordingRunner struct {
	mu     sync.Mutex
	runs   []string
	output map[string]string
	err    error
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{output: map[string]string{}}
}

func (r *recordingRunner) Exists(name string) bool { return true }

func (r *recordingRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	return r.Output(context.Background(), name, args...)
}

func (r *recordingRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, joinCommand(name, args))
	if out, ok := r.output[name]; ok {
		return []byte(out), nil
	}
	return nil, r.err
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	r.runs = append(r.runs, joinCommand(name, args))
	r.mu.Unlock()
	return r.err
}

// Runs returns the commands the test asked for, in order.
func (r *recordingRunner) Runs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.runs...)
}

// installRecorder swaps the package runner for the duration of the test.
//
// Every test in this file must call it before exercising a kernel line: the
// previous version of the OOM test installed nothing, so the reboot path had
// only its threshold between it and the real binary.
func installRecorder(t *testing.T) *recordingRunner {
	t.Helper()

	r := newRecordingRunner()
	t.Cleanup(setCommandRunner(r))
	return r
}

// waitForRun polls until the runner recorded at least one command.
func (r *recordingRunner) waitForRun(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(r.Runs()) > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return len(r.Runs()) > 0
}

func TestProcessKernelLinesNoDeadlockOnOOM(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Monitor.KwInitialized = true
	ctx.Monitor.KwLastSignatures = make(map[string]string)
	bot := &fakeBot{}
	runner := installRecorder(t)

	lines := []string{
		"[ 123.456] Out of memory: Killed process 123 (python3) total-vm:123456kB, anon-rss:1234kB",
	}

	done := make(chan struct{})
	go func() {
		processKernelLines(ctx, bot, lines)
		close(done)
	}()

	select {
	case <-done:
		if len(bot.sent) == 0 {
			t.Fatalf("expected OOM notification to be sent")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("processKernelLines appears to be deadlocked on OOM path")
	}

	// One OOM kill must not reboot a NAS. Only a loop of them may, and that is
	// what the threshold test below is for.
	if runs := runner.Runs(); len(runs) != 0 {
		t.Fatalf("a single OOM kill executed commands: %v", runs)
	}
	if got := len(ctx.Monitor.RecentOOMs); got != 1 {
		t.Errorf("expected the OOM to be recorded once, got %d entries", got)
	}
}

func TestProcessKernelLinesNoDeadlockOnNonOOM(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Monitor.KwInitialized = true
	ctx.Monitor.KwLastSignatures = make(map[string]string)
	bot := &fakeBot{}
	installRecorder(t)

	lines := []string{
		"[ 999.111] EXT4-fs error (device sda1): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0",
		"[ 999.222] Aborting journal on device sda1-8.",
		"[ 999.333] EXT4-fs (sda1): Remounting filesystem read-only",
	}

	done := make(chan struct{})
	go func() {
		processKernelLines(ctx, bot, lines)
		close(done)
	}()

	select {
	case <-done:
		if len(bot.sent) == 0 {
			t.Fatalf("expected kernel notification to be sent")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("processKernelLines appears to be deadlocked on non-OOM path")
	}
}

// TestHandleOOMLoopRebootsOnlyAtThreshold pins the threshold from both sides.
//
// The count is the difference between rebooting a NAS that survived a memory
// spike and rebooting one that is genuinely thrashing, so both sides are
// asserted: oomLoopThreshold-1 kills must do nothing, the n-th must run `reboot`
// and nothing else. A threshold lowered to 2, or a comparison changed to `>`, is
// invisible to a test that only checks the threshold itself.
func TestHandleOOMLoopRebootsOnlyAtThreshold(t *testing.T) {
	ctx := newTestAppContext()
	b := &fakeBot{}
	runner := installRecorder(t)

	below := oomLoopThreshold - 1
	for i := 0; i < below; i++ {
		handleOOMLoop(ctx, b)
	}
	// The reboot runs in its own goroutine; give it the same chance a real one
	// would have before declaring it did not happen.
	time.Sleep(100 * time.Millisecond)
	if runs := runner.Runs(); len(runs) != 0 {
		t.Fatalf("%d OOM kills are below the threshold of %d but executed %v",
			below, oomLoopThreshold, runs)
	}
	if len(b.sent) != 0 {
		t.Errorf("the user must not be warned about a reboot that was not triggered, got %d messages", len(b.sent))
	}

	handleOOMLoop(ctx, b) // the n-th kill

	if !runner.waitForRun(2 * time.Second) {
		t.Fatalf("reboot was not invoked after %d OOM kills", oomLoopThreshold)
	}
	runs := runner.Runs()
	if len(runs) != 1 || runs[0] != "reboot" {
		t.Fatalf("expected exactly one `reboot`, got %v", runs)
	}
	if len(b.sent) != 1 {
		t.Errorf("expected one warning message before the reboot, got %d", len(b.sent))
	}
}

// TestHandleOOMLoopResetsAfterReboot: the counter is cleared once it fires, so a
// single burst cannot reboot the host repeatedly. Without the reset, the reboot
// command is queued again on the very next kill.
func TestHandleOOMLoopResetsAfterReboot(t *testing.T) {
	ctx := newTestAppContext()
	b := &fakeBot{}
	runner := installRecorder(t)

	for i := 0; i < oomLoopThreshold; i++ {
		handleOOMLoop(ctx, b)
	}
	if !runner.waitForRun(2 * time.Second) {
		t.Fatalf("reboot was not invoked after %d OOM kills", oomLoopThreshold)
	}

	ctx.Monitor.Mu.Lock()
	remaining := len(ctx.Monitor.RecentOOMs)
	ctx.Monitor.Mu.Unlock()
	if remaining != 0 {
		t.Errorf("the OOM counter must be reset after a reboot, %d entries left", remaining)
	}

	// One more kill must not fire a second reboot on its own.
	handleOOMLoop(ctx, b)
	time.Sleep(100 * time.Millisecond)
	if runs := runner.Runs(); len(runs) != 1 {
		t.Fatalf("a single OOM kill after a reboot executed %v, want no second reboot", runs)
	}
}

// TestHandleOOMLoopIgnoresOldEvents is the time half of the threshold: only the
// kills inside oomLoopWindow count. Five OOM kills spread over a day are not an
// OOM loop, and rebooting on them would take the NAS down for a reason that is
// long gone.
func TestHandleOOMLoopIgnoresOldEvents(t *testing.T) {
	ctx := newTestAppContext()
	b := &fakeBot{}
	runner := installRecorder(t)

	// Seed the window with kills that are already too old to count, then add one
	// fresh kill. Pruned, the count is 1 and nothing happens; not pruned, it
	// reaches the threshold and the host reboots on the strength of yesterday.
	ctx.Monitor.Mu.Lock()
	for i := 0; i < oomLoopThreshold; i++ {
		ctx.Monitor.RecentOOMs = append(ctx.Monitor.RecentOOMs,
			time.Now().Add(-2*oomLoopWindow))
	}
	ctx.Monitor.Mu.Unlock()

	handleOOMLoop(ctx, b)

	time.Sleep(100 * time.Millisecond)
	if runs := runner.Runs(); len(runs) != 0 {
		t.Fatalf("stale OOM kills outside the %v window executed %v", oomLoopWindow, runs)
	}
	ctx.Monitor.Mu.Lock()
	remaining := len(ctx.Monitor.RecentOOMs)
	ctx.Monitor.Mu.Unlock()
	if remaining != 1 {
		t.Errorf("expected the stale kills to be pruned down to the one fresh event, got %d", remaining)
	}
}

// TestHandleOOMLoopPrunesOutOfOrderSamples: a clock that jumped backwards (NTP
// correction, a suspend) leaves timestamps in the future. They must be kept, not
// dropped as "old", otherwise a real loop is masked until they expire.
func TestHandleOOMLoopPrunesOutOfOrderSamples(t *testing.T) {
	ctx := newTestAppContext()
	b := &fakeBot{}
	runner := installRecorder(t)

	future := time.Now().Add(time.Hour)
	ctx.Monitor.Mu.Lock()
	ctx.Monitor.RecentOOMs = append(ctx.Monitor.RecentOOMs, future)
	ctx.Monitor.Mu.Unlock()

	handleOOMLoop(ctx, b)

	ctx.Monitor.Mu.Lock()
	remaining := len(ctx.Monitor.RecentOOMs)
	ctx.Monitor.Mu.Unlock()
	if remaining != 2 {
		t.Errorf("a future-dated kill must be kept, got %d entries", remaining)
	}
	if runs := runner.Runs(); len(runs) != 0 {
		t.Fatalf("a single kill executed %v", runs)
	}
}
