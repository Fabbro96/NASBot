package commands

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type PingCmd struct{}

func (c *PingCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getPingText(ctx))
}
func (c *PingCmd) Description() string { return "Check bot latency and uptime" }

type LogsCmd struct{}

func (c *LogsCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getLogsText(ctx))
}
func (c *LogsCmd) Description() string { return "Show recent system logs" }

type LogSearchCmd struct{}

func (c *LogSearchCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getLogSearchText(ctx, args))
}
func (c *LogSearchCmd) Description() string { return "Search system logs" }

type HelpCmd struct{}

func (c *HelpCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getHelpText(ctx))
}
func (c *HelpCmd) Description() string { return "Show help message" }

type AskCmd struct{}

func (c *AskCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	question := strings.TrimSpace(args)
	if question == "" {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("ask_usage"))
		return
	}
	conf := cfg(ctx)
	if conf == nil || conf.GeminiAPIKey == "" {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("ask_no_gemini"))
		return
	}

	modelName := defaultGeminiModel
	loadingText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("ask_analyzing"), modelName)
	loadingMsg := tgbotapi.NewMessage(msg.Chat.ID, loadingText)
	loadingMsg.ParseMode = "Markdown"
	sentMsg, err := bot.Send(loadingMsg)
	if err != nil {
		loadingMsg.ParseMode = ""
		sentMsg, _ = bot.Send(loadingMsg)
	}

	logs, err := getRecentLogs(ctx)
	if err != nil {
		errText := ctx.Tr("ask_no_logs")
		if sentMsg.MessageID != 0 {
			editMessage(bot, msg.Chat.ID, sentMsg.MessageID, errText, nil)
			return
		}
		sendMarkdown(bot, msg.Chat.ID, errText)
		return
	}

	prompt := trf(ctx.Tr, "ask_prompt", question, logs)

	analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
		newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("ask_analyzing"), model)
		edit := tgbotapi.NewEditMessageText(msg.Chat.ID, sentMsg.MessageID, newText)
		edit.ParseMode = "Markdown"
		safeSend(bot, edit)
	})
	if err != nil {
		errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("ask_error"), sanitizeErr(err))
		if sentMsg.MessageID != 0 {
			editMessage(bot, msg.Chat.ID, sentMsg.MessageID, errText, nil)
			return
		}
		sendMarkdown(bot, msg.Chat.ID, errText)
		return
	}

	result := fmt.Sprintf("🤖 *%s*\n\n%s", ctx.Tr("ask_title"), analysis)
	if sentMsg.MessageID != 0 {
		editMessage(bot, msg.Chat.ID, sentMsg.MessageID, result, nil)
		return
	}
	sendMarkdown(bot, msg.Chat.ID, result)
}
func (c *AskCmd) Description() string { return "Ask the AI about recent logs" }

type QuickCmd struct{}

func (c *QuickCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getQuickText(ctx))
}
func (c *QuickCmd) Description() string { return "Show quick status summary" }

type DiskPredCmd struct{}

func (c *DiskPredCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getDiskPredictionText(ctx))
}
func (c *DiskPredCmd) Description() string { return "Show disk usage prediction" }

type HealthCmd struct{}

func (c *HealthCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	handleHealthCommand(ctx, bot, msg.Chat.ID)
}
func (c *HealthCmd) Description() string { return "Show healthchecks.io integration status" }

type UpdateCmd struct{}

func (c *UpdateCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	rel, hasUpdate, err := checkForUpdate(ctx)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, trf(ctx.Tr, "update_check_failed", sanitizeErr(err)))
		return
	}
	if !hasUpdate {
		sendMarkdown(bot, msg.Chat.ID, trf(ctx.Tr, "update_none", getVersion()))
		return
	}

	text := trf(ctx.Tr, "update_available_confirm", rel.Tag, rel.Changelog)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("yes"), "update_apply_latest"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("no"), "update_cancel"),
		),
	)
	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = kb
	if _, err := bot.Send(m); err != nil {
		slog.Error("Update available message failed, retrying plain text", "err", sanitizeErr(err))
		m.ParseMode = ""
		safeSend(bot, m)
	}
}
func (c *UpdateCmd) Description() string { return "Download latest GitHub release and restart NASBot" }

type ChangelogCmd struct{}

func (c *ChangelogCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, trf(ctx.Tr, "update_check_failed", sanitizeErr(err)))
		return
	}

	title := trf(ctx.Tr, "changelog_title", rel.Tag)
	text := fmt.Sprintf("%s\n\n%s\n\n[Release Page](%s)", title, rel.Changelog, rel.URL)
	sendMarkdown(bot, msg.Chat.ID, text)
}
func (c *ChangelogCmd) Description() string { return "Show the latest release changelog" }

type AgyCmd struct{}

func (c *AgyCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	cmdArgs := strings.TrimSpace(args)
	slog.Info("Executing agy command via NASBot", "args", cmdArgs, "user_id", userIDOf(msg))
	var execArgs []string
	if cmdArgs != "" {
		execArgs = strings.Fields(cmdArgs)
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runCommandOutput(reqCtx, "agy", execArgs...)
	outputStr := commandOutput(out, err, maxCmdOutputChars)
	result := trf(ctx.Tr, "agy_title", outputStr)
	sendMarkdown(bot, msg.Chat.ID, result)
}
func (c *AgyCmd) Description() string { return "Execute Antigravity CLI (agy)" }

type CmdCmd struct{}

func (c *CmdCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	// Default denied. Every branch below runs only after the gate is open, so a
	// configuration that was never filled in, was truncated, or lost the section
	// on a merge behaves exactly like an explicit "off".
	gate := readShellCommandGate(cfg(ctx))
	if !gate.enabled {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("cmd_disabled"))
		return
	}

	argv, err := splitCommandLine(args)
	if err != nil {
		sendMarkdown(bot, msg.Chat.ID, trf(ctx.Tr, "cmd_parse_error", sanitizeErr(err)))
		return
	}
	if len(argv) == 0 {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("cmd_usage"))
		return
	}

	binary, commandArgs := argv[0], argv[1:]
	if !gate.allows(binary) {
		slog.Warn("Command refused by the /cmd allowlist",
			"command", binary,
			"user_id", userIDOf(msg),
			"allowlist_size", len(gate.allowlist))
		sendMarkdown(bot, msg.Chat.ID, trf(ctx.Tr, "cmd_not_allowed",
			displayBinaryName(binary), gate.allowlistDisplay()))
		return
	}

	slog.Info("Executing allowlisted command via NASBot",
		"command", binary,
		"arg_count", len(commandArgs),
		"user_id", userIDOf(msg))
	reqCtx, cancel := context.WithTimeout(context.Background(), gate.timeout)
	defer cancel()
	out, err := runCommandOutput(reqCtx, binary, commandArgs...)
	result := getCmdResultText(ctx, displayCommandLine(argv), commandOutput(out, err, gate.maxOutput))
	sendMarkdown(bot, msg.Chat.ID, result)
}
func (c *CmdCmd) Description() string { return "Run an allowlisted command (off by default)" }

// shellCommandGate is the /cmd capability as the command sees it: the feature
// switch, a copy of the allowlist, and the two limits.
//
// The limits are read with the same fallbacks sanitizeConfig applies, so a
// Config assembled in memory (unit tests, the standalone watchdog) cannot
// produce a zero timeout that would kill every command on the spot.
type shellCommandGate struct {
	enabled   bool
	allowlist []string
	timeout   time.Duration
	maxOutput int
}

// readShellCommandGate turns the published configuration into the gate. c may
// be nil, which is "off": a missing configuration is not a licence to run
// anything.
func readShellCommandGate(c *Config) shellCommandGate {
	gate := shellCommandGate{
		timeout:   defaultShellCommandTimeout,
		maxOutput: defaultShellCommandMaxOutput,
	}
	if c == nil {
		return gate
	}

	sc := c.ShellCommand
	if sc.TimeoutSeconds > 0 {
		gate.timeout = time.Duration(sc.TimeoutSeconds) * time.Second
	}
	if sc.MaxOutputChars > 0 {
		gate.maxOutput = sc.MaxOutputChars
	}

	gate.allowlist = make([]string, 0, len(sc.AllowedBinaries))
	for _, name := range sc.AllowedBinaries {
		// The same check sanitizeConfig applies, re-applied here: this is the
		// boundary, and a Config that never went through the loader must not be
		// able to name a path from here.
		if isShellBinaryName(name) {
			gate.allowlist = append(gate.allowlist, name)
		}
	}

	// "Enabled" with an empty allowlist runs nothing. sanitizeConfig refuses
	// that combination; repeating it here keeps the two paths identical.
	gate.enabled = sc.Enabled && len(gate.allowlist) > 0
	return gate
}

// allows reports whether binary is in the allowlist. The entry has already been
// validated, so this is an exact string comparison: a path, a traversal or a
// name carrying a separator can never be in the list, and therefore can never
// match what the user typed.
func (g shellCommandGate) allows(binary string) bool {
	for _, name := range g.allowlist {
		if name == binary {
			return true
		}
	}
	return false
}

// allowlistDisplay renders the allowlist as a comma separated list of code
// spans, for the refusal message.
func (g shellCommandGate) allowlistDisplay() string {
	if len(g.allowlist) == 0 {
		return "-"
	}
	spans := make([]string, 0, len(g.allowlist))
	for _, name := range g.allowlist {
		spans = append(spans, "`"+name+"`")
	}
	return strings.Join(spans, ", ")
}

// Fallbacks for a Config that carries no usable limits, used when sanitizeConfig
// never ran over it (a Config built in memory, by a unit test or by the
// standalone watchdog binary). They mirror shellCommandOutputChars and
// defaultShellCommandTimeoutSeconds in internal/app/config_defaults.go and must
// be changed with them.
const (
	defaultShellCommandTimeout   = 30 * time.Second
	defaultShellCommandMaxOutput = 3800
)

// isShellBinaryName reports whether entry is a bare executable name: no
// separator, no whitespace, no shell metacharacter, and short.
//
// The rules are duplicated from isShellBinaryName in internal/app/config.go on
// purpose. That one cleans up the file; this one is the boundary the executor
// actually checks, and a validator that only lived in the loader would leave a
// Config built in memory (tests, the watchdog binary) unfiltered. The
// duplication is the same one heavyContainerMemPercent carries, and the two
// must be changed together.
func isShellBinaryName(entry string) bool {
	if entry == "" || len(entry) > 64 {
		return false
	}
	if entry == "." || entry == ".." || strings.ContainsAny(entry, `/\`) {
		return false
	}
	for _, r := range entry {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '+':
		default:
			return false
		}
	}
	return true
}

// splitCommandLine splits a /cmd line into an argv.
//
// It understands single quotes, double quotes and backslash escapes, and
// nothing else. There is no variable expansion, no command substitution, no
// glob and no operator: `;`, `|`, `&&`, `$(id)` and `$HOME` are ordinary
// characters that end up inside a single argument. That is the point of the
// function — the result reaches exec.Command verbatim, so nothing the user
// types can become a second command.
//
// A quote that is never closed is an error rather than a best-effort split:
// guessing would run a command different from the one that was refused, and the
// refusal message would then be about the wrong one.
func splitCommandLine(line string) ([]string, error) {
	const (
		noQuote     = 0
		singleQuote = '\''
		doubleQuote = '"'
	)

	var (
		args    []string
		current strings.Builder
		open    byte
		// hasToken separates an empty argument (`ls ""`) from no argument at
		// all, which is what decides whether the next space flushes.
		hasToken bool
	)

	flush := func() {
		if !hasToken {
			return
		}
		args = append(args, current.String())
		current.Reset()
		hasToken = false
	}

	for i := 0; i < len(line); i++ {
		c := line[i]
		if open != noQuote {
			if c == open {
				open = noQuote
				continue
			}
			// Inside double quotes a backslash escapes the next byte; inside
			// single quotes it is literal, as in every other shell.
			if open == doubleQuote && c == '\\' && i+1 < len(line) {
				i++
				c = line[i]
			}
			current.WriteByte(c)
			hasToken = true
			continue
		}

		switch c {
		case singleQuote, doubleQuote:
			open = c
			hasToken = true
		case '\\':
			if i+1 >= len(line) {
				return nil, errors.New("line ends with a backslash")
			}
			i++
			current.WriteByte(line[i])
			hasToken = true
		case ' ', '\t', '\n', '\r', '\v', '\f':
			flush()
		default:
			current.WriteByte(c)
			hasToken = true
		}
	}

	if open != noQuote {
		return nil, errors.New("unterminated quote")
	}
	flush()
	return args, nil
}

// displayBinaryName renders a refused binary name for the chat, replacing every
// character a binary name cannot contain with '?'. The name comes from the
// message: echoing it verbatim would let a caller inject Markdown into the
// reply, and the substituted form still shows which name was refused — and
// which part of it is not a bare name.
func displayBinaryName(name string) string {
	if name == "" {
		return "?"
	}
	out := []rune(name)
	for i, r := range out {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.' || r == '_' || r == '-' || r == '+':
		default:
			out[i] = '?'
		}
	}
	return string(out)
}

// displayCommandLine renders the argv for the header of a successful reply.
//
// Unlike the refused name, arguments are left readable: a path like
// /volume1 must survive, so the rule is not the binary-name charset but the
// narrower one of what can break the surrounding code span. Inside a Telegram
// code span the only character that ends it is a backtick; control characters
// are replaced because they would break the layout. Anything else is inert.
//
// This is a display concern only: what actually ran is the argv handed to
// runCommandOutput, untouched.
func displayCommandLine(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, token := range argv {
		out := []rune(token)
		for i, r := range out {
			if r == '`' || r < 0x20 || r == 0x7f {
				out[i] = '?'
			}
		}
		parts = append(parts, string(out))
	}
	return strings.Join(parts, " ")
}

// commandOutput renders the output of /cmd and /agy, truncating it rune-safe:
// a byte slice would split a multi-byte rune in half and Telegram would render
// U+FFFD (or reject the whole message).
func commandOutput(out []byte, err error, maxChars int) string {
	outputStr := strings.TrimSpace(string(out))
	if err != nil && outputStr == "" {
		outputStr = fmt.Sprintf("❌ Error: %v", sanitizeErr(err))
	}
	if maxChars <= 0 {
		maxChars = maxCmdOutputChars
	}
	return format.Truncate(outputStr, maxChars)
}
