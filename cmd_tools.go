package main

import (
	"fmt"
	"strings"

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
	if ctx.Config.GeminiAPIKey == "" {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("ask_no_gemini"))
		return
	}

	modelName := "gemini-2.5-flash"
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

	prompt := fmt.Sprintf(ctx.Tr("ask_prompt"), question, logs)

	analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
		newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("ask_analyzing"), model)
		edit := tgbotapi.NewEditMessageText(msg.Chat.ID, sentMsg.MessageID, newText)
		edit.ParseMode = "Markdown"
		safeSend(bot, edit)
	})
	if err != nil {
		errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("ask_error"), err)
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
