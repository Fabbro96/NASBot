package main

import "context"

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
