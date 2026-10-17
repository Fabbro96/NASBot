package cmdexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// killWaitDelay bounds how long Wait blocks after the context expired and
// the process group was killed. Without it, a grandchild that inherited the
// stdout pipe (e.g. "/cmd sleep 300 &") keeps Wait blocked until it exits.
const killWaitDelay = 3 * time.Second

// baseSystemPaths are extra directories searched when exec.LookPath fails.
// This ensures commands like "reboot" or "shutdown" are found even when
// /sbin and /usr/sbin are absent from the process PATH (e.g. under cron).
// No user home directory belongs here: on a multi-user NAS an executable
// with the same name in another account's ~/.local/bin must never be picked
// up. The running user's own ~/.local/bin is resolved at call time instead
// (see userLocalBin).
var baseSystemPaths = []string{
	"/usr/local/bin",
	"/usr/sbin",
	"/sbin",
	"/usr/bin",
	"/bin",
}

// userLocalBin returns $HOME/.local/bin, or "" when it cannot be resolved.
func userLocalBin() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "bin")
}

// resolveName returns the full path of name, searching the current PATH
// first, then the fallback systemPaths.
func resolveName(name string) (string, error) {
	// Already an absolute path — just verify it exists and is executable.
	if filepath.IsAbs(name) {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("command %s not found", name)
	}

	// Standard PATH lookup.
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}

	// Avoid duplicate searching of directories already in PATH.
	pathDirs := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	inPath := make(map[string]bool, len(pathDirs))
	for _, d := range pathDirs {
		inPath[d] = true
	}

	dirs := baseSystemPaths
	if homeBin := userLocalBin(); homeBin != "" {
		dirs = append(append([]string(nil), dirs...), homeBin)
	}

	for _, dir := range dirs {
		if inPath[dir] {
			continue
		}
		candidate := filepath.Join(dir, name)
		if p, err := exec.LookPath(candidate); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("command %s not found", name)
}

var ErrUnsupportedOS = errors.New("unsupported OS")

// goos is runtime.GOOS behind a variable. The execution machinery below
// (Setpgid, Kill on a negative pid) is Linux specific, and a constant GOOS
// makes the platform guard unreachable from a test running on Linux, so the
// guard would never be exercised at all.
var goos = runtime.GOOS

// newCommand builds an exec.Cmd that runs resolved in its own process group.
// exec.CommandContext kills only the direct child, so a shell command that
// backgrounds a job ("/cmd sleep 300 &") would survive the context deadline:
// the child of the shell dies, the background process keeps running with the
// inherited stdout pipe, and Wait blocks on it. Running the command in its own
// group (Setpgid) and cancelling the whole group fixes both.
func newCommand(ctx context.Context, resolved string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, resolved, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		// A negative PID targets the whole process group created by Setpgid.
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		// Fall back to the direct child if the group is already gone.
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = killWaitDelay
	return cmd
}

// Runner abstracts external command execution.
type Runner interface {
	Exists(name string) bool
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
	Run(ctx context.Context, name string, args ...string) error
}

type defaultRunner struct{}

func (defaultRunner) Exists(name string) bool {
	_, err := resolveName(name)
	return err == nil
}

func (defaultRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if goos != "linux" {
		return nil, ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return nil, err
	}
	cmd := newCommand(ctx, resolved, args)
	buf := &limitedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf
	err = cmd.Run()
	return buf.Bytes(), err
}

func (defaultRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if goos != "linux" {
		return nil, ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return nil, err
	}
	cmd := newCommand(ctx, resolved, args)
	out := &limitedBuffer{limit: maxCapturedOutput}
	errBuf := &limitedBuffer{limit: maxCapturedOutput}
	cmd.Stdout = out
	cmd.Stderr = errBuf
	err = cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		ee.Stderr = errBuf.Bytes()
	}
	return out.Bytes(), err
}

func (defaultRunner) Run(ctx context.Context, name string, args ...string) error {
	if goos != "linux" {
		return ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return err
	}
	return newCommand(ctx, resolved, args).Run()
}

var runner Runner = defaultRunner{}

// SetRunner swaps the active runner. Returns a restore func.
func SetRunner(r Runner) (restore func()) {
	prev := runner
	runner = r
	return func() { runner = prev }
}

func Exists(name string) bool {
	return runner.Exists(name)
}

func CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runner.CombinedOutput(ctx, name, args...)
}

func Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runner.Output(ctx, name, args...)
}

func Run(ctx context.Context, name string, args ...string) error {
	return runner.Run(ctx, name, args...)
}

// escapeBinaries are executables that can run arbitrary code or other programs
// from their arguments, so allowlisting one of them makes the /cmd allowlist
// equivalent to a full shell (`sh -c`, `find -exec`, `docker run -v /:/h`, ...).
var escapeBinaries = map[string]bool{
	"sh": true, "bash": true, "dash": true, "zsh": true, "ash": true, "ksh": true, "busybox": true,
	"env": true, "xargs": true, "find": true, "awk": true, "gawk": true, "sed": true, "tee": true,
	"python": true, "python2": true, "python3": true, "perl": true, "ruby": true, "node": true, "php": true, "lua": true,
	"docker": true, "nsenter": true, "chroot": true, "sudo": true, "su": true, "ssh": true,
	"nohup": true, "timeout": true, "watch": true, "script": true, "vi": true, "vim": true, "less": true, "man": true,
}

// IsEscapeBinary reports whether name (a bare executable name) is able to
// execute arbitrary code, which defeats the purpose of an allowlist.
func IsEscapeBinary(name string) bool {
	return escapeBinaries[strings.ToLower(name)]
}

// maxCapturedOutput caps what a single command may leave in memory. Callers
// truncate to a few thousand characters anyway; without a cap `cat /dev/zero`
// inside the timeout window buffers gigabytes.
const maxCapturedOutput = 4 << 20

// limitedBuffer keeps the first limit bytes and silently discards the rest. It
// keeps reporting full writes so the child is drained rather than blocked on a
// full pipe, and is killed by the context deadline like any other command.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte { return b.buf.Bytes() }
