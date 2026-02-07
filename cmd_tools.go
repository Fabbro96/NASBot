package main

import (
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
