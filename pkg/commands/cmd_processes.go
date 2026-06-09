package commands

import (
	"context"
	"fmt"
	"strings"

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
	reqCtx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err), tgbotapi.NewInlineKeyboardMarkup()
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return "Nessun processo trovato.", tgbotapi.NewInlineKeyboardMarkup()
	}

	text := "⚙️ *Gestore Processi*\n\nSeleziona un processo per gestirlo:\n\n`PID   CPU  MEM  NAME`\n"

	count := 0
	var rows [][]tgbotapi.InlineKeyboardButton

	for i := 1; i < len(lines) && count < 10; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid := fields[0]
		cmdName := fields[1]
		cpuPct := fields[2]
		memPct := fields[3]

		if len(cmdName) > 12 {
			cmdName = cmdName[:10] + ".."
		}

		text += fmt.Sprintf("`%-5s %-4s %-4s %s`\n", pid, cpuPct, memPct, cmdName)

		btnText := fmt.Sprintf("🛑 %s (%s)", cmdName, pid)
		btnData := fmt.Sprintf("proc_manage_%s", pid)
		
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(btnText, btnData),
		))

		count++
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Aggiorna", "proc_refresh"),
	))

	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func GetProcessesMenu(ctx *AppContext) (string, tgbotapi.InlineKeyboardMarkup) {
	return getProcessesMenu(ctx)
}
