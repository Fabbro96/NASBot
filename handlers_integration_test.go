package main

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestCommandHandlersSmoke(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	ctx := &AppContext{
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
	}

	restore := setCommandRunner(mockRunner{exists: true, out: []byte(""), err: nil})
	t.Cleanup(restore)

	r := SetupCommandRegistry()

	tests := []string{"/status", "/quick", "/ping", "/help"}
	for _, cmd := range tests {
		msg := &tgbotapi.Message{
			Text:     cmd,
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(cmd)}},
		}
		if ok := r.Execute(ctx, nil, msg); !ok {
			t.Fatalf("Execute returned false for %s", cmd)
		}
	}
}
