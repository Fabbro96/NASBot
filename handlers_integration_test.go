package main

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestCommandHandlersSmoke(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	ctx := InitApp(&Config{AllowedUserID: 1, Cache: CacheConfig{DockerTTLSeconds: 60}})
	ctx.Stats.Set(Stats{
		CPU:    12,
		RAM:    34,
		Swap:   0,
		Uptime: 3600,
		VolSSD: VolumeStats{Used: 10, Free: 50 * giB},
		VolHDD: VolumeStats{Used: 20, Free: 100 * giB},
	})
	ctx.Settings.Language = "en"
	ctx.Bot.StartTime = time.Now().Add(-10 * time.Minute)
	ctx.Docker.Cache = DockerCache{Containers: []ContainerInfo{{Name: "x", Running: true}}, LastUpdate: time.Now()}

	restore := setCommandRunner(mockRunner{exists: true, out: []byte(""), err: nil})
	t.Cleanup(restore)

	r := SetupCommandRegistry()

	tests := []string{"/status", "/quick", "/ping", "/help"}
	for _, cmd := range tests {
		msg := &tgbotapi.Message{
			Text:     cmd,
			Chat:     &tgbotapi.Chat{ID: 1},
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: len(cmd)}},
		}
		if ok := r.Execute(ctx, nil, msg); !ok {
			t.Fatalf("Execute returned false for %s", cmd)
		}
	}
}
