package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  SPEEDTEST
// ═══════════════════════════════════════════════════════════════════

func handleSpeedtest(_ *AppContext, bot BotAPI, chatID int64) {
	if !commandExists("speedtest-cli") {
		sendMarkdown(bot, chatID, "❌ `speedtest-cli` not installed.\n\nInstall it with:\n`sudo apt install speedtest-cli`")
		return
	}

	msg := tgbotapi.NewMessage(chatID, "🚀 Running speed test... (this may take a minute)")
	sent, err := bot.Send(msg)
	if err != nil {
		slog.Error("Failed to send speedtest start message", "err", err)
	}

	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	output, err := runCommandOutput(c, "speedtest-cli", "--simple")

	var resultText string
	if err != nil {
		if c.Err() == context.DeadlineExceeded {
			resultText = "⏱ Speed test timed out"
		} else {
			resultText = fmt.Sprintf("❌ Speed test failed:\n`%s`", err.Error())
		}
	} else {
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var ping, download, upload string
		for _, line := range lines {
			if strings.HasPrefix(line, "Ping:") {
				ping = strings.TrimPrefix(line, "Ping: ")
			} else if strings.HasPrefix(line, "Download:") {
				download = strings.TrimPrefix(line, "Download: ")
			} else if strings.HasPrefix(line, "Upload:") {
				upload = strings.TrimPrefix(line, "Upload: ")
			}
		}

		resultText = fmt.Sprintf("🚀 *Speed Test Results*\n\n"+
			"📡 Ping: `%s`\n"+
			"⬇️ Download: `%s`\n"+
			"⬆️ Upload: `%s`",
			ping, download, upload)
	}

	edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID, resultText)
	edit.ParseMode = "Markdown"
	if sent.MessageID != 0 {
		if _, err := bot.Send(edit); err != nil {
			slog.Error("Failed to edit speedtest message", "err", err)
			safeSend(bot, tgbotapi.NewMessage(chatID, resultText))
		}
	} else {
		safeSend(bot, tgbotapi.NewMessage(chatID, resultText))
	}
}

// ═══════════════════════════════════════════════════════════════════
//  POWER MANAGEMENT
// ═══════════════════════════════════════════════════════════════════

func getPowerMenuText(_ *AppContext) (string, *tgbotapi.InlineKeyboardMarkup) {
	text := "⚡ *Power Management*\n\nBe careful, these actions affect the physical system."
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Reboot NAS", "pre_confirm_reboot"),
			tgbotapi.NewInlineKeyboardButtonData("🛑 Shutdown NAS", "pre_confirm_shutdown"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💥 Force Reboot", "force_reboot_now"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "back_main"),
		),
	)
	return text, &kb
}

func askPowerConfirmation(ctx *AppContext, bot BotAPI, chatID int64, msgID int, action string) {
	ctx.Bot.SetPendingAction(action)

	question := "🔄 *Reboot* the NAS?"
	if action == "shutdown" {
		question = "⚠️ *Shut down* the NAS?"
	}
	question += "\n\n_Are you sure?_"

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, do it", "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_power"),
		),
	)

	if msgID > 0 {
		editMessage(bot, chatID, msgID, question, &kb)
	} else {
		msg := tgbotapi.NewMessage(chatID, question)
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = kb
		safeSend(bot, msg)
	}
}

func handlePowerConfirm(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	action := ctx.Bot.GetPendingAction()
	ctx.Bot.ClearPendingAction()

	expectedAction := strings.TrimPrefix(data, "confirm_")
	if action == "" || action != expectedAction {
		editMessage(bot, chatID, msgID, "_Session expired — try again_", nil)
		return
	}

	cmd := "reboot"
	actionMsg := "Rebooting now..."
	if action == "shutdown" {
		cmd = "poweroff"
		actionMsg = "Shutting down... See you later!"
	}

	editMessage(bot, chatID, msgID, actionMsg, nil)

	go func() {
		time.Sleep(1 * time.Second)
		if err := runCommand(context.Background(), cmd); err != nil {
			slog.Error("Power command failed", "cmd", cmd, "err", err)
		}
	}()
}

func executeForcedReboot(ctx *AppContext, bot BotAPI, chatID int64, msgID int, reason string) {
	ctx.State.AddEvent("action", "Forced reboot triggered: "+reason)

	if msgID > 0 {
		editMessage(bot, chatID, msgID, ctx.Tr("force_reboot_triggered"), nil)
	} else {
		msg := tgbotapi.NewMessage(chatID, ctx.Tr("force_reboot_triggered"))
		msg.ParseMode = "Markdown"
		safeSend(bot, msg)
	}

	go func() {
		time.Sleep(1 * time.Second)
		slog.Warn("Executing forced reboot", "reason", reason)
		if err := runCommand(context.Background(), "reboot", "-f"); err != nil {
			slog.Error("Forced reboot command failed", "err", err)
		}
	}()
}

// ═══════════════════════════════════════════════════════════════════
//  MESSAGE HELPERS
// ═══════════════════════════════════════════════════════════════════

func sendMarkdown(bot BotAPI, chatID int64, text string) {
	if bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := bot.Send(msg); err != nil {
		slog.Error("Error sending Markdown message. Retrying as plain text", "err", err)
		msg.ParseMode = ""
		safeSend(bot, msg)
	}
}

func sendWithKeyboard(ctx *AppContext, bot BotAPI, chatID int64, text string) {
	if bot == nil {
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	// Ensure getMainKeyboard is updated to take ctx if we translate it
	msg.ReplyMarkup = getMainKeyboard(ctx)
	if _, err := bot.Send(msg); err != nil {
		slog.Error("Error sending Markdown message with keyboard. Retrying as plain text", "err", err)
		msg.ParseMode = ""
		safeSend(bot, msg)
	}
}

func editMessage(bot BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	if bot == nil {
		return
	}
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "Markdown"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard
	}
	if _, err := bot.Send(edit); err != nil {
		slog.Error("Error editing message to Markdown. Retrying as plain text", "err", err)
		edit.ParseMode = ""
		safeSend(bot, edit)
	}
}

func getMainKeyboard(_ *AppContext) tgbotapi.InlineKeyboardMarkup {
	// Future: use ctx.Tr for button labels
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData("🌡 Temp", "show_temp"),
			tgbotapi.NewInlineKeyboardButtonData("🌐 Net", "show_net"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Docker", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("📊 D-Stats", "show_dstats"),
			tgbotapi.NewInlineKeyboardButtonData("🔥 Top Proc", "show_top"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Power Actions", "show_power"),
		),
	)
}
