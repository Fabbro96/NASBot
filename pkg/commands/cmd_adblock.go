package commands

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type AdBlockCmd struct{}

func (c *AdBlockCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	if !ctx.Config.AdBlock.Enabled {
		sendMarkdown(bot, msg.Chat.ID, ctx.Tr("adblock_disabled"))
		return
	}

	text := ctx.Tr("adblock_menu_title")

	btnPause5 := tgbotapi.NewInlineKeyboardButtonData("⏸ "+ctx.Tr("adblock_pause_5m"), "adblock_pause_5m")
	btnResume := tgbotapi.NewInlineKeyboardButtonData("▶️ "+ctx.Tr("adblock_resume"), "adblock_resume")

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btnPause5, btnResume),
	)

	reply := tgbotapi.NewMessage(msg.Chat.ID, text)
	reply.ParseMode = "Markdown"
	reply.ReplyMarkup = keyboard
	safeSend(bot, reply)
}

func (c *AdBlockCmd) Description() string { return "AdBlock Control (Pi-hole/Adguard)" }
