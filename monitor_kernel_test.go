package main

import (
	"testing"
	"time"
)

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
