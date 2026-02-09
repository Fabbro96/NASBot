package main

import (
	"strings"
	"testing"
	"time"
)

func TestTextGeneratorsSmoke(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	ctx := &AppContext{
		Stats: &ThreadSafeStats{Data: Stats{
			CPU:    12,
			RAM:    34,
			Swap:   0,
			Uptime: 3600,
			VolSSD: VolumeStats{Used: 10, Free: 50 * giB},
			VolHDD: VolumeStats{Used: 20, Free: 100 * giB},
		}, Ready: true},
		State: &RuntimeState{
			TimeLocation: time.UTC,
			DiskHistory: []DiskUsagePoint{
				{Time: time.Now().Add(-2 * time.Hour), SSDFree: 100 * giB, HDDFree: 200 * giB},
				{Time: time.Now(), SSDFree: 90 * giB, HDDFree: 190 * giB},
			},
		},
		Settings: &UserSettings{Language: "en"},
		Bot:      &BotContext{StartTime: time.Now().Add(-1 * time.Hour)},
		Monitor: &MonitorState{
			CPUTrend: []TrendPoint{},
			RAMTrend: []TrendPoint{},
		},
		Docker: &DockerManager{
			Cache: DockerCache{Containers: []ContainerInfo{{Name: "x", Running: true}}, LastUpdate: time.Now()},
		},
		Config: &Config{Cache: CacheConfig{DockerTTLSeconds: 60}},
	}

	if got := getStatusText(ctx); !strings.Contains(got, "NAS") {
		t.Fatalf("getStatusText missing content: %q", got)
	}
	if got := getQuickText(ctx); got == "" {
		t.Fatalf("getQuickText empty")
	}
	if got := getDiskPredictionText(ctx); !strings.Contains(got, "Disk Space") {
		t.Fatalf("getDiskPredictionText missing content: %q", got)
	}
}
