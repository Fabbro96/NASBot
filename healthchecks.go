package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MaxDowntimeEvents is the max number of downtime events to keep in history
const MaxDowntimeEvents = 50

// ═══════════════════════════════════════════════════════════════════
//  HEALTHCHECKS.IO INTEGRATION
// ═══════════════════════════════════════════════════════════════════

// startHealthchecksPinger starts the background goroutine that pings healthchecks.io
func startHealthchecksPinger(bot *tgbotapi.BotAPI) {
	if !cfg.Healthchecks.Enabled || cfg.Healthchecks.PingURL == "" {
		log.Println("[i] Healthchecks.io disabled or no URL configured")
		return
	}

	period := cfg.Healthchecks.PeriodSeconds
	if period <= 0 {
		period = 60 // default 1 minute
	}

	log.Printf("[+] Healthchecks.io pinger started (every %ds)", period)

	ticker := time.NewTicker(time.Duration(period) * time.Second)
	defer ticker.Stop()

	// Initial ping on startup
	pingHealthchecks(bot)

	for range ticker.C {
		pingHealthchecks(bot)
	}
}

// pingHealthchecks sends a ping to healthchecks.io
func pingHealthchecks(bot *tgbotapi.BotAPI) {
	if cfg.Healthchecks.PingURL == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.Healthchecks.PingURL, nil)
	if err != nil {
		recordHealthcheckFailure(bot, fmt.Sprintf("request error: %v", err))
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		recordHealthcheckFailure(bot, fmt.Sprintf("network error: %v", err))
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		recordHealthcheckSuccess(bot)
	} else {
		recordHealthcheckFailure(bot, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}
}

// recordHealthcheckSuccess records a successful ping
func recordHealthcheckSuccess(bot *tgbotapi.BotAPI) {
	healthchecksMutex.Lock()
	defer healthchecksMutex.Unlock()

	healthchecksState.TotalPings++
	healthchecksState.SuccessfulPings++
	healthchecksState.LastPingTime = time.Now()
	healthchecksState.LastPingSuccess = true

	// If we were in downtime, close the event and notify recovery
	if healthchecksInDowntime && len(healthchecksState.DowntimeEvents) > 0 {
		lastIdx := len(healthchecksState.DowntimeEvents) - 1
		event := &healthchecksState.DowntimeEvents[lastIdx]
		event.EndTime = time.Now()
		downtimeDuration := event.EndTime.Sub(event.StartTime)
		event.Duration = formatDuration(downtimeDuration)
		healthchecksInDowntime = false
		log.Printf("[+] Healthchecks: downtime ended, duration: %s", event.Duration)

		// Send recovery notification
		if bot != nil && !isQuietHours() {
			period := cfg.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			periodStr := formatPeriod(period)

			msg := fmt.Sprintf("🟢 *Healthchecks UP*\n\n"+
				"_The check is now receiving pings normally._\n\n"+
				"⏱ Downtime: `%s`\n"+
				"📊 Total Pings: `%d`\n"+
				"🔄 Period: `%s`",
				event.Duration,
				healthchecksState.TotalPings,
				periodStr)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		addReportEvent("info", fmt.Sprintf("🟢 Healthchecks recovered (down for %s)", event.Duration))
	}

	// Save state periodically (every 10 pings)
	if healthchecksState.TotalPings%10 == 0 {
		go saveState()
	}
}

// recordHealthcheckFailure records a failed ping
func recordHealthcheckFailure(bot *tgbotapi.BotAPI, reason string) {
	healthchecksMutex.Lock()
	defer healthchecksMutex.Unlock()

	healthchecksState.TotalPings++
	healthchecksState.FailedPings++
	healthchecksState.LastPingTime = time.Now()
	healthchecksState.LastPingSuccess = false
	healthchecksState.LastFailure = time.Now()

	log.Printf("[!] Healthchecks ping failed: %s", reason)

	// Start a new downtime event if we weren't already in one
	if !healthchecksInDowntime {
		healthchecksInDowntime = true
		event := DowntimeLog{
			StartTime: time.Now(),
			Reason:    reason,
		}
		healthchecksState.DowntimeEvents = append(healthchecksState.DowntimeEvents, event)

		// Keep only the last N events
		if len(healthchecksState.DowntimeEvents) > MaxDowntimeEvents {
			healthchecksState.DowntimeEvents = healthchecksState.DowntimeEvents[len(healthchecksState.DowntimeEvents)-MaxDowntimeEvents:]
		}

		// Notify user (respecting quiet hours)
		if bot != nil && !isQuietHours() {
			period := cfg.Healthchecks.PeriodSeconds
			if period <= 0 {
				period = 60
			}
			periodStr := formatPeriod(period)

			// Calculate last successful ping time
			lastPingAgo := "N/A"
			if !healthchecksState.LastPingTime.IsZero() {
				lastPingAgo = formatDuration(time.Since(healthchecksState.LastPingTime))
			}

			msg := fmt.Sprintf("🔴 *Healthchecks DOWN*\n\n"+
				"_Ping failed: `%s`_\n\n"+
				"🔄 Period: `%s`\n"+
				"📊 Total Pings: `%d`\n"+
				"⏱ Last Success: `%s ago`\n\n"+
				"_Monitoring for recovery..._",
				reason,
				periodStr,
				healthchecksState.TotalPings,
				lastPingAgo)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		addReportEvent("warning", fmt.Sprintf("🔴 Healthchecks down: %s", reason))
	}

	go saveState()
}

// getHealthchecksStats returns formatted stats for the /health command
func getHealthchecksStats() string {
	healthchecksMutex.Lock()
	defer healthchecksMutex.Unlock()

	if !cfg.Healthchecks.Enabled {
		return tr("health_disabled")
	}

	if cfg.Healthchecks.PingURL == "" {
		return tr("health_no_url")
	}

	var sb strings.Builder
	sb.WriteString(tr("health_title"))

	// Current status
	if healthchecksState.LastPingSuccess {
		sb.WriteString("✅ " + tr("health_status_ok") + "\n\n")
	} else {
		sb.WriteString("❌ " + tr("health_status_fail") + "\n\n")
	}

	// Stats
	total := healthchecksState.TotalPings
	success := healthchecksState.SuccessfulPings
	failed := healthchecksState.FailedPings

	if total > 0 {
		successRate := float64(success) / float64(total) * 100
		sb.WriteString(fmt.Sprintf(tr("health_stats_fmt"), total, success, failed, successRate))
	} else {
		sb.WriteString(tr("health_no_data"))
	}

	// Last ping
	if !healthchecksState.LastPingTime.IsZero() {
		ago := time.Since(healthchecksState.LastPingTime)
		sb.WriteString(fmt.Sprintf(tr("health_last_ping"), formatDuration(ago)))
	}

	// Configuration
	period := cfg.Healthchecks.PeriodSeconds
	if period <= 0 {
		period = 60
	}
	grace := cfg.Healthchecks.GraceSeconds
	if grace <= 0 {
		grace = 60
	}
	sb.WriteString(fmt.Sprintf(tr("health_config_fmt"), period, grace))

	// Recent downtime events
	if len(healthchecksState.DowntimeEvents) > 0 {
		sb.WriteString("\n" + tr("health_downtime_title"))
		// Show last 5 events
		start := len(healthchecksState.DowntimeEvents) - 5
		if start < 0 {
			start = 0
		}
		for i := len(healthchecksState.DowntimeEvents) - 1; i >= start; i-- {
			event := healthchecksState.DowntimeEvents[i]
			startStr := event.StartTime.In(location).Format("02/01 15:04")
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
func getHealthchecksAISummary() string {
	healthchecksMutex.Lock()
	defer healthchecksMutex.Unlock()

	if len(healthchecksState.DowntimeEvents) == 0 {
		return tr("health_ai_no_data")
	}

	// Build context for AI
	var sb strings.Builder
	sb.WriteString("Healthchecks.io monitoring data:\n")
	sb.WriteString(fmt.Sprintf("- Total pings: %d\n", healthchecksState.TotalPings))
	sb.WriteString(fmt.Sprintf("- Successful: %d\n", healthchecksState.SuccessfulPings))
	sb.WriteString(fmt.Sprintf("- Failed: %d\n", healthchecksState.FailedPings))
	sb.WriteString(fmt.Sprintf("- Success rate: %.1f%%\n", float64(healthchecksState.SuccessfulPings)/float64(maxInt(healthchecksState.TotalPings, 1))*100))
	sb.WriteString("\nDowntime events:\n")

	for _, event := range healthchecksState.DowntimeEvents {
		startStr := event.StartTime.In(location).Format("2006-01-02 15:04")
		if event.EndTime.IsZero() {
			sb.WriteString(fmt.Sprintf("- %s: %s (ongoing)\n", startStr, event.Reason))
		} else {
			sb.WriteString(fmt.Sprintf("- %s: %s, duration %s\n", startStr, event.Reason, event.Duration))
		}
	}

	return sb.String()
}

// handleHealthCommand handles the /health command
func handleHealthCommand(bot *tgbotapi.BotAPI, chatID int64) {
	stats := getHealthchecksStats()

	// Create inline keyboard
	var keyboard tgbotapi.InlineKeyboardMarkup
	if cfg.Healthchecks.Enabled && cfg.GeminiAPIKey != "" && len(healthchecksState.DowntimeEvents) > 0 {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤖 "+tr("health_ai_analyze"), "health_ai"),
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+tr("health_refresh"), "health_refresh"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🧹 "+tr("health_clear_history"), "health_clear"),
			),
		)
	} else if cfg.Healthchecks.Enabled {
		keyboard = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+tr("health_refresh"), "health_refresh"),
			),
		)
	}

	msg := tgbotapi.NewMessage(chatID, stats)
	msg.ParseMode = "Markdown"
	if cfg.Healthchecks.Enabled {
		msg.ReplyMarkup = keyboard
	}
	bot.Send(msg)
}

// handleHealthCallback handles callback queries for health buttons
func handleHealthCallback(bot *tgbotapi.BotAPI, callback *tgbotapi.CallbackQuery, action string) {
	switch action {
	case "health_refresh":
		stats := getHealthchecksStats()
		edit := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, stats)
		edit.ParseMode = "Markdown"

		var keyboard tgbotapi.InlineKeyboardMarkup
		if cfg.GeminiAPIKey != "" && len(healthchecksState.DowntimeEvents) > 0 {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🤖 "+tr("health_ai_analyze"), "health_ai"),
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+tr("health_refresh"), "health_refresh"),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🧹 "+tr("health_clear_history"), "health_clear"),
				),
			)
		} else {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔄 "+tr("health_refresh"), "health_refresh"),
				),
			)
		}
		edit.ReplyMarkup = &keyboard
		bot.Send(edit)

	case "health_ai":
		if cfg.GeminiAPIKey == "" {
			callback := tgbotapi.NewCallback(callback.ID, tr("health_no_gemini"))
			bot.Send(callback)
			return
		}

		// Show loading
		loadingMsg := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, "⏳ "+tr("health_analyzing"))
		bot.Send(loadingMsg)

		// Get AI analysis
		context := getHealthchecksAISummary()
		prompt := fmt.Sprintf(tr("health_ai_prompt"), context)

		analysis, err := callGeminiWithFallback(prompt, func(model string) {
			// Update the loading message with the current model
			newText := fmt.Sprintf("⏳ %s\n_(%s)_", tr("health_analyzing"), model)
			edit := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, newText)
			edit.ParseMode = "Markdown"
			bot.Send(edit)
		})
		if err != nil {
			log.Printf("[!] Healthchecks AI error: %v", err)
			edit := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, "❌ "+tr("health_ai_error"))
			bot.Send(edit)
			return
		}

		// Show analysis
		result := fmt.Sprintf("🤖 *%s*\n\n%s", tr("health_ai_title"), analysis)
		edit := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, result)
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+tr("back"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		bot.Send(edit)

	case "health_clear":
		healthchecksMutex.Lock()
		healthchecksState.DowntimeEvents = []DowntimeLog{}
		healthchecksState.TotalPings = 0
		healthchecksState.SuccessfulPings = 0
		healthchecksState.FailedPings = 0
		healthchecksMutex.Unlock()
		saveState()

		stats := getHealthchecksStats()
		edit := tgbotapi.NewEditMessageText(callback.Message.Chat.ID, callback.Message.MessageID, stats+"\n\n✅ "+tr("health_history_cleared"))
		edit.ParseMode = "Markdown"
		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+tr("health_refresh"), "health_refresh"),
			),
		)
		edit.ReplyMarkup = &keyboard
		bot.Send(edit)
	}

	// Answer callback to remove loading indicator
	bot.Send(tgbotapi.NewCallback(callback.ID, ""))
}

// pingHealthchecksStart sends a /start signal to healthchecks.io
func pingHealthchecksStart() {
	if !cfg.Healthchecks.Enabled || cfg.Healthchecks.PingURL == "" {
		return
	}

	url := cfg.Healthchecks.PingURL + "/start"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
	log.Println("[+] Healthchecks.io /start signal sent")
}

// maxInt returns the larger of two integers
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
