package commands

import (
	"log/slog"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Import types from root package
// Note: We avoid direct imports to prevent circular dependencies at init time
// Instead, we relyon type forwarding at runtime

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

// Register adds a command to the registry.
//
// A duplicate name is a programming error, not a runtime condition: the old
// silently-overwritten handler used to keep running while the code below
// believed the new one was registered. Fail fast at startup instead.
func (r *CommandRegistry) Register(name string, cmd Command) {
	if name == "" {
		panic("commands: Register called with an empty command name")
	}
	if cmd == nil {
		panic("commands: Register called with a nil command for " + name)
	}
	if prev, dup := r.commands[name]; dup {
		panic("commands: duplicate registration of /" + name + " (" + prev.Description() + ")")
	}
	r.commands[name] = cmd
}

// privilegedCommands are the commands that reach the host system or the
// configuration: shell execution, power control, backup (it ships bot_token and
// gemini_api_key) and config patching.
//
// The update loop only compares msg.Chat.ID with AllowedUserID, so an
// allowed_user_id that accidentally holds a group or channel id would hand
// arbitrary command execution to every member of it. Here the *sender* is
// checked too: in a group only the allowed user can trigger these commands.
var privilegedCommands = map[string]bool{
	"agy":         true,
	"backup":      true,
	"cmd":         true,
	"configset":   true,
	"exec":        true,
	"forcereboot": true,
	"reboot":      true,
	"shell":       true,
	"shutdown":    true,
}

// senderAuthorized reports whether msg comes from the allowed user.
func senderAuthorized(ctx *AppContext, msg *tgbotapi.Message) bool {
	if ctx == nil || msg == nil {
		return false
	}
	c := cfg(ctx)
	if c == nil || c.AllowedUserID == 0 {
		return false
	}
	if msg.From == nil {
		return false
	}
	return msg.From.ID == c.AllowedUserID
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
	cmd, ok := r.commands[cmdName]
	if !ok {
		// Alias handling could go here if needed, or simply strictly map commands
		return false
	}

	// ctx == nil means there is no configuration to check against (unit tests
	// only); production always passes a context.
	if privilegedCommands[cmdName] && ctx != nil && !senderAuthorized(ctx, msg) {
		slog.Warn("Unauthorized command denied",
			"command", cmdName,
			"chat_id", msg.Chat.ID,
			"user_id", userIDOf(msg))
		if ctx != nil && msg.Chat != nil {
			sendMarkdown(bot, msg.Chat.ID, ctx.Tr("command_denied"))
		}
		// Handled (refused): do not fall through to "unknown command".
		return true
	}

	cmd.Execute(ctx, bot, msg, msg.CommandArguments())
	return true
}

func userIDOf(msg *tgbotapi.Message) int64 {
	if msg == nil || msg.From == nil {
		return 0
	}
	return msg.From.ID
}
