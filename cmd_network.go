package main

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type NetCmd struct{}

func (c *NetCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	sendMarkdown(bot, msg.Chat.ID, getNetworkText(ctx))
}
func (c *NetCmd) Description() string { return "Show network information" }

type SpeedtestCmd struct{}

func (c *SpeedtestCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	handleSpeedtest(ctx, bot, msg.Chat.ID)
}
func (c *SpeedtestCmd) Description() string { return "Run network speed test" }
