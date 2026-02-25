package main

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type fakeBot struct {
	sent     []tgbotapi.Chattable
	requests []tgbotapi.Chattable
	nextID   int
}

func (b *fakeBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	b.sent = append(b.sent, c)
	b.nextID++
	return tgbotapi.Message{MessageID: b.nextID, Chat: &tgbotapi.Chat{ID: 1}}, nil
}

func (b *fakeBot) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	b.requests = append(b.requests, c)
	return &tgbotapi.APIResponse{}, nil
}

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

func TestHandleCommandStatus(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	msg := &tgbotapi.Message{
		Text:     "/status",
		Chat:     &tgbotapi.Chat{ID: 1},
		Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 7}},
	}

	handleCommand(bot, msg)
	if len(bot.sent) == 0 {
		t.Fatalf("expected a reply for /status")
	}
}

func TestHandleCallbackLanguage(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	query := &tgbotapi.CallbackQuery{
		ID:      "1",
		Data:    "set_lang_it",
		From:    &tgbotapi.User{ID: 1},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}, MessageID: 10},
	}

	handleCallback(bot, query)
	if app.Settings.GetLanguage() != "it" {
		t.Fatalf("language not updated")
	}
	if len(bot.requests) == 0 {
		t.Fatalf("expected callback ack request")
	}
}

func TestHandleCallbackLanguageSpanish(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	query := &tgbotapi.CallbackQuery{
		ID:      "2",
		Data:    "set_lang_es",
		From:    &tgbotapi.User{ID: 1},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}, MessageID: 11},
	}

	handleCallback(bot, query)
	if app.Settings.GetLanguage() != "es" {
		t.Fatalf("language not updated to es")
	}
	if len(bot.requests) == 0 {
		t.Fatalf("expected callback ack request")
	}
}

func TestHandleCallbackUnauthorizedIgnored(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	query := &tgbotapi.CallbackQuery{
		ID:      "3",
		Data:    "set_lang_it",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 1}, MessageID: 12},
	}

	handleCallback(bot, query)
	if app.Settings.GetLanguage() != "en" {
		t.Fatalf("unauthorized callback should not modify settings")
	}
	if len(bot.requests) == 0 {
		t.Fatalf("expected callback ack request")
	}
}

func TestHandleCallbackNilIgnored(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	handleCallback(bot, nil)
	if len(bot.requests) != 0 {
		t.Fatalf("expected no callback ack for nil query")
	}
}

func TestHandleCallbackNilMessageIgnored(t *testing.T) {
	prev := app
	app = newTestAppContext()
	t.Cleanup(func() { app = prev })

	bot := &fakeBot{}
	query := &tgbotapi.CallbackQuery{ID: "4", Data: "set_lang_it", From: &tgbotapi.User{ID: 1}}

	handleCallback(bot, query)
	if len(bot.requests) != 0 {
		t.Fatalf("expected no callback ack for callback without message")
	}
}
