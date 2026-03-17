package main

import (
	"context"
	"errors"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestSleepWithContextCancelled(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()

	if ok := sleepWithContext(c, 2*time.Second); ok {
		t.Fatalf("sleepWithContext should return false when context is already canceled")
	}
}

func TestPeriodicReportStopsOnCancelledContext(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Config.Intervals.StatsSeconds = 1
	ctx.Settings.ReportMode = 1

	c, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		periodicReport(ctx, &fakeBot{}, c)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("periodicReport did not stop promptly on canceled context")
	}
}

type markdownFailBot struct {
	attempts    int
	parseModes  []string
	alwaysError bool
}

func (b *markdownFailBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	b.attempts++
	if m, ok := c.(tgbotapi.MessageConfig); ok {
		b.parseModes = append(b.parseModes, m.ParseMode)
		if m.ParseMode == "Markdown" || b.alwaysError {
			return tgbotapi.Message{}, errors.New("simulated send failure")
		}
	}
	return tgbotapi.Message{MessageID: b.attempts, Chat: &tgbotapi.Chat{ID: 1}}, nil
}

func (b *markdownFailBot) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{}, nil
}

func TestSendScheduledReportFallsBackToPlainText(t *testing.T) {
	bot := &markdownFailBot{}

	err := sendScheduledReport(bot, 1, "*hello*")
	if err != nil {
		t.Fatalf("expected fallback success, got error: %v", err)
	}

	if bot.attempts != 2 {
		t.Fatalf("expected 2 attempts (markdown + plain), got %d", bot.attempts)
	}

	if len(bot.parseModes) != 2 || bot.parseModes[0] != "Markdown" || bot.parseModes[1] != "" {
		t.Fatalf("unexpected parse mode attempts: %v", bot.parseModes)
	}
}

func TestSendScheduledReportReturnsErrorWhenBothFail(t *testing.T) {
	bot := &markdownFailBot{alwaysError: true}

	err := sendScheduledReport(bot, 1, "hello")
	if err == nil {
		t.Fatalf("expected error when both markdown and plain text fail")
	}
}
