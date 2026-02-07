package main

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type testCmd struct {
	called bool
	args   string
}

func (t *testCmd) Execute(_ *AppContext, _ BotAPI, _ *tgbotapi.Message, args string) {
	t.called = true
	t.args = args
}

func (t *testCmd) Description() string { return "test" }

func TestCommandRegistryExecute(t *testing.T) {
	r := NewCommandRegistry()
	cmd := &testCmd{}
	r.Register("ping", cmd)

	msg := &tgbotapi.Message{
		Text: "/ping hello",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 5},
		},
	}

	if ok := r.Execute(nil, nil, msg); !ok {
		t.Fatalf("Execute returned false for registered command")
	}
	if !cmd.called {
		t.Fatalf("Execute did not call command")
	}
	if cmd.args != "hello" {
		t.Fatalf("Command args = %q, want %q", cmd.args, "hello")
	}
}

func TestCommandRegistryExecuteUnknown(t *testing.T) {
	r := NewCommandRegistry()
	msg := &tgbotapi.Message{
		Text: "/unknown",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 8},
		},
	}

	if ok := r.Execute(nil, nil, msg); ok {
		t.Fatalf("Execute returned true for unknown command")
	}
}

func TestCommandRegistryExecuteNil(t *testing.T) {
	r := NewCommandRegistry()
	if ok := r.Execute(nil, nil, nil); ok {
		t.Fatalf("Execute returned true for nil message")
	}
}
