package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoSafeResilientRestartsAfterPanic(t *testing.T) {
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	restarted := make(chan struct{})

	goSafeResilient("test-resilient", runCtx, 5*time.Millisecond, func() {
		n := calls.Add(1)
		if n == 1 {
			panic("boom")
		}
		if n == 2 {
			select {
			case <-restarted:
			default:
				close(restarted)
			}
		}
	})

	select {
	case <-restarted:
		// expected: second invocation after panic restart
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("resilient goroutine did not restart in time")
	}
}
