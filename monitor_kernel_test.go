package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type countingRunner struct {
	mu       sync.Mutex
	runCalls int
}

func (r *countingRunner) Exists(name string) bool { return true }

func (r *countingRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return nil, nil
}

func (r *countingRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return nil, nil
}

func (r *countingRunner) Run(ctx context.Context, name string, args ...string) error {
	r.mu.Lock()
	r.runCalls++
	r.mu.Unlock()
	return nil
}

func (r *countingRunner) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runCalls
}

func TestProcessKernelLinesNoDeadlockOnOOM(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Monitor.KwInitialized = true
	ctx.Monitor.KwLastSignatures = make(map[string]string)
	bot := &fakeBot{}

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
}

func TestProcessKernelLinesNoDeadlockOnNonOOM(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Monitor.KwInitialized = true
	ctx.Monitor.KwLastSignatures = make(map[string]string)
	bot := &fakeBot{}

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

func TestHandleOOMLoopTriggersRebootAtThreshold(t *testing.T) {
	ctx := newTestAppContext()
	b := &fakeBot{}

	runner := &countingRunner{}
	restore := setCommandRunner(runner)
	t.Cleanup(restore)

	for i := 0; i < oomLoopThreshold; i++ {
		handleOOMLoop(ctx, b)
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if runner.Calls() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if runner.Calls() == 0 {
		t.Fatalf("expected reboot command to be invoked after OOM threshold")
	}
}
