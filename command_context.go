package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Command is the interface that all bot commands must implement
type Command interface {
	Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string)
	Description() string
}

// CommandRegistry holds the map of commands
type CommandRegistry struct {
	commands map[string]Command
}

// NewCommandRegistry creates a new registry
func NewCommandRegistry() *CommandRegistry {
	return &CommandRegistry{
		commands: make(map[string]Command),
	}
}

// Register adds a command to the registry
func (r *CommandRegistry) Register(name string, cmd Command) {
	r.commands[name] = cmd
}

// Execute runs a command if found
func (r *CommandRegistry) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message) bool {
	if msg == nil {
		return false
	}
	cmdName := msg.Command()
	if cmdName == "" {
		return false
	}
	if cmd, ok := r.commands[cmdName]; ok {
		cmd.Execute(ctx, bot, msg, msg.CommandArguments())
		return true
	}
	// Alias handling could go here if needed, or simply strictly map commands
	return false
}
