package commands

import (
	"time"
)

func newTestAppContext() *AppContext {
	giB := uint64(1024 * 1024 * 1024)
	return &AppContext{
		Config: &Config{AllowedUserID: 1, Cache: CacheConfig{DockerTTLSeconds: 60}},
		Stats: &ThreadSafeStats{Data: Stats{
			CPU:    12,
			RAM:    34,
			Swap:   0,
			Uptime: 3600,
			VolSSD: VolumeStats{Used: 10, Free: 50 * giB},
			VolHDD: VolumeStats{Used: 20, Free: 100 * giB},
		}, Ready: true},
		State:    &RuntimeState{TimeLocation: time.UTC},
		Settings: &UserSettings{Language: "en"},
		Bot:      &BotContext{StartTime: time.Now().Add(-10 * time.Minute)},
		Monitor:  &MonitorState{},
		Docker: &DockerManager{
			Cache: DockerCache{Containers: []ContainerInfo{{Name: "x", Running: true}}, LastUpdate: time.Now()},
		},
	}
}
