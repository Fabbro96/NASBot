package commands

import (
	"context"
	"errors"
	"testing"
)

// TestCommandRunnerHelpers exercises the bound path of the three wrappers, which
// is the only way to reach them without shelling out.
func TestCommandRunnerHelpers(t *testing.T) {
	var gotName string
	var gotArgs []string

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			gotName, gotArgs = name, args
			return []byte("ok"), nil
		},
		RunCommandStdout: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return []byte("out"), nil
		},
		RunCommand: func(ctx context.Context, name string, args ...string) error {
			return nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	out, err := runCommandOutput(context.Background(), "cmd", "arg")
	if err != nil || string(out) != "ok" {
		t.Fatalf("runCommandOutput = %q, %v", string(out), err)
	}
	if gotName != "cmd" || len(gotArgs) != 1 || gotArgs[0] != "arg" {
		t.Errorf("runCommandOutput forwarded name=%q args=%v, want cmd [arg]", gotName, gotArgs)
	}

	out, err = runCommandStdout(context.Background(), "cmd", "arg")
	if err != nil || string(out) != "out" {
		t.Fatalf("runCommandStdout = %q, %v", string(out), err)
	}
	if err := runCommand(context.Background(), "cmd", "arg"); err != nil {
		t.Fatalf("runCommand error: %v", err)
	}
}

// TestUnboundRunnerReportsErrDepUnbound is the regression guard for the silent
// no-op this file exists for.
//
// The wrappers used to report success (or an empty result) when their binding was
// missing, so /configset answered "updated" without writing anything, /top
// answered "no processes" and /logs answered "no logs": a wrong answer the user
// cannot tell from the right one. ErrDepUnbound is the only thing that turns
// those into a visible failure.
func TestUnboundRunnerReportsErrDepUnbound(t *testing.T) {
	BindRuntime(RuntimeDeps{})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	if _, err := runCommandOutput(context.Background(), "cmd"); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("runCommandOutput unbound: %v, want ErrDepUnbound", err)
	}
	if err := runCommand(context.Background(), "cmd"); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("runCommand unbound: %v, want ErrDepUnbound", err)
	}

	// runCommandStdout has no binding of its own in production; it must fall
	// back to runCommandOutput, and report the same error when that is unbound
	// too rather than an empty success.
	if _, err := runCommandStdout(context.Background(), "cmd"); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("runCommandStdout unbound: %v, want ErrDepUnbound", err)
	}
}

// TestRunCommandStdoutFallsBackToCombinedOutput pins that fallback: with only
// RunCommandOutput bound, runCommandStdout must still produce the payload. A
// version that dropped the fallback would answer /top and /logs with no data on
// any build where the stdout runner was left unbound.
func TestRunCommandStdoutFallsBackToCombinedOutput(t *testing.T) {
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("fallback"), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	out, err := runCommandStdout(context.Background(), "cmd")
	if err != nil || string(out) != "fallback" {
		t.Fatalf("runCommandStdout = %q, %v, want the RunCommandOutput payload", string(out), err)
	}
}

// TestUnboundDepAccessorsReportErrDepUnbound: every accessor that can silently
// answer "" is a place where a missing binding is indistinguishable from a real
// empty answer. They must all name the missing dependency.
func TestUnboundDepAccessorsReportErrDepUnbound(t *testing.T) {
	BindRuntime(RuntimeDeps{})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	ctx := newTestAppContext()

	if _, err := callGeminiWithFallback(ctx, "hi", nil); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("callGeminiWithFallback: %v", err)
	}
	if _, _, err := checkForUpdate(ctx); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("checkForUpdate: %v", err)
	}
	if _, err := fetchLatestRelease(ctx); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("fetchLatestRelease: %v", err)
	}
	if _, err := getConfigJSONSafe(); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("getConfigJSONSafe: %v", err)
	}
	if _, err := applyConfigPatch(nil); !errors.Is(err, ErrDepUnbound) {
		t.Errorf("applyConfigPatch: %v", err)
	}
}

// TestSilentUnboundAccessorsReturnZeroValues documents the other half, which is
// deliberate: the accessors whose zero value is the correct answer when the
// dependency is absent must not panic and must not invent data.
func TestSilentUnboundAccessorsReturnZeroValues(t *testing.T) {
	BindRuntime(RuntimeDeps{})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	ctx := newTestAppContext()

	if cpu, ram := getTrendSummary(ctx); cpu != "" || ram != "" {
		t.Errorf("getTrendSummary unbound = %q, %q, want empty", cpu, ram)
	}
	if got := getCachedContainerList(ctx); got != nil {
		t.Errorf("getCachedContainerList unbound = %+v, want nil", got)
	}
	if got := readCPUTemp(); got != 0 {
		t.Errorf("readCPUTemp unbound = %v, want 0", got)
	}
	if got := getSmartDevices(ctx); got != nil {
		t.Errorf("getSmartDevices unbound = %+v, want nil", got)
	}
	if got := getDockerStatsText(ctx); got != "" {
		t.Errorf("getDockerStatsText unbound = %q, want empty", got)
	}
	if got := getVersion(); got != "unknown" {
		t.Errorf("getVersion unbound = %q, want %q", got, "unknown")
	}
	if got := generateReport(ctx, false, nil); got != "" {
		t.Errorf("generateReport unbound = %q, want empty", got)
	}
	// readDiskSMART has a meaningful fallback: -1 is "no reading", which /temp
	// renders as N/A. Answering 0 would show a plausible-looking 0°C.
	temp, health := readDiskSMART("sda")
	if temp != -1 || health != "UNKNOWN" {
		t.Errorf("readDiskSMART unbound = %d, %q, want -1, UNKNOWN", temp, health)
	}
}

// TestBoundDepsAreDispatched proves the binding is actually consulted rather than
// shadowed by a package-level default: each wrapper must call its own field.
func TestBoundDepsAreDispatched(t *testing.T) {
	var called []string
	mark := func(name string) func(context.Context, string, ...string) ([]byte, error) {
		return func(context.Context, string, ...string) ([]byte, error) {
			called = append(called, name)
			return []byte(name), nil
		}
	}

	BindRuntime(RuntimeDeps{
		RunCommandOutput: mark("combined"),
		RunCommandStdout: mark("stdout"),
		GetTrendSummary: func(*AppContext) (string, string) {
			called = append(called, "trend")
			return "cpu", "ram"
		},
		GetCachedContainerList: func(*AppContext) []ContainerInfo {
			called = append(called, "containers")
			return []ContainerInfo{{Name: "x"}}
		},
		ReadCPUTemp: func() float64 {
			called = append(called, "temp")
			return 42
		},
		GetSmartDevices: func(*AppContext) []string {
			called = append(called, "devices")
			return []string{"sda"}
		},
		ReadDiskSMART: func(string) (int, string) {
			called = append(called, "smart")
			return 35, "OK"
		},
		Version: func() string {
			called = append(called, "version")
			return "v1.2.3"
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	ctx := newTestAppContext()

	if _, err := runCommandOutput(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := runCommandStdout(context.Background(), "c"); err != nil {
		t.Fatal(err)
	}
	getTrendSummary(ctx) //nolint:errcheck // only the dispatch is under test
	getCachedContainerList(ctx)
	readCPUTemp()
	getSmartDevices(ctx)
	readDiskSMART("sda")
	getVersion()

	want := []string{"combined", "stdout", "trend", "containers", "temp", "devices", "smart", "version"}
	if len(called) != len(want) {
		t.Fatalf("dispatched %v, want %v", called, want)
	}
	for i := range want {
		if called[i] != want[i] {
			t.Fatalf("dispatched %v, want %v", called, want)
		}
	}
}
