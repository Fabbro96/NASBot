package cmdexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// systemPaths are extra directories searched when exec.LookPath fails.
// This ensures commands like "reboot" or "shutdown" are found even when
// /sbin and /usr/sbin are absent from the process PATH (e.g. under cron).
var systemPaths = []string{
	"/usr/sbin",
	"/sbin",
	"/usr/bin",
	"/bin",
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

	for _, dir := range systemPaths {
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
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, resolved, args...).CombinedOutput()
}

func (defaultRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return nil, err
	}
	return exec.CommandContext(ctx, resolved, args...).Output()
}

func (defaultRunner) Run(ctx context.Context, name string, args ...string) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedOS
	}
	resolved, err := resolveName(name)
	if err != nil {
		return err
	}
	return exec.CommandContext(ctx, resolved, args...).Run()
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
