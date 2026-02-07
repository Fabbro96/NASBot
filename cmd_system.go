package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type StatusCmd struct{}

func (c *StatusCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendWithKeyboard(ctx, bot, msg.Chat.ID, getStatusText(ctx))
}
func (c *StatusCmd) Description() string { return "Show system status" }

type TopCmd struct{}

func (c *TopCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendWithKeyboard(ctx, bot, msg.Chat.ID, getTopProcText(ctx))
}
func (c *TopCmd) Description() string { return "Show top processes" }

type SysInfoCmd struct{}

func (c *SysInfoCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getSysInfoText(ctx))
}
func (c *SysInfoCmd) Description() string { return "Show detailed system info" }

type TempCmd struct{}

func (c *TempCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendWithKeyboard(ctx, bot, msg.Chat.ID, getTempText(ctx))
}
func (c *TempCmd) Description() string { return "Show temperature sensors" }

type PowerCmd struct{ Action string }

func (c *PowerCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	askPowerConfirmation(ctx, bot, msg.Chat.ID, 0, c.Action)
}
func (c *PowerCmd) Description() string { return "Reboot or shutdown" }
