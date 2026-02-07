package cmdexec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
)

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
	_, err := exec.LookPath(name)
	return err == nil
}

func (defaultRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedOS
	}
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("command %s not found", name)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (defaultRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrUnsupportedOS
	}
	if _, err := exec.LookPath(name); err != nil {
		return nil, fmt.Errorf("command %s not found", name)
	}
	return exec.CommandContext(ctx, name, args...).Output()
}

func (defaultRunner) Run(ctx context.Context, name string, args ...string) error {
	if runtime.GOOS != "linux" {
		return ErrUnsupportedOS
	}
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("command %s not found", name)
	}
	return exec.CommandContext(ctx, name, args...).Run()
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
