package main

import (
	"fmt"
	"log/slog"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ReportCmd struct{}

func (c *ReportCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	chatID := msg.Chat.ID
	modelName := "gemini-2.5-flash"
	loadingMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("generating_report"), modelName))
	loadingMsg.ParseMode = tgbotapi.ModeMarkdown
	sentMsg, err := bot.Send(loadingMsg)
	if err != nil {
		slog.Error("Error sending report loading message", "err", err)
		loadingMsg.ParseMode = ""
		loadingMsg.Text = strings.ReplaceAll(loadingMsg.Text, "*", "")
		if fallbackMsg, sendErr := bot.Send(loadingMsg); sendErr == nil {
			sentMsg = fallbackMsg
		} else {
			slog.Error("Error sending report loading message (plain text)", "err", sendErr)
		}
	}

	onModelChange := func(newModel string) {
		if sentMsg.MessageID != 0 {
			edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, fmt.Sprintf(ctx.Tr("generating_report"), newModel))
			edit.ParseMode = tgbotapi.ModeMarkdown
			if _, err := bot.Send(edit); err != nil {
				slog.Error("Error editing loading message", "err", err)
				edit.ParseMode = ""
				edit.Text = strings.ReplaceAll(edit.Text, "*", "")
				safeSend(bot, edit)
			}
		}
	}

	report := generateReport(ctx, true, onModelChange)

	if strings.TrimSpace(report) == "" {
		report = "⚠️ Error: Generated report is empty."
	}

	if sentMsg.MessageID != 0 {
		// Try to edit the existing message first to avoid "disappearing" effect
		edit := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, report)
		edit.ParseMode = tgbotapi.ModeMarkdown
		if _, err := bot.Send(edit); err != nil {
			slog.Error("Error editing final report (Markdown). Retrying edit as plain text.", "err", err)
			edit.ParseMode = ""
			if _, err := bot.Send(edit); err != nil {
				slog.Error("Error editing final report (Text). Falling back to delete and send.", "err", err)
				bot.Request(tgbotapi.NewDeleteMessage(chatID, sentMsg.MessageID))
				sendMarkdown(bot, chatID, report)
			}
		}
	} else {
		sendMarkdown(bot, chatID, report)
	}
}
func (c *ReportCmd) Description() string { return "Generate status report" }
