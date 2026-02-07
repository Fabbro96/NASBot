package main

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// BotAPI abstracts Telegram bot methods used by the app.
type BotAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
}
