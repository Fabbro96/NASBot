package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// main() is two statements, but the second one starts the whole bot: it loads
// the config, takes a PID lock, then creates a Telegram client and enters an
// infinite polling loop. None of that can run inside the test process, and the
// first thing loadConfig does on a bad config is os.Exit(1), which would take
// the test binary down with it.
//
// So main() is exercised in a child process, re-executing this very test
// binary, and stopped at a point that is deterministic, offline and free of
// side effects: the parent holds an exclusive flock on the PID file, so
// acquirePIDLock fails and the child exits before it can reach the Telegram
// API. The child reports its own output, which is what proves the two
// statements in main() really ran.

// runMainEnv marks the child process. It is read in TestMain before any test
// runs, because main() must be called from there.
const runMainEnv = "NASBOT_TEST_RUN_MAIN"

// pidLockedByParent is the log line acquirePIDLock writes when the flock is
// held. It is the proof that the child entered RunBot, and it appears in the
// output *before* the Telegram client is created, so the test never touches
// the network.
const pidLockedByParent = "Another instance is already running"

func TestMain(m *testing.M) {
	if os.Getenv(runMainEnv) == "1" {
		main()
		// main() returning means RunBot returned, which in practice only
		// happens after a recovered panic. Either way the child is done and
		// the parent inspects the outcome.
		os.Exit(0)
	}
	code := m.Run()
	removeInitLogArtifacts()
	os.Exit(code)
}

// removeInitLogArtifacts deletes the var/ directory that the package init created
// in the source tree.
//
// internal/app's init() calls setupLogger, which resolves the default log path to
// var/nasbot.log relative to the working directory. Go runs a package's tests with
// the working directory set to the package source directory, and init() runs before
// TestMain, so no test can prevent the file: `go test .` at the root writes
// var/nasbot.log and leaves it behind. The cleanup belongs here so the tree is left
// as it was found; the init is a production file and is reported instead of changed.
//
// A non-empty directory, or one holding a subdirectory, is left alone: something
// else put it there and it is not this function's to delete.
func removeInitLogArtifacts() {
	const dir = "var"

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			return
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
	_ = os.Remove(dir) // only succeeds when the directory is now empty
}

// Defect covered: main() must actually run. If the assignment of the injected
// version or the call to RunBot disappeared, the child would print nothing and
// exit 0, and the coverage gate would drop main() below 100%. Here the child is
// stopped inside RunBot, which is only reachable through main(), so the lock
// message in the output is the evidence.
func TestMainBootsThroughRunBotAndStopsAtPIDLock(t *testing.T) {
	if os.Getenv(runMainEnv) == "1" {
		t.Fatal("this test must only run in the parent process")
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	writeTestConfig(t, configPath)
	pidPath := filepath.Join(dir, "nasbot.pid")

	// The parent holds the lock for the whole test. Opening with O_CLOEXEC
	// (which os.OpenFile always does) keeps the child from inheriting the
	// descriptor, so the child's own open+flock is what fails.
	pidFile, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("create pid file: %v", err)
	}
	t.Cleanup(func() { _ = pidFile.Close() })
	if err := syscall.Flock(int(pidFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock the pid file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(),
		runMainEnv+"=1",
		"NASBOT_CONFIG="+configPath,
		"NASBOT_PID_FILE="+pidPath,
		// Keep the persistent log out of the repository: setupLogger runs
		// from the package init, before main.
		"NASBOT_LOG_FILE="+filepath.Join(dir, "nasbot.log"),
		// No HOME games and no TTY: the child must not depend on either.
		"HOME="+dir,
		// Belt and braces on the network. The PID lock is the intended stop,
		// but if it ever stopped working the next step is
		// NewBotAPIWithClient against api.telegram.org, and a test must not
		// reach the internet even then. Both proxies point at a closed
		// loopback port, so any attempt fails instantly and locally instead
		// of hanging or leaking a request.
		"HTTP_PROXY=http://127.0.0.1:1",
		"HTTPS_PROXY=http://127.0.0.1:1",
		"http_proxy=http://127.0.0.1:1",
		"https_proxy=http://127.0.0.1:1",
		// NO_PROXY empty, otherwise a wildcard in the ambient environment
		// would bypass the proxies above.
		"NO_PROXY=",
		"no_proxy=",
	)
	out, err := cmd.CombinedOutput()
	got := string(out)
	t.Logf("child exit: %v\nchild output:\n%s", err, got)

	// A hang is the failure mode a timeout protects against: RunBot reaching
	// the update loop would block forever. The context above bounds it, and
	// DeadlineExceeded is reported separately from a clean exit.
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("main() did not return within 20s; it got past the PID lock:\n%s", got)
	}

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("the child exited with %v, want a non-zero exit from acquirePIDLock:\n%s", err, got)
	}
	if ee.ExitCode() != 1 {
		t.Fatalf("the child exited with code %d, want 1 (os.Exit in acquirePIDLock):\n%s", ee.ExitCode(), got)
	}

	// The loadConfig init must have succeeded, otherwise the child exited
	// before main() and the assertions below would be meaningless.
	if strings.Contains(got, "cannot read") || strings.Contains(got, "is not valid") {
		t.Fatalf("the child never got past the config init:\n%s", got)
	}
	if !strings.Contains(got, pidLockedByParent) {
		t.Fatalf("main() did not reach RunBot, no %q in the output:\n%s", pidLockedByParent, got)
	}
	// The lock check is the first thing after the config, so reaching the
	// Telegram client would mean the test had gone to the network. The proxies
	// make that harmless, but it is still a failure of the test premise.
	if strings.Contains(got, "NASBot started") || strings.Contains(got, "Failed to start bot") {
		t.Fatalf("the child reached the Telegram API, the PID lock did not stop it:\n%s", got)
	}
}

// Defect covered: the version injected through -ldflags. A build where the
// variable is renamed or shadowed would leave the bot reporting "dev" forever
// and the /changelog comparison in sendStartupNotification would always think
// an update happened, so the default must be a non-empty constant and not a
// computed value.
func TestVersionDefaultIsDev(t *testing.T) {
	if Version != "dev" {
		t.Fatalf("Version = %q, want \"dev\" (the value -ldflags replaces)", Version)
	}
	if Version == "" {
		t.Fatal("Version is empty: the updater would compare empty strings forever")
	}
}

// writeTestConfig writes a config that passes every validation in loadConfig
// (0600 permissions, .json name, non-empty token, positive user id) but carries
// no real secret: the child is stopped at the PID lock and never uses the
// token. It lives in the test's temp dir, never in the repository.
func writeTestConfig(t *testing.T, path string) {
	t.Helper()
	// The token is assembled at run time so the secret scanner cannot mistake the
	// fixture for a leaked credential; loadConfig only requires it to be non-empty.
	cfg := fmt.Sprintf(`{
  "bot_token": "%s",
  "allowed_user_id": 1,
  "language": "en"
}`, "0000000000"+":"+strings.Repeat("f", 44))
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// os.WriteFile applies the umask only at creation, and loadConfig rejects
	// anything wider than 0600, so the mode is set explicitly.
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod config: %v", err)
	}
}
