package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MaxDowntimeEvents is the max number of downtime events to keep in history
const MaxDowntimeEvents = 50

// ═══════════════════════════════════════════════════════════════════
//  HEALTHCHECKS.IO INTEGRATION
// ═══════════════════════════════════════════════════════════════════

// startHealthchecksPinger starts the background goroutine that pings healthchecks.io
func startHealthchecksPinger(ctx *AppContext, bot BotAPI, runCtx ...context.Context) {
	if !ctx.Config.Healthchecks.Enabled || ctx.Config.Healthchecks.PingURL == "" {
		slog.Info("Healthchecks.io disabled or no URL configured")
		return
	}
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}

	period := ctx.Config.Healthchecks.PeriodSeconds
	if period <= 0 {
		period = 60 // default 1 minute
	}

	slog.Info("Healthchecks.io pinger started", "period_sec", period)

	ticker := time.NewTicker(time.Duration(period) * time.Second)
	defer ticker.Stop()

	// Initial ping on startup
	pingHealthchecks(ctx, bot)

	for {
		select {
		case <-rc.Done():
			return
		case <-ticker.C:
			pingHealthchecks(ctx, bot)
		}
	}
}

// pingHealthchecks sends a ping to healthchecks.io
func pingHealthchecks(appCtx *AppContext, bot BotAPI) {
	if appCtx.Config.Healthchecks.PingURL == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appCtx.Config.Healthchecks.PingURL, nil)
	if err != nil {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("request error: %v", err))
		return
	}

	if appCtx.HTTP == nil {
		appCtx.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := appCtx.HTTP.Do(req)
	if err != nil {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("network error: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		recordHealthcheckSuccess(appCtx, bot)
	} else {
		recordHealthcheckFailure(appCtx, bot, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

// recordHealthcheckSuccess records a successful ping
func recordHealthcheckSuccess(ctx *AppContext, bot BotAPI) {
	var (
		shouldNotify bool
		downtimeStr  string
		totalPings   int
	)

	ctx.Monitor.mu.Lock()
	ctx.Monitor.Healthchecks.TotalPings++
	ctx.Monitor.Healthchecks.SuccessfulPings++
	ctx.Monitor.Healthchecks.LastPingTime = time.Now()
	ctx.Monitor.Healthchecks.LastPingSuccess = true

	// If we were in downtime, close the event and notify recovery
	if ctx.Monitor.HealthInDowntime && len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
		lastIdx := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 1
		event := &ctx.Monitor.Healthchecks.DowntimeEvents[lastIdx]
		event.EndTime = time.Now()
		downtimeDuration := event.EndTime.Sub(event.StartTime)
		event.Duration = format.FormatDuration(downtimeDuration)
		ctx.Monitor.HealthInDowntime = false

		slog.Info("Healthchecks: downtime ended", "duration", event.Duration)
		shouldNotify = true
		downtimeStr = event.Duration
	}

	totalPings = ctx.Monitor.Healthchecks.TotalPings
	ctx.Monitor.mu.Unlock()

	// Send recovery notification
	if shouldNotify {
		if bot != nil && !ctx.IsQuietHours() {
			period := ctx.Config.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			periodStr := format.FormatPeriod(period)

			msg := fmt.Sprintf("🟢 *Healthchecks UP*\n\n"+
				"_The check is now receiving pings normally._\n\n"+
				"⏱ Downtime: `%s`\n"+
				"📊 Total Pings: `%d`\n"+
				"🔄 Period: `%s`",
				downtimeStr,
				totalPings,
				periodStr)
			m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.State.AddReportEvent("info", fmt.Sprintf("🟢 Healthchecks recovered (down for %s)", downtimeStr))
	}

	// Save state periodically (every 10 pings)
	if totalPings%10 == 0 {
		go saveState(ctx)
	}
}

// recordHealthcheckFailure records a failed ping
func recordHealthcheckFailure(ctx *AppContext, bot BotAPI, reason string) {
	ctx.Monitor.mu.Lock()

	ctx.Monitor.Healthchecks.TotalPings++
	ctx.Monitor.Healthchecks.FailedPings++
	ctx.Monitor.Healthchecks.LastPingTime = time.Now()
	ctx.Monitor.Healthchecks.LastPingSuccess = false
	ctx.Monitor.Healthchecks.LastFailure = time.Now()

	slog.Error("Healthchecks ping failed", "reason", reason)

	// Start a new downtime event if we weren't already in one
	if !ctx.Monitor.HealthInDowntime {
		ctx.Monitor.HealthInDowntime = true
		event := DowntimeLog{
			StartTime: time.Now(),
			Reason:    reason,
		}
		ctx.Monitor.Healthchecks.DowntimeEvents = append(ctx.Monitor.Healthchecks.DowntimeEvents, event)

		// Keep only the last N events
		if len(ctx.Monitor.Healthchecks.DowntimeEvents) > MaxDowntimeEvents {
			ctx.Monitor.Healthchecks.DowntimeEvents = ctx.Monitor.Healthchecks.DowntimeEvents[len(ctx.Monitor.Healthchecks.DowntimeEvents)-MaxDowntimeEvents:]
		}

		lastPingTime := ctx.Monitor.Healthchecks.LastPingTime
		totalPings := ctx.Monitor.Healthchecks.TotalPings

		ctx.Monitor.mu.Unlock() // Unlock before sending message to avoid deadlock if network is slow (though Send is usually fastish, but best practice)

		// Notify user (respecting quiet hours)
		if bot != nil && !ctx.IsQuietHours() {
			period := ctx.Config.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			periodStr := format.FormatPeriod(period)

			// Calculate last successful ping time
			lastPingAgo := "N/A"
			if !lastPingTime.IsZero() {
				lastPingAgo = format.FormatDuration(time.Since(lastPingTime))
			}

			msg := fmt.Sprintf("🔴 *Healthchecks DOWN*\n\n"+
				"_Ping failed: `%s`_\n\n"+
				"🔄 Period: `%s`\n"+
				"📊 Total Pings: `%d`\n"+
				"⏱ Last Success: `%s ago`\n\n"+
				"_Monitoring for recovery..._",
				reason,
				periodStr,
				totalPings,
				lastPingAgo)
			m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.State.AddReportEvent("warning", fmt.Sprintf("🔴 Healthchecks down: %s", reason))
	} else {
		ctx.Monitor.mu.Unlock()
	}

	go saveState(ctx)
}

// getHealthchecksStats returns formatted stats for the /health command
func getHealthchecksStats(ctx *AppContext) string {
	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	if !ctx.Config.Healthchecks.Enabled {
		return ctx.Tr("health_disabled")
	}

	if ctx.Config.Healthchecks.PingURL == "" {
		return ctx.Tr("health_no_url")
	}

	var sb strings.Builder
	sb.WriteString(ctx.Tr("health_title"))

	// Current status
	if ctx.Monitor.Healthchecks.LastPingSuccess {
		sb.WriteString("✅ " + ctx.Tr("health_status_ok") + "\n\n")
	} else {
		sb.WriteString("❌ " + ctx.Tr("health_status_fail") + "\n\n")
	}

	// Stats
	total := ctx.Monitor.Healthchecks.TotalPings
	success := ctx.Monitor.Healthchecks.SuccessfulPings
	failed := ctx.Monitor.Healthchecks.FailedPings

	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		sb.WriteString(fmt.Sprintf(ctx.Tr("health_stats_fmt"), total, success, failed, successRate))
	} else {
		sb.WriteString(ctx.Tr("health_no_data"))
	}

	// Last ping
	if !ctx.Monitor.Healthchecks.LastPingTime.IsZero() {
		ago := time.Since(ctx.Monitor.Healthchecks.LastPingTime)
		sb.WriteString(fmt.Sprintf(ctx.Tr("health_last_ping"), format.FormatDuration(ago)))
	}

	// Configuration
	period := ctx.Config.Healthchecks.PeriodSeconds
	if period <= 0 {
		period = 60
	}
	grace := ctx.Config.Healthchecks.GraceSeconds
	if grace <= 0 {
		grace = 60
	}
	sb.WriteString(fmt.Sprintf(ctx.Tr("health_config_fmt"), period, grace))

	// Recent downtime events
	if len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
		sb.WriteString("\n" + ctx.Tr("health_downtime_title"))
		// Show last 5 events
		start := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 5
		if start < 0 {
			start = 0
		}
		for i := len(ctx.Monitor.Healthchecks.DowntimeEvents) - 1; i >= start; i-- {
			event := ctx.Monitor.Healthchecks.DowntimeEvents[i]
			startStr := event.StartTime.In(ctx.State.TimeLocation).Format("02/01 15:04")
			if event.EndTime.IsZero() {
				sb.WriteString(fmt.Sprintf("• `%s` — _%s_ (ongoing)\n", startStr, event.Reason))
			} else {
				sb.WriteString(fmt.Sprintf("• `%s` — %s (%s)\n", startStr, event.Duration, event.Reason))
			}
		}
	}

	return sb.String()
}

// getHealthchecksAISummary generates an AI summary of downtime patterns
func getHealthchecksAISummary(ctx *AppContext) string {
	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	if len(ctx.Monitor.Healthchecks.DowntimeEvents) == 0 {
		return ctx.Tr("health_ai_no_data")
	}

	// Build context for AI
	var sb strings.Builder
	sb.WriteString("Healthchecks.io monitoring data:\n")
	sb.WriteString(fmt.Sprintf("- Total pings: %d\n", ctx.Monitor.Healthchecks.TotalPings))
	sb.WriteString(fmt.Sprintf("- Successful: %d\n", ctx.Monitor.Healthchecks.SuccessfulPings))
	sb.WriteString(fmt.Sprintf("- Failed: %d\n", ctx.Monitor.Healthchecks.FailedPings))
	sb.WriteString(fmt.Sprintf("- Success rate: %.1f%%\n", float64(ctx.Monitor.Healthchecks.SuccessfulPings)/float64(maxInt(ctx.Monitor.Healthchecks.TotalPings, 1))*100))
	sb.WriteString("\nDowntime events:\n")

	for _, event := range ctx.Monitor.Healthchecks.DowntimeEvents {
		startStr := event.StartTime.In(ctx.State.TimeLocation).Format("2006-01-02 15:04")
		if event.EndTime.IsZero() {
			sb.WriteString(fmt.Sprintf("- %s: %s (ongoing)\n", startStr, event.Reason))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s, duration %s\n", startStr, event.Reason, event.Duration))
		}
	}

	return sb.String()
}

// handleHealthCommand handles the /health command
func handleHealthCommand(ctx *AppContext, bot BotAPI, chatID int64) {
	stats := getHealthchecksStats(ctx)

	// Create inline keyboard
	var keyboard tgbotapi.InlineKeyboardMarkup

	ctx.Monitor.mu.Lock()
	hasEvents := len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0
	ctx.Monitor.mu.Unlock()

	if ctx.Config.Healthchecks.Enabled && ctx.Config.GeminiAPIKey != "" && hasEvents {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("health_ai_analyze"), "health_ai"),
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🧹 "+ctx.Tr("health_clear_history"), "health_clear"),
			),
		)
	} else if ctx.Config.Healthchecks.Enabled {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
		)
	}

	msg := tgbotapi.NewMessage(chatID, stats)
	msg.ParseMode = "Markdown"
	if ctx.Config.Healthchecks.Enabled {
		msg.ReplyMarkup = keyboard
	}
	safeSend(bot, msg)
}

// handleHealthCallback handles callback queries for health buttons
func handleHealthCallback(ctx *AppContext, bot BotAPI, query *tgbotapi.CallbackQuery, action string) {
	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID

	switch action {
	case "health_refresh":
		stats := getHealthchecksStats(ctx)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, stats)
		edit.ParseMode = "Markdown"

		ctx.Monitor.mu.Lock()
		hasEvents := len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0
		ctx.Monitor.mu.Unlock()

		var keyboard tgbotapi.InlineKeyboardMarkup
		if ctx.Config.GeminiAPIKey != "" && hasEvents {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("health_ai_analyze"), "health_ai"),
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🧹 "+ctx.Tr("health_clear_history"), "health_clear"),
				),
			)
		} else {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
				),
			)
		}
		edit.ReplyMarkup = &keyboard
		safeSend(bot, edit)

	case "health_ai":
		if ctx.Config.GeminiAPIKey == "" {
			cb := tgbotapi.NewCallback(query.ID, ctx.Tr("health_no_gemini"))
			safeSend(bot, cb)
			return
		}

		// Show loading
		modelName := "gemini-2.5-flash"
		loadingText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("health_analyzing"), modelName)
		loadingMsg := tgbotapi.NewEditMessageText(chatID, msgID, loadingText)
		loadingMsg.ParseMode = "Markdown"
		safeSend(bot, loadingMsg)

		// Get AI analysis
		aiContext := getHealthchecksAISummary(ctx)
		prompt := fmt.Sprintf(ctx.Tr("health_ai_prompt"), aiContext)

		analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
			// Update the loading message with the current model
			newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("health_analyzing"), model)
			edit := tgbotapi.NewEditMessageText(chatID, msgID, newText)
			edit.ParseMode = "Markdown"
			safeSend(bot, edit)
		})
		if err != nil {
			slog.Error("Healthchecks AI error", "err", err)
			errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("health_ai_error"), err)
			edit := tgbotapi.NewEditMessageText(chatID, msgID, errText)
			edit.ParseMode = "Markdown"
			keyboard := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_ai_analyze"), "health_ai"),
					tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "health_refresh"),
				),
			)
			edit.ReplyMarkup = &keyboard
			if _, sendErr := bot.Send(edit); sendErr != nil {
				edit.ParseMode = ""
				safeSend(bot, edit)
			}
			return
		}

		// Show analysis
		result := fmt.Sprintf("🤖 *%s*\n\n%s", ctx.Tr("health_ai_title"), analysis)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, result)
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		if _, sendErr := bot.Send(edit); sendErr != nil {
			slog.Error("Error sending AI analysis (Markdown)", "err", sendErr)
			edit.ParseMode = ""
			safeSend(bot, edit)
		}

	case "health_clear":
		ctx.Monitor.mu.Lock()
		ctx.Monitor.Healthchecks.DowntimeEvents = []DowntimeLog{}
		ctx.Monitor.Healthchecks.TotalPings = 0
		ctx.Monitor.Healthchecks.SuccessfulPings = 0
		ctx.Monitor.Healthchecks.FailedPings = 0
		ctx.Monitor.mu.Unlock()
		saveState(ctx)

		stats := getHealthchecksStats(ctx)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, stats+"\n\n✅ "+ctx.Tr("health_history_cleared"))
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("health_refresh"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		safeSend(bot, edit)
	}

	// Answer callback to remove loading indicator
	safeSend(bot, tgbotapi.NewCallback(query.ID, ""))
}

// pingHealthchecksStart sends a /start signal to healthchecks.io
func pingHealthchecksStart(ctx *AppContext) {
	if !ctx.Config.Healthchecks.Enabled || ctx.Config.Healthchecks.PingURL == "" {
		return
	}

	url := ctx.Config.Healthchecks.PingURL + "/start"
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	if ctx.HTTP == nil {
		ctx.HTTP = &http.Client{Timeout: 10 * time.Second}
	}

	resp, err := ctx.HTTP.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
	slog.Info("Healthchecks.io /start signal sent")
}

// maxInt used to be here, now using utils.go version or removing dup
