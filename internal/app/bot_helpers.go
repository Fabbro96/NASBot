package app

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

func handleSpeedtest(ctx *AppContext, bot BotAPI, chatID int64) {
	if !commandExists("speedtest-cli") {
		sendMarkdown(bot, chatID, ctx.Tr("speedtest_not_installed"))
		return
	}

	msg := tgbotapi.NewMessage(chatID, ctx.Tr("speedtest_running"))
	sent, sendErr := bot.Send(msg)
	if sendErr != nil {
		slog.Error("Failed to send speedtest start message", "err", sanitizeErr(sendErr))
	}

	c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	output, err := runCommandOutput(c, "speedtest-cli", "--simple")

	var resultText string
	if err != nil {
		if c.Err() == context.DeadlineExceeded {
			resultText = "⏱ Speed test timed out"
		} else {
			resultText = fmt.Sprintf("❌ Speed test failed:\n`%s`", sanitizeErr(err))
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

	// The placeholder message must exist before it can be edited: the check on
	// sent.MessageID comes before the edit is built, where it used to come
	// after, so the edit config was built for a message that did not exist.
	if sendErr != nil || sent.MessageID == 0 {
		safeSend(bot, newMarkdownMessage(chatID, resultText))
		return
	}
	if err := sendWithParseFallback(bot, newMarkdownEdit(chatID, sent.MessageID, resultText, nil)); err != nil {
		slog.Error("Failed to edit speedtest message", "err", sanitizeErr(err))
		safeSend(bot, newMarkdownMessage(chatID, resultText))
	}
}

// newMarkdownMessage builds a Markdown message.
func newMarkdownMessage(chatID int64, text string) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	return msg
}

// newMarkdownEdit builds a Markdown edit of an existing message.
func newMarkdownEdit(chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) tgbotapi.EditMessageTextConfig {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "Markdown"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard
	}
	return edit
}

// ═══════════════════════════════════════════════════════════════════
//  POWER MANAGEMENT
// ═══════════════════════════════════════════════════════════════════

func getPowerMenuText(ctx *AppContext) (string, *tgbotapi.InlineKeyboardMarkup) {
	text := ctx.Tr("power_title")
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("power_reboot"), "pre_confirm_reboot"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("power_shutdown"), "pre_confirm_shutdown"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("power_force_reboot"), "force_reboot_now"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "back_main"),
		),
	)
	return text, &kb
}

func askPowerConfirmation(ctx *AppContext, bot BotAPI, chatID int64, msgID int, action string) {
	ctx.Bot.SetPendingAction(action)

	question := ctx.Tr("power_confirm_reboot")
	if action == "shutdown" {
		question = ctx.Tr("power_confirm_shutdown")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_confirm_power"), "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("cancel"), "cancel_power"),
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
		editMessage(bot, chatID, msgID, ctx.Tr("session_expired"), nil)
		return
	}

	cmd := "reboot"
	actionMsg := ctx.Tr("power_rebooting")
	if action == "shutdown" {
		cmd = "poweroff"
		actionMsg = ctx.Tr("power_shutting_down")
	}

	addPowerLifecycleEvent(ctx, action, false, "command", cmd, "user-confirmation")
	saveState(ctx)

	editMessage(bot, chatID, msgID, actionMsg, nil)

	goSafe("power-confirm", func() {
		time.Sleep(1 * time.Second)
		if err := executeSystemPowerCommand(cmd); err != nil {
			slog.Error("Power command failed", "cmd", cmd, "err", err)
		}
	})
}

func executeForcedReboot(ctx *AppContext, bot BotAPI, chatID int64, msgID int, reason string) {
	source := powerSourceFromReason(reason)
	addPowerLifecycleEvent(ctx, "reboot", true, source, "reboot", reason)
	saveState(ctx)

	if msgID > 0 {
		editMessage(bot, chatID, msgID, ctx.Tr("force_reboot_triggered"), nil)
	} else {
		msg := tgbotapi.NewMessage(chatID, ctx.Tr("force_reboot_triggered"))
		msg.ParseMode = "Markdown"
		safeSend(bot, msg)
	}

	goSafe("force-reboot-execution", func() {
		time.Sleep(1 * time.Second)
		slog.Warn("Executing reboot", "reason", reason)
		if err := executeSystemPowerCommand("reboot"); err != nil {
			slog.Error("Reboot command failed", "err", err)
		}
	})
}

func executeSystemPowerCommand(cmd string) error {
	ctx := context.Background()
	if commandExists("nsenter") {
		if err := runCommand(ctx, "nsenter", "-t", "1", "-m", "-u", "-i", "-n", "-p", "--", cmd); err == nil {
			return nil
		}
	}
	if commandExists("systemctl") {
		if err := runCommand(ctx, "systemctl", cmd); err == nil {
			return nil
		}
	}
	return runCommand(ctx, cmd)
}

// ═══════════════════════════════════════════════════════════════════
//  MESSAGE HELPERS
// ═══════════════════════════════════════════════════════════════════

// sendMarkdown sends text, splitting it when it exceeds the Telegram limit.
func sendMarkdown(bot BotAPI, chatID int64, text string) {
	if bot == nil {
		return
	}
	for _, chunk := range splitTextChunks(text, telegramMaxTextRunes) {
		safeSend(bot, newMarkdownMessage(chatID, chunk))
	}
}

func sendWithKeyboard(ctx *AppContext, bot BotAPI, chatID int64, text string) {
	if bot == nil {
		return
	}
	kb := getMainKeyboard(ctx)
	for _, chunk := range splitTextChunks(text, telegramMaxTextRunes) {
		msg := newMarkdownMessage(chatID, chunk)
		msg.ReplyMarkup = kb
		safeSend(bot, msg)
	}
}

// editMessage rewrites an existing message in place.
//
// The text is split at the Telegram limit: the first chunk is the edit, the
// remaining ones have to be new messages, because editing the same message twice
// would overwrite the first chunk. When the edit cannot be delivered at all
// (message too old, deleted, or a markup error that survived the plain retry)
// the text goes out as a new message instead of being lost.
func editMessage(bot BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	if bot == nil {
		return
	}
	chunks := splitTextChunks(text, telegramMaxTextRunes)
	for i, chunk := range chunks {
		// The keyboard belongs to the last chunk: it is the final state of the
		// conversation, and repeating it on every chunk would make the message
		// grow buttons while being read.
		kb := keyboard
		if i < len(chunks)-1 {
			kb = nil
		}
		if i == 0 && msgID > 0 {
			err := sendWithParseFallback(bot, newMarkdownEdit(chatID, msgID, chunk, kb))
			if err == nil {
				continue
			}
			slog.Error("Error editing message, falling back to a new message", "msg_id", msgID, "err", sanitizeErr(err))
		}
		msg := newMarkdownMessage(chatID, chunk)
		if kb != nil {
			msg.ReplyMarkup = *kb
		}
		safeSend(bot, msg)
	}
}

func getMainKeyboard(ctx *AppContext) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_refresh"), "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_temp"), "show_temp"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_net"), "show_net"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_docker"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_dstats"), "show_dstats"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_top"), "show_top"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_power"), "show_power"),
		),
	)
}
