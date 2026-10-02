package commands

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type ProcessesCmd struct{}

func (c *ProcessesCmd) Execute(ctx *AppContext, bot BotAPI, msg *tgbotapi.Message, args string) {
	text, kb := getProcessesMenu(ctx)

	outMsg := tgbotapi.NewMessage(msg.Chat.ID, text)
	outMsg.ParseMode = "Markdown"
	outMsg.ReplyMarkup = kb
	safeSend(bot, outMsg)
}

func (c *ProcessesCmd) Description() string {
	return "Interactive process manager (kill/terminate)"
}

func getProcessesMenu(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	procs, err := collectTopProcesses(procMenuMaxCount, procMenuMaxNameLen)
	if err != nil {
		return trf(ctx.Tr, "proc_fetch_err", err), tgbotapi.NewInlineKeyboardMarkup()
	}
	if len(procs) == 0 {
		return ctx.Tr("proc_none_found"), tgbotapi.NewInlineKeyboardMarkup()
	}

	text := ctx.Tr("proc_header")

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(procs)+1)
	for _, p := range procs {
		text += fmt.Sprintf("`%-5s %-4s %-4s %s`\n", p.PID, p.CPU, p.MEM, p.Name)

		btnText := fmt.Sprintf("🛑 %s (%s)", p.Name, p.PID)
		btnData := fmt.Sprintf("proc_manage_%s", p.PID)

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, btnData),
		))
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("proc_refresh_btn"), "proc_refresh"),
	))

	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func GetProcessesMenu(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	return getProcessesMenu(ctx)
}
