package app

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

func TestGuardedDiskUsageDoesNotBlockOnStuckMount(t *testing.T) {
	release := make(chan struct{})
	saved := diskUsageFn
	diskUsageFn = func(string) (*disk.UsageStat, error) {
		<-release
		return &disk.UsageStat{}, nil
	}
	defer func() { diskUsageFn = saved; close(release) }()

	start := time.Now()
	if _, err := guardedDiskUsage("/stuck-test"); err == nil {
		t.Fatal("expected an error for a blocked mount with no last value")
	}
	if time.Since(start) > diskUsageTimeout+2*time.Second {
		t.Fatalf("blocked for %v", time.Since(start))
	}
	// A second call must return immediately instead of spawning another goroutine.
	start = time.Now()
	if _, err := probeDiskUsage("/stuck-test"); err == nil {
		t.Fatal("probe must report a blocked mount as an error")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("second call blocked for %v", time.Since(start))
	}
}
