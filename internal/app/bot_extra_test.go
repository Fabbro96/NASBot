package app

import (
	"errors"
	"path/filepath"
	"testing"

	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestParseUptime_Hours(t *testing.T) {
	if got := parseUptime("Up 2 hours"); got != "2 hours" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Seconds(t *testing.T) {
	if got := parseUptime("Up 12 seconds"); got != "12 seconds" {
		t.Errorf("failed")
	}
}

func TestParseUptime_JustUp(t *testing.T) {
	if got := parseUptime("Up"); got != "running" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Exited(t *testing.T) {
	if got := parseUptime("Exited (0) 2 hours ago"); got != "stopped" {
		t.Errorf("failed")
	}
}

func TestParseUptime_Created(t *testing.T) {
	if got := parseUptime("Created"); got != "stopped" {
		t.Errorf("failed")
	}
}

func TestTruncate_Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("failed")
	}
}

func TestTruncate_Long(t *testing.T) {
	if got := truncate("hello world", 5); got != "hell~" {
		t.Errorf("failed: got %q", got)
	}
}

func TestTruncate_Empty(t *testing.T) {
	if got := truncate("", 5); got != "" {
		t.Errorf("failed")
	}
}

func TestCommandExists_Ls(t *testing.T) {
	if !commandExists("ls") {
		t.Errorf("failed")
	}
}

func TestCommandExists_NonExistent(t *testing.T) {
	if commandExists("nonexistent_123456") {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_NilCtx(t *testing.T) {
	devs := getSmartDevices(nil)
	if len(devs) != 2 || devs[0] != "sda" {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_EmptyCtx(t *testing.T) {
	ctx := &AppContext{Config: &Config{}}
	devs := getSmartDevices(ctx)
	if len(devs) != 2 || devs[0] != "sda" {
		t.Errorf("failed")
	}
}

func TestGetSmartDevices_CustomCtx(t *testing.T) {
	ctx := &AppContext{Config: &Config{Notifications: NotificationsConfig{SMART: SmartConfig{Devices: []string{"nvme0n1"}}}}}
	devs := getSmartDevices(ctx)
	if len(devs) != 1 || devs[0] != "nvme0n1" {
		t.Errorf("failed")
	}
}

// scriptedBot answers Send with a fixed list of results and records what it was
// asked to send, so a test can assert the retry sequence and not just its outcome.
type scriptedBot struct {
	results []error
	sent    []tgbotapi.Chattable
}

func (b *scriptedBot) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	b.sent = append(b.sent, c)
	n := len(b.sent) - 1
	if n >= len(b.results) {
		return tgbotapi.Message{MessageID: len(b.sent)}, b.results[len(b.results)-1]
	}
	return tgbotapi.Message{MessageID: len(b.sent)}, b.results[n]
}

func (b *scriptedBot) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{}, nil
}

var errTelegramRejected = errors.New("telegram: unsupported markdown")

// TestSendWithParseFallbackRetriesMarkdownAsPlainText is the regression guard for
// the class of bug that makes the whole bot look broken: Telegram answers 400 to
// a message with one unpaired Markdown marker, and the only recovery is resending
// it as plain text. If the retry were dropped, every user would read raw
// asterisks and backticks in /status, /help and /settings.
func TestSendWithParseFallbackRetriesMarkdownAsPlainText(t *testing.T) {
	bot := &scriptedBot{results: []error{errTelegramRejected, nil}}

	msg := tgbotapi.NewMessage(1, "*broken*")
	msg.ParseMode = "Markdown"

	if err := sendWithParseFallback(bot, msg); err != nil {
		t.Fatalf("retry as plain text must succeed, got %v", err)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("expected the message to be sent twice, got %d attempts", len(bot.sent))
	}
	retry, ok := bot.sent[1].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("retry is a %T, want tgbotapi.MessageConfig", bot.sent[1])
	}
	if retry.ParseMode != "" {
		t.Errorf("retry still carries ParseMode=%q, so it would be rejected again", retry.ParseMode)
	}
	if retry.Text != msg.Text {
		t.Errorf("retry changed the text: %q, want %q", retry.Text, msg.Text)
	}
}

// TestSendWithParseFallbackPlainMessageIsNotRetried: a message that was never
// Markdown cannot be rejected for its Markdown, so a failure must be reported
// after a single attempt instead of silently sending it twice.
func TestSendWithParseFallbackPlainMessageIsNotRetried(t *testing.T) {
	bot := &scriptedBot{results: []error{errTelegramRejected}}

	msg := tgbotapi.NewMessage(1, "plain")

	err := sendWithParseFallback(bot, msg)
	if !errors.Is(err, errTelegramRejected) {
		t.Fatalf("got %v, want the Telegram error", err)
	}
	if len(bot.sent) != 1 {
		t.Fatalf("a plain message must not be retried, got %d attempts", len(bot.sent))
	}
}

// TestSendWithParseFallbackReportsRetryFailure: when the plain-text retry fails
// too, the caller must learn about it. safeSend logs that error, and a nil return
// here would drop the only trace of a lost message.
func TestSendWithParseFallbackReportsRetryFailure(t *testing.T) {
	retryErr := errors.New("telegram: chat not found")
	bot := &scriptedBot{results: []error{errTelegramRejected, retryErr}}

	msg := tgbotapi.NewMessage(1, "*broken*")
	msg.ParseMode = "Markdown"

	if err := sendWithParseFallback(bot, msg); !errors.Is(err, retryErr) {
		t.Fatalf("got %v, want the retry error", err)
	}
	if len(bot.sent) != 2 {
		t.Fatalf("expected two attempts, got %d", len(bot.sent))
	}
}

// TestSendWithParseFallbackNilBotSkips: a nil bot is how the code says "there is
// no Telegram connection here" (the standalone watchdog). It must be a no-op
// that reports success, not a nil dereference.
func TestSendWithParseFallbackNilBotSkips(t *testing.T) {
	if err := sendWithParseFallback(nil, tgbotapi.NewMessage(1, "x")); err != nil {
		t.Fatalf("nil bot must be skipped, got %v", err)
	}
}

// TestSafeSendSwallowsSendFailure: safeSend has no return value on purpose, so a
// Telegram outage must be logged rather than propagated into the caller. The
// invariant that can actually fail is that the send was attempted at all.
func TestSafeSendSwallowsSendFailure(t *testing.T) {
	bot := &scriptedBot{results: []error{errTelegramRejected, nil}}

	msg := tgbotapi.NewMessage(1, "*broken*")
	msg.ParseMode = "Markdown"

	safeSend(bot, msg)

	if len(bot.sent) != 2 {
		t.Fatalf("expected safeSend to attempt the send and its plain-text retry, got %d", len(bot.sent))
	}
}

// TestStateLoadSave_Integration pins NASBOT_STATE_FILE to a temp dir.
//
// Without it the test wrote and re-read the default path, var/nasbot_state.json
// relative to the package directory, i.e. the file a previous test had left
// there: the assertions then passed on a leftover blob and failed as soon as that
// blob differed. It also left a state file in the working tree on every run.
func TestStateLoadSave_Integration(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	ctx := model.InitApp(nil)
	ctx.State.AddEvent("test", "test_alert_1")

	// Save
	saveState(ctx)

	// Load into new ctx
	newCtx := model.InitApp(nil)
	loadState(newCtx)

	events := newCtx.State.GetEvents()
	if len(events) != 1 || events[0].Message != "test_alert_1" {
		t.Fatalf("expected the single saved alert to be reloaded, got %d events: %+v",
			len(events), events)
	}
}

// TestStateLoadSaveIgnoresForeignFile is the companion of the fix above: the
// reload must read the file this test just wrote and nothing else. It fails if
// loadState silently keeps the events already in memory (i.e. if the round trip
// is skipped) and if it prefers some other path over NASBOT_STATE_FILE.
func TestStateLoadSaveIgnoresForeignFile(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	saved := model.InitApp(nil)
	saved.State.AddEvent("persisted", "kept")
	saveState(saved)

	// A context that already holds an unrelated event: a load that did nothing
	// would leave both events behind and look like a success.
	loaded := model.InitApp(nil)
	loaded.State.AddEvent("in-memory", "must be replaced")
	loadState(loaded)

	events := loaded.State.GetEvents()
	if len(events) != 1 {
		t.Fatalf("loadState must replace the in-memory history, got %d events: %+v",
			len(events), events)
	}
	if events[0].Type != "persisted" || events[0].Message != "kept" {
		t.Fatalf("expected the persisted event, got %+v", events[0])
	}
}
