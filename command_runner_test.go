package main

import (
	"context"
	"testing"
)

type mockRunner struct {
	exists bool
	out    []byte
	err    error
}

func (m mockRunner) Exists(name string) bool { return m.exists }

func (m mockRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return m.out, m.err
}

func (m mockRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return m.out, m.err
}

func (m mockRunner) Run(ctx context.Context, name string, args ...string) error {
	return m.err
}

func TestCommandRunnerHelpers(t *testing.T) {
	restore := setCommandRunner(mockRunner{exists: true, out: []byte("ok"), err: nil})
	t.Cleanup(restore)

	if !commandExists("anything") {
		t.Fatalf("commandExists expected true")
	}
	out, err := runCommandOutput(context.Background(), "cmd", "arg")
	if err != nil || string(out) != "ok" {
		t.Fatalf("runCommandOutput = %q, %v", string(out), err)
	}
	out, err = runCommandStdout(context.Background(), "cmd", "arg")
	if err != nil || string(out) != "ok" {
		t.Fatalf("runCommandStdout = %q, %v", string(out), err)
	}
	if err := runCommand(context.Background(), "cmd", "arg"); err != nil {
		t.Fatalf("runCommand error: %v", err)
	}
}
