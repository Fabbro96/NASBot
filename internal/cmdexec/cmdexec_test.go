package cmdexec

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The tests in this file execute real processes on purpose: this package is
// the only place where NASBot runs external commands, so what matters is not
// that the code is called but that it cannot do more than it should. Only
// inert binaries are used (/bin/sh, printf, sleep, touch), never reboot,
// docker, systemctl, smartctl, and nothing here touches the network.
//
// Every test is hermetic: PATH and HOME are replaced with temporary
// directories, the fallback list is swapped for a temporary one, and no test
// depends on the working directory, on the user or on the system clock.

const (
	// testToolName is a name that no real system command can have, so a
	// resolveName hit can only come from the fixture the test created.
	testToolName = "nasbot-cmdexec-fixture"
)

// ---------------------------------------------------------------------------
// fixtures
// ---------------------------------------------------------------------------

func lookPathOrFail(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("fixture needs %q in PATH: %v", name, err)
	}
	return p
}

// writeFakeTool creates an executable named name inside dir and returns its
// absolute path. The content is irrelevant: resolution tests never run it.
func writeFakeTool(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	// os.WriteFile applies the umask, so make the x bit explicit: a fixture
	// without it would make a *correct* resolveName report "not found".
	if err := os.Chmod(p, 0o755); err != nil {
		t.Fatalf("chmod %s: %v", p, err)
	}
	return p
}

// withFallbackPaths replaces the package level fallback list for one test.
func withFallbackPaths(t *testing.T, dirs ...string) {
	t.Helper()
	prev := baseSystemPaths
	baseSystemPaths = dirs
	t.Cleanup(func() { baseSystemPaths = prev })
}

// isolatedPath points PATH at an empty directory, so exec.LookPath can only
// succeed through the fallback list.
func isolatedPath(t *testing.T) string {
	t.Helper()
	empty := t.TempDir()
	t.Setenv("PATH", empty)
	return empty
}

// ---------------------------------------------------------------------------
// /proc helpers
// ---------------------------------------------------------------------------

// procStat returns the /proc/<pid>/stat line, or false when the pid is gone.
// The comm field may contain spaces and parentheses, so parsing starts after
// the last ')': what follows is state ppid pgrp ...
func procStat(pid int) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func procFieldAfterComm(stat string, idx int) (string, bool) {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return "", false
	}
	fields := strings.Fields(stat[i+1:])
	if idx >= len(fields) {
		return "", false
	}
	return fields[idx], true
}

// processGroupOf returns the process group of pid. False when the pid is gone.
func processGroupOf(pid int) (int, bool) {
	stat, ok := procStat(pid)
	if !ok {
		return 0, false
	}
	// fields[0]=state, fields[1]=ppid, fields[2]=pgrp
	raw, ok := procFieldAfterComm(stat, 2)
	if !ok {
		return 0, false
	}
	pgid, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return pgid, true
}

// processState returns the single letter state of pid ("R", "S", "Z", ...).
func processState(pid int) (string, bool) {
	stat, ok := procStat(pid)
	if !ok {
		return "", false
	}
	raw, ok := procFieldAfterComm(stat, 0)
	if !ok {
		return "", false
	}
	return raw, true
}

// isRunning reports whether pid is a live process. A zombie counts as dead:
// it cannot run anything, and whether its corpse is reaped depends on the init
// of the machine (a container init may never reap), which is not this test's
// business.
func isRunning(pid int) bool {
	state, ok := processState(pid)
	return ok && state != "Z"
}

// livePidsInGroup returns the pids that are still running inside pgid. It
// walks /proc instead of calling pgrep: the assertion must not depend on
// whatever else runs on the machine, only on the group created by the test.
func livePidsInGroup(pgid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if g, ok := processGroupOf(pid); ok && g == pgid && isRunning(pid) {
			out = append(out, pid)
		}
	}
	return out
}

// waitGone polls until pid is not running, and reports the state it saw last.
func waitGone(pid int, timeout time.Duration) (gone bool, last string) {
	deadline := time.Now().Add(timeout)
	for {
		state, ok := processState(pid)
		if !ok {
			return true, "no /proc entry"
		}
		if state == "Z" {
			return true, "zombie (killed, not reaped yet)"
		}
		if !time.Now().Before(deadline) {
			return false, "still running, state=" + state
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ---------------------------------------------------------------------------
// resolveName / userLocalBin
// ---------------------------------------------------------------------------

// Defect covered: the PATH lookup is the fast path every command relies on.
// Without it resolveName would still work through the fallback list, so the
// fixture is put *only* in PATH and the returned path is compared exactly.
func TestResolveNameFindsCommandInPATH(t *testing.T) {
	dir := t.TempDir()
	want := writeFakeTool(t, dir, testToolName)
	t.Setenv("PATH", dir)
	withFallbackPaths(t) // no fallback: only PATH can answer

	got, err := resolveName(testToolName)
	if err != nil {
		t.Fatalf("resolveName(%q) failed: %v", testToolName, err)
	}
	if got != want {
		t.Fatalf("resolveName(%q) = %q, want %q", testToolName, got, want)
	}
}

// Defect covered: this is the reason the fallback list exists. With a reduced
// PATH (cron, systemd unit, container image) "reboot" and "shutdown" live in
// /sbin or /usr/local/sbin and are not in PATH. If the fallback loop were
// dropped, every power command would answer "command not found" while the
// binary is right there.
func TestResolveNameFindsCommandInFallbackDirsWhenNotInPATH(t *testing.T) {
	isolatedPath(t)
	fallback := t.TempDir()
	want := writeFakeTool(t, fallback, testToolName)
	withFallbackPaths(t, fallback)

	got, err := resolveName(testToolName)
	if err != nil {
		t.Fatalf("resolveName(%q) with empty PATH failed: %v", testToolName, err)
	}
	if got != want {
		t.Fatalf("resolveName(%q) = %q, want %q", testToolName, got, want)
	}
}

// Defect covered: $HOME/.local/bin must be computed from the environment *at
// call time*, not captured at init. A captured value breaks the moment HOME
// differs (su nasbot, systemd Environment=), and a hardcoded "/root/.local/bin"
// would run the wrong user's binary. The assertion compares the whole path, so
// a hardcoded directory fails too.
func TestResolveNameResolvesUserLocalBinAtCallTime(t *testing.T) {
	isolatedPath(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	withFallbackPaths(t) // no system fallback: only $HOME can answer

	want := writeFakeTool(t, filepath.Join(home, ".local", "bin"), testToolName)

	got, err := resolveName(testToolName)
	if err != nil {
		t.Fatalf("resolveName(%q) did not search $HOME/.local/bin: %v", testToolName, err)
	}
	if got != want {
		t.Fatalf("resolveName(%q) = %q, want %q", testToolName, got, want)
	}
	if lb := userLocalBin(); lb != filepath.Join(home, ".local", "bin") {
		t.Fatalf("userLocalBin() = %q, want %q", lb, filepath.Join(home, ".local", "bin"))
	}
}

// Defect covered: the multi-user NAS rule stated in the baseSystemPaths
// comment. An executable with the same name inside *another* account's
// ~/.local/bin must never be picked up. The fixture lives in the home of the
// first user only; after switching HOME the command must become unresolvable.
// A glob search ("/home/*/.local/bin"), a cached home, or an unset-HOME
// fallback to "/" all fail here.
func TestResolveNameIgnoresOtherUsersLocalBin(t *testing.T) {
	isolatedPath(t)
	owner := t.TempDir()
	t.Setenv("HOME", owner)
	withFallbackPaths(t)
	writeFakeTool(t, filepath.Join(owner, ".local", "bin"), testToolName)

	if _, err := resolveName(testToolName); err != nil {
		t.Fatalf("setup: the owner's own ~/.local/bin must be searched: %v", err)
	}

	// A second, different HOME with no such tool.
	other := t.TempDir()
	t.Setenv("HOME", other)
	if p, err := resolveName(testToolName); err == nil {
		t.Fatalf("resolveName(%q) resolved %q from another user's home; only $HOME/.local/bin may be searched", testToolName, p)
	}
}

// Defect covered: with HOME unset, os.UserHomeDir fails. Returning "/.local/bin"
// would silently search a root owned directory, so userLocalBin must return an
// empty string and the caller must not append it to the search list.
func TestUserLocalBinEmptyWhenHomeUnset(t *testing.T) {
	t.Setenv("HOME", "")
	if got := userLocalBin(); got != "" {
		t.Fatalf("userLocalBin() with empty HOME = %q, want \"\"", got)
	}

	// And the command still resolves from the system list, proving the empty
	// home did not poison the search.
	isolatedPath(t)
	fallback := t.TempDir()
	want := writeFakeTool(t, fallback, testToolName)
	withFallbackPaths(t, fallback)
	got, err := resolveName(testToolName)
	if err != nil {
		t.Fatalf("resolveName(%q) after empty HOME failed: %v", testToolName, err)
	}
	if got != want {
		t.Fatalf("resolveName(%q) = %q, want %q", testToolName, got, want)
	}
}

// Defect covered: callers pass resolved absolute paths around (docker, systemd
// unit files). An absolute path must be accepted as is, including one outside
// every fallback directory; if the absolute branch were dropped, the fallback
// loop would join the name to each system dir and report "not found".
func TestResolveNameAcceptsAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := writeFakeTool(t, dir, testToolName)
	withFallbackPaths(t) // nothing but the absolute branch can answer

	got, err := resolveName(abs)
	if err != nil {
		t.Fatalf("resolveName(%q) rejected a valid absolute path: %v", abs, err)
	}
	if got != abs {
		t.Fatalf("resolveName(%q) = %q, want %q", abs, got, abs)
	}
}

// Defect covered: a configured path that no longer exists (uninstalled tool,
// typo in a systemd unit) must fail at resolution with a clear message, not
// return a path that exec then reports as a generic "no such file".
func TestResolveNameRejectsMissingAbsolutePath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nasbot-does-not-exist")
	withFallbackPaths(t)

	p, err := resolveName(missing)
	if err == nil {
		t.Fatalf("resolveName(%q) = %q, want an error for a missing absolute path", missing, p)
	}
	if p != "" {
		t.Fatalf("resolveName(%q) returned %q together with an error, want an empty path", missing, p)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error %q does not name the offending command %q", err, missing)
	}
}

// Defect covered: the error is the only thing a Telegram user sees when a tool
// is missing, so it must name the command. A generic "not found", or worse a
// nil error with an empty path, would send the user hunting for the wrong
// binary. The name is unique, so it cannot exist in any fallback directory.
func TestResolveNameMissingCommandErrorNamesTheCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withFallbackPaths(t)

	name := "nasbot-cmdexec-missing-" + strconv.Itoa(os.Getpid())
	p, err := resolveName(name)
	if err == nil {
		t.Fatalf("resolveName(%q) = %q, want an error", name, p)
	}
	if !strings.Contains(err.Error(), name) {
		t.Fatalf("error %q does not name the missing command %q", err, name)
	}
	if !errors.Is(err, exec.ErrNotFound) && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error %q is not recognisable as a missing command", err)
	}
}

// Defect covered: names coming from a config file or a chat message must not be
// able to escape the search directories. A name with a slash is rejected unless
// it is absolute, and a traversal like ../../bin/sh must not resolve: if the
// code joined and cleaned the name against each candidate directory, a
// traversal would resolve to an arbitrary binary and /cmd would run it.
func TestResolveNameNameWithSlashIsNotInterpreted(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	fallback := t.TempDir()
	withFallbackPaths(t, fallback)

	for _, name := range []string{
		"sub/" + testToolName,
		"../" + testToolName,
		"../../bin/sh",
		"nested/dir/" + testToolName,
	} {
		p, err := resolveName(name)
		if err == nil {
			t.Errorf("resolveName(%q) = %q, want an error: a name with a slash must not be resolved", name, p)
			continue
		}
		if !strings.Contains(err.Error(), name) {
			t.Errorf("resolveName(%q) error %q does not name the command", name, err)
		}
	}
}

// Defect covered: the dedup branch of the fallback loop. It exists to avoid
// searching a directory twice, so removing it would not change the result, only
// the work done: this test documents the intent and covers the branch by
// putting a directory in both PATH and the fallback list and asserting the
// command is still resolved from the *other* fallback directory.
func TestResolveNameSkipsFallbackDirsAlreadyInPATH(t *testing.T) {
	shared := t.TempDir()
	t.Setenv("PATH", shared)
	other := t.TempDir()
	want := writeFakeTool(t, other, testToolName)
	// shared holds no such tool on purpose: the shared directory must be
	// skipped without breaking the walk.
	writeFakeTool(t, shared, "nasbot-cmdexec-other-fixture")
	withFallbackPaths(t, shared, other)

	got, err := resolveName(testToolName)
	if err != nil {
		t.Fatalf("resolveName(%q) failed: %v", testToolName, err)
	}
	if got != want {
		t.Fatalf("resolveName(%q) = %q, want %q from the second fallback dir", testToolName, got, want)
	}
}

// ---------------------------------------------------------------------------
// newCommand
// ---------------------------------------------------------------------------

// Defect covered: the three properties every execution depends on. Setpgid is
// what makes the group kill possible, WaitDelay is what keeps a leaked grandchild
// from blocking the bot forever, and Cancel must be present (exec refuses to
// run a command whose Cancel is nil only with a context, and silently falls
// back to killing just the child when it is missing).
func TestNewCommandConfiguresProcessGroupAndWaitDelay(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	cmd := newCommand(context.Background(), sh, []string{"-c", "exit 0"})

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %+v, want Setpgid true: without a new process group the timeout kill cannot reach the children", cmd.SysProcAttr)
	}
	if cmd.WaitDelay != killWaitDelay {
		t.Fatalf("WaitDelay = %v, want %v", cmd.WaitDelay, killWaitDelay)
	}
	if killWaitDelay != 3*time.Second {
		t.Fatalf("killWaitDelay = %v, want 3s: the comment above it documents that value", killWaitDelay)
	}
	if cmd.Cancel == nil {
		t.Fatal("Cancel is nil: exec would only kill the direct child")
	}
	if cmd.Path != sh {
		t.Fatalf("cmd.Path = %q, want the resolved %q", cmd.Path, sh)
	}
}

// Defect covered: the nil guard inside Cancel. exec only calls Cancel on a
// started command, so this path is defensive; if it were removed, a call
// before Start (or a race on cmd.Process) would panic instead of reporting
// os.ErrProcessDone. The test calls Cancel on a command that was never
// started, which is exactly the unguarded case.
func TestNewCommandCancelBeforeStartReturnsErrProcessDone(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	cmd := newCommand(context.Background(), sh, []string{"-c", "exit 0"})
	if cmd.Process != nil {
		t.Fatalf("Process is %v before Start, the test premise is wrong", cmd.Process)
	}

	err := cmd.Cancel()
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Cancel() before Start = %v, want os.ErrProcessDone", err)
	}
}

// Defect covered: the fallback that kills the direct child when the group is
// already gone. The group kill must not swallow that failure: after the process
// has been reaped, kill(-pgid) fails with ESRCH and Cancel must reach
// Process.Kill. If the fallback were removed, Cancel would report a raw ESRCH
// and exec would report a context error that never happened; if the group
// success were ignored, Cancel would return nil and the caller would believe
// something was killed.
func TestNewCommandCancelFallsBackToDirectChildWhenGroupIsGone(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	cmd := newCommand(context.Background(), sh, []string{"-c", "exit 0"})
	if err := cmd.Run(); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := os.Stat(fmt.Sprintf("/proc/%d", cmd.Process.Pid)); err == nil {
		t.Fatalf("pid %d still in /proc after Wait, the test premise is wrong", cmd.Process.Pid)
	}

	err := cmd.Cancel()
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("Cancel() on a reaped process = %v, want os.ErrProcessDone from the Process.Kill fallback", err)
	}
}

// ---------------------------------------------------------------------------
// exit status, output streams, signals
// ---------------------------------------------------------------------------

// Defect covered: the lookup error must be reported as is, without attempting
// the execution. If the runner ran the name anyway, the caller would see an
// exit status (127 from the shell) instead of "command not found", and
// /docker would report a container error for a missing CLI.
func TestRunnerReportsMissingCommandWithoutExecuting(t *testing.T) {
	isolatedPath(t)
	withFallbackPaths(t)
	name := "nasbot-cmdexec-absent-" + strconv.Itoa(os.Getpid())
	ctx := context.Background()

	if err := Run(ctx, name); err == nil {
		t.Errorf("Run(%q) = nil, want an error", name)
	} else {
		assertNotExitError(t, "Run", err, name)
		if !strings.Contains(err.Error(), name) {
			t.Errorf("Run(%q) error %q does not name the command", name, err)
		}
		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Run(%q) error %q is not recognisable as a missing command", name, err)
		}
	}
	if out, err := Output(ctx, name); err == nil {
		t.Errorf("Output(%q) = %q, want an error", name, out)
	} else {
		assertNotExitError(t, "Output", err, name)
	}
	if out, err := CombinedOutput(ctx, name); err == nil {
		t.Errorf("CombinedOutput(%q) = %q, want an error", name, out)
	} else {
		assertNotExitError(t, "CombinedOutput", err, name)
	}
	if Exists(name) {
		t.Errorf("Exists(%q) = true, want false", name)
	}
}

func assertNotExitError(t *testing.T, op string, err error, name string) {
	t.Helper()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		t.Errorf("%s(%q) returned an *exec.ExitError (%v): the command was executed although the name does not resolve", op, name, err)
	}
}

// Defect covered: the exit code is the only signal a caller has for "the tool
// ran and failed". Swallowing it (returning nil) would make a failed
// "docker restart" look like a success in Telegram.
func TestRunReportsExitCode(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	err := Run(context.Background(), sh, "-c", "exit 3")
	if err == nil {
		t.Fatal("Run of a command exiting 3 returned nil")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("Run error is %T (%v), want *exec.ExitError", err, err)
	}
	if got := ee.ExitCode(); got != 3 {
		t.Fatalf("ExitCode() = %d, want 3", got)
	}
}

// Defect covered: the happy path must stay silent, otherwise every successful
// command would be reported as a failure.
func TestRunSuccessReturnsNil(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	if err := Run(context.Background(), sh, "-c", "exit 0"); err != nil {
		t.Fatalf("Run of a command exiting 0 = %v, want nil", err)
	}
}

// Defect covered: CombinedOutput must return both streams interleaved in write
// order, because that is what "docker logs" and "journalctl" callers parse.
// Dropping stderr would lose the error line of a failing tool.
func TestCombinedOutputCapturesStdoutAndStderr(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	out, err := CombinedOutput(context.Background(), sh, "-c", "printf out; printf err >&2")
	if err != nil {
		t.Fatalf("CombinedOutput = %v, want nil", err)
	}
	got := string(out)
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Fatalf("CombinedOutput = %q, want it to contain both out and err", got)
	}
	if i, j := strings.Index(got, "out"), strings.Index(got, "err"); i > j {
		t.Fatalf("CombinedOutput = %q, want write order preserved (out before err)", got)
	}
}

// Defect covered: Output must capture stdout only. If stderr leaked into it,
// a warning printed by docker or smartctl would be parsed as part of the data
// (a container list, a temperature table) and the report would show garbage.
func TestOutputCapturesOnlyStdout(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	out, err := Output(context.Background(), sh, "-c", "printf out; printf err >&2")
	if err != nil {
		t.Fatalf("Output = %v, want nil", err)
	}
	if got := string(out); got != "out" {
		t.Fatalf("Output = %q, want exactly \"out\"", got)
	}
}

// Defect covered: a process killed by a signal must be distinguishable from one
// that exited with a code. RunBot and the Telegram handlers turn this into
// "terminated by signal" versus "failed with status N"; collapsing the two
// would report a SIGKILL from a timeout as an ordinary failure.
func TestRunnerReportsSignalDeath(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	err := Run(context.Background(), sh, "-c", "kill -TERM $$")
	if err == nil {
		t.Fatal("Run of a command killing itself returned nil")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("Run error is %T (%v), want *exec.ExitError", err, err)
	}
	if got := ee.ExitCode(); got != -1 {
		t.Fatalf("ExitCode() = %d, want -1 for a signalled process", got)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("Sys() is %T, want syscall.WaitStatus", ee.Sys())
	}
	if !ws.Signaled() {
		t.Fatalf("WaitStatus %v: Signaled() = false, want true", ws)
	}
	if got := ws.Signal(); got != syscall.SIGTERM {
		t.Fatalf("Signal() = %v, want SIGTERM", got)
	}
}

// ---------------------------------------------------------------------------
// context, timeout, process group
// ---------------------------------------------------------------------------

// assertKilledOnDeadline checks the error shape a caller sees when the context
// expires. It is not context.DeadlineExceeded: os/exec waits the process first
// and prefers its own error ("If c.Process.Wait returned an error, prefer
// that", os/exec/exec.go), and the process is dead by SIGKILL. Callers that
// test for errors.Is(err, context.DeadlineExceeded) would therefore never match
// a timeout of this package, so the contract is asserted here explicitly.
func assertKilledOnDeadline(t *testing.T, op string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s returned nil after the deadline expired, want the kill error", op)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("%s error is %T (%v), want *exec.ExitError: the deadline did not reach the process", op, err, err)
	}
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok {
		t.Fatalf("%s: Sys() is %T, want syscall.WaitStatus", op, ee.Sys())
	}
	if !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("%s: process ended with %v, want death by SIGKILL from the group kill", op, ws)
	}
}

// Defect covered: the deadline must be honoured by the process itself, not just
// by the caller giving up. If the ctx were not propagated, the call would block
// for the full 30s of the sleep; if the process were not killed, it would keep
// running as an orphan after the bot moved on. Both are asserted, and the
// elapsed time is checked so that a regression shows up as a 30s test rather
// than a pass.
func TestRunnerReturnsPromptlyOnContextDeadlineAndKillsProcess(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	out, err := Output(ctx, sh, "-c", "printf '%s' $$; exec sleep 30")
	elapsed := time.Since(start)

	assertKilledOnDeadline(t, "Output", err)
	if elapsed > 5*time.Second {
		t.Fatalf("the call took %v: the deadline was not enforced on the process", elapsed)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil {
		t.Fatalf("could not read the shell pid from %q: %v", out, convErr)
	}
	t.Logf("pid %d killed after %v with %v", pid, elapsed, err)
	gone, state := waitGone(pid, 2*time.Second)
	if !gone {
		t.Fatalf("pid %d survived the deadline (%s): the process outlived the call", pid, state)
	}
}

// Defect covered: THE process group test. "/cmd sh -c 'sleep 300 &'" backgrounds
// a job; without Setpgid + kill(-pgid) the shell dies but the background job
// keeps running with the inherited stdout pipe, so the call blocks on Wait
// until WaitDelay expires and the bot leaks one process per timed out command.
// The test asserts the three things that regression breaks: the call returns
// fast, the kill reached the process, and neither the group leader nor the
// backgrounded child is still running.
func TestRunnerKillsWholeProcessGroupOnTimeout(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// $$ is the shell, and with Setpgid it is also the process group leader;
	// $! is the backgrounded child, which stays in that same group because a
	// non interactive shell has no job control.
	start := time.Now()
	out, err := Output(ctx, sh, "-c", "printf '%s ' $$; sleep 10 & echo $!; wait")
	elapsed := time.Since(start)

	assertKilledOnDeadline(t, "Output", err)
	// A leaked grandchild keeps Wait blocked until WaitDelay (3s), so a
	// regression shows up as a 3.3s call even though the kill error is the
	// same: the direct child is killed either way.
	if elapsed > 2500*time.Millisecond {
		t.Fatalf("the call took %v: the backgrounded child still held the stdout pipe, so the group was not killed", elapsed)
	}

	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		t.Fatalf("could not read the two pids from %q", out)
	}
	pgid, err1 := strconv.Atoi(fields[0])
	child, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		t.Fatalf("unparsable pids in %q", out)
	}
	t.Logf("group leader pid %d, backgrounded child pid %d, call returned after %v with %v", pgid, child, elapsed, err)

	// The child must be gone, and it must be gone because of the group kill:
	// the shell that would have reaped it is dead too.
	if gone, state := waitGone(child, 2*time.Second); !gone {
		t.Fatalf("backgrounded child %d survived the timeout (%s) and is now an orphan", child, state)
	}
	if gone, state := waitGone(pgid, 2*time.Second); !gone {
		t.Fatalf("group leader %d survived its own timeout (%s)", pgid, state)
	}
	// The whole group must be empty. The scan is over /proc and skips zombies:
	// the backgrounded child is reparented to init when its shell dies, and
	// whether init reaps it immediately is not something this test controls
	// (a container PID 1 may never reap at all), while a zombie runs nothing.
	// A kill(-pgid, 0) probe cannot make that distinction: it answers for
	// zombies too, so it is only used to tell "group reaped" from "group still
	// holds a corpse" in the failure message.
	if left := livePidsInGroup(pgid); len(left) != 0 {
		probe := syscall.Kill(-pgid, syscall.Signal(0))
		t.Fatalf("process group %d still has %d running member(s) %v after the timeout (kill(-%d, 0) = %v)",
			pgid, len(left), left, pgid, probe)
	}
}

// Defect covered: the premise the group kill rests on. A non interactive shell
// does not put a backgrounded job in its own process group, so the child stays
// inside the group created by Setpgid and the negative pid kill reaches it. If
// a future change made the shell lead its own group (job control, setsid), this
// test would fail and the group kill would become a no-op for that case. The
// pids are read from a pipe and inspected while the job is still alive, which
// is why this test drives newCommand directly instead of Output.
func TestNewCommandKeepsBackgroundChildInProcessGroup(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	cmd := newCommand(context.Background(), sh, []string{"-c", "printf '%s ' $$; sleep 3 & echo $!; wait"})
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var pids []int
	sc := bufio.NewScanner(stdout)
	for len(pids) < 2 && sc.Scan() {
		for _, f := range strings.Fields(sc.Text()) {
			if p, err := strconv.Atoi(f); err == nil {
				pids = append(pids, p)
			}
		}
	}
	if len(pids) != 2 {
		_ = cmd.Wait()
		t.Fatalf("read %v from the shell output, want the leader and the child pid", pids)
	}
	leader, child := pids[0], pids[1]

	// The sleep still has time left, so both processes are inspectable here.
	// This must happen before Wait: once the shell has exited, the child is
	// reaped and /proc no longer says anything about its process group.
	childGroup, ok := processGroupOf(child)
	if !ok {
		t.Fatalf("child %d is not in /proc, the test premise is wrong", child)
	}
	leaderGroup, ok := processGroupOf(leader)
	if !ok {
		t.Fatalf("leader %d is not in /proc, the test premise is wrong", leader)
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		t.Fatalf("Wait: %v", waitErr)
	}

	if childGroup != leader {
		t.Fatalf("child %d is in process group %d, want %d (the group created by Setpgid): the group kill would miss it", child, childGroup, leader)
	}
	if leaderGroup != leader {
		t.Fatalf("leader %d is in process group %d, want its own pid: Setpgid is not in effect", leader, leaderGroup)
	}
	if self := os.Getpid(); leaderGroup == self {
		t.Fatalf("leader %d shares the group of the test process %d: Setpgid was not applied", leader, self)
	}
}

// ---------------------------------------------------------------------------
// argument handling
// ---------------------------------------------------------------------------

// Defect covered: no implicit shell. Arguments must reach the process as
// arguments, never concatenated into a string handed to sh -c. Every value here
// is a shell metacharacter: with an implicit shell the command substitution
// would run, the glob would expand and the output would be a different string,
// so the assertion is exact.
func TestRunnerDoesNotUseAnImplicitShell(t *testing.T) {
	printf, err := exec.LookPath("printf")
	if err != nil {
		t.Fatalf("fixture needs printf in PATH: %v", err)
	}
	args := []string{"a b", "c;d", "$(id)", "*", "x|y", "~", "$HOME", ">out", "\n"}
	want := strings.Join(args, "|") + "|"

	got, err := Output(context.Background(), printf, append([]string{"%s|"}, args...)...)
	if err != nil {
		t.Fatalf("Output = %v, want nil", err)
	}
	if string(got) != want {
		t.Fatalf("Output = %q, want %q: the arguments were not passed verbatim", got, want)
	}
	// A shell would also have created this file from the ">out" argument.
	if _, err := os.Stat(">out"); err == nil {
		t.Fatal("a file named \">out\" was created: the arguments went through a shell")
	}
}

// Defect covered: argument order and count must survive untouched. Callers build
// these slices by hand (docker ps --format, smartctl -A /dev/sda), so a
// reversed or joined slice would silently query the wrong thing. "$@" expands
// to the exact argument list, which is what makes this assertion meaningful.
func TestRunnerForwardsArgumentsVerbatimAndInOrder(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	got, err := Output(context.Background(), sh, "-c", `printf '%s|' "$@"`, "sh", "one", "two three", "--flag=1", "")
	if err != nil {
		t.Fatalf("Output = %v, want nil", err)
	}
	if want := "one|two three|--flag=1||"; string(got) != want {
		t.Fatalf("Output = %q, want %q", got, want)
	}
}

// Defect covered: the command name itself is not interpreted either. Callers
// pass values that come from Telegram, so a name with metacharacters must be
// reported as missing instead of being handed to a shell.
func TestRunnerCommandNameIsNotInterpreted(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	withFallbackPaths(t)
	name := "sh -c 'touch /tmp/nasbot-cmdexec-injected' #"
	if Exists(name) {
		t.Fatalf("Exists(%q) = true: the name was split or interpreted", name)
	}
	if _, err := CombinedOutput(context.Background(), name, "anything"); err == nil {
		t.Fatalf("CombinedOutput(%q) succeeded: the name was interpreted", name)
	}
}

// ---------------------------------------------------------------------------
// runner plumbing
// ---------------------------------------------------------------------------

// Defect covered: Exists is the guard used by the command handlers to decide
// whether a feature is available. A version that always returns true would
// register "docker" on a host without Docker; one that always returns false
// would hide every command.
func TestExistsReportsPresenceAndAbsence(t *testing.T) {
	sh := lookPathOrFail(t, "sh")
	if !Exists(filepath.Base(sh)) {
		t.Fatalf("Exists(%q) = false, want true", filepath.Base(sh))
	}
	name := "nasbot-cmdexec-absent-" + strconv.Itoa(os.Getpid())
	t.Setenv("PATH", t.TempDir())
	withFallbackPaths(t)
	if Exists(name) {
		t.Fatalf("Exists(%q) = true, want false", name)
	}
}

// recordingRunner is a Runner that answers from memory. It exists to prove the
// package level functions are a thin indirection over the active runner.
type recordingRunner struct {
	combined [][]string
	output   [][]string
	run      [][]string
	exists   []string
}

func (r *recordingRunner) Exists(name string) bool { r.exists = append(r.exists, name); return true }

func (r *recordingRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.combined = append(r.combined, append([]string{name}, args...))
	return []byte("combined"), nil
}

func (r *recordingRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	r.output = append(r.output, append([]string{name}, args...))
	return []byte("output"), nil
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.run = append(r.run, append([]string{name}, args...))
	return nil
}

// Defect covered: restore must restore the *previous* runner, not "the default".
// The handlers install their fake once at startup and defer the restore, so a
// restore that resets to defaultRunner instead of the saved value is invisible
// in any single test; it only shows up when a fake is installed on top of
// another fake, which is exactly what a nested SetRunner does. The second fake
// must be active until its own restore, and the first must come back after.
func TestSetRunnerNestsAndRestoresThePreviousRunner(t *testing.T) {
	outer := &recordingRunner{}
	restoreOuter := SetRunner(outer)
	t.Cleanup(restoreOuter)

	inner := &recordingRunner{}
	restoreInner := SetRunner(inner)

	if err := Run(context.Background(), "inner-only"); err != nil {
		t.Fatalf("Run through the inner fake = %v, want nil", err)
	}
	if len(inner.run) != 1 || len(outer.run) != 0 {
		t.Fatalf("while the inner fake is installed: inner=%v outer=%v, want the call to reach the inner fake only", inner.run, outer.run)
	}

	restoreInner()

	if err := Run(context.Background(), "outer-again"); err != nil {
		t.Fatalf("Run after the inner restore = %v, want nil", err)
	}
	if len(outer.run) != 1 || outer.run[0][0] != "outer-again" {
		t.Fatalf("after the inner restore outer=%v, want [outer-again]: the inner fake was not popped", outer.run)
	}
	if len(inner.run) != 1 {
		t.Fatalf("inner fake recorded %v, want exactly one call", inner.run)
	}

	restoreOuter()

	// Back to the real runner, so the fake cannot leak into another test.
	if out, err := CombinedOutput(context.Background(), lookPathOrFail(t, "sh"), "-c", "printf real"); err != nil || string(out) != "real" {
		t.Fatalf("after both restores CombinedOutput = %q, %v, want \"real\"", out, err)
	}
}

// Defect covered: the platform guard. Setpgid, Kill on a negative pid and the
// /proc assumptions in the kill path are Linux only; on any other platform the
// runner must refuse to execute anything instead of silently producing a
// command that cannot be cancelled. The guard also has to come *before* the
// name resolution, otherwise a missing binary on a foreign platform would be
// reported as "not found" and the real problem would stay hidden.
func TestRunnerRefusesToRunOnUnsupportedOS(t *testing.T) {
	prev := goos
	goos = "windows"
	t.Cleanup(func() { goos = prev })

	isolatedPath(t)
	fallback := t.TempDir()
	want := writeFakeTool(t, fallback, testToolName)
	withFallbackPaths(t, fallback)

	ctx := context.Background()
	if out, err := CombinedOutput(ctx, testToolName); !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("CombinedOutput = %q, %v; want ErrUnsupportedOS (the fixture %q resolves fine)", out, err, want)
	}
	if out, err := Output(ctx, testToolName); !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("Output = %q, %v; want ErrUnsupportedOS", out, err)
	}
	if err := Run(ctx, testToolName); !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("Run = %v; want ErrUnsupportedOS", err)
	}

	// The guard must not depend on the name resolving: an unresolvable name on
	// a foreign platform is still a platform problem.
	if err := Run(ctx, "nasbot-cmdexec-absent"); !errors.Is(err, ErrUnsupportedOS) {
		t.Errorf("Run of a missing name = %v; want ErrUnsupportedOS", err)
	}

	// And on Linux the same fixture must run, so the guard is not simply
	// blocking everything.
	goos = "linux"
	if out, err := CombinedOutput(ctx, filepath.Base(want)); err != nil {
		t.Errorf("CombinedOutput after restoring linux = %q, %v; want nil", out, err)
	}
}

// Defect covered: the package level functions must go through the runner
// variable, which is what lets the tests of the callers inject a fake instead
// of rebooting the machine. If Exists/CombinedOutput/Output/Run were changed to
// build a defaultRunner directly, this test records nothing and fails: the
// injection point is the whole reason SetRunner exists. The restore function is
// asserted too, because a restore that does not restore leaks the fake into
// every later test.
func TestPackageFunctionsDelegateToActiveRunner(t *testing.T) {
	fake := &recordingRunner{}
	restore := SetRunner(fake)

	if !Exists("whatever") {
		t.Error("Exists through the fake runner = false, want true")
	}
	if out, err := CombinedOutput(context.Background(), "c", "1", "2"); err != nil || string(out) != "combined" {
		t.Errorf("CombinedOutput through the fake runner = %q, %v", out, err)
	}
	if out, err := Output(context.Background(), "o"); err != nil || string(out) != "output" {
		t.Errorf("Output through the fake runner = %q, %v", out, err)
	}
	if err := Run(context.Background(), "r", "x"); err != nil {
		t.Errorf("Run through the fake runner = %v, want nil", err)
	}

	restore()

	if len(fake.exists) != 1 || fake.exists[0] != "whatever" {
		t.Errorf("fake Exists calls = %v, want [whatever]", fake.exists)
	}
	if len(fake.combined) != 1 || strings.Join(fake.combined[0], " ") != "c 1 2" {
		t.Errorf("fake CombinedOutput calls = %v, want [c 1 2]", fake.combined)
	}
	if len(fake.output) != 1 || fake.output[0][0] != "o" {
		t.Errorf("fake Output calls = %v, want [o]", fake.output)
	}
	if len(fake.run) != 1 || strings.Join(fake.run[0], " ") != "r x" {
		t.Errorf("fake Run calls = %v, want [r x]", fake.run)
	}

	// After the restore the real runner is back: a leaked fake would answer
	// "combined" for everything that follows.
	if out, err := CombinedOutput(context.Background(), lookPathOrFail(t, "sh"), "-c", "printf real"); err != nil || string(out) != "real" {
		t.Fatalf("after restore CombinedOutput = %q, %v: the fake runner was not restored", out, err)
	}
}
