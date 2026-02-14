package main

import (
	"fmt"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func checkResourceStress(ctx *AppContext, bot BotAPI, resource string, currentValue, threshold float64) {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	tracker := ctx.State.ResourceStress[resource]
	if tracker == nil {
		tracker = &StressTracker{}
		ctx.State.ResourceStress[resource] = tracker
	}

	isStressed := currentValue >= threshold
	stressDurationThreshold := time.Duration(ctx.Config.StressTracking.DurationThresholdMinutes) * time.Minute

	if isStressed {
		if tracker.CurrentStart.IsZero() {
			tracker.CurrentStart = time.Now()
			tracker.StressCount++
			tracker.Notified = false
		}

		stressDuration := time.Since(tracker.CurrentStart)
		if stressDuration >= stressDurationThreshold && !tracker.Notified && !ctx.IsQuietHours() {
			var emoji, unit string
			switch resource {
			case "HDD":
				emoji = "💾"
				unit = "I/O"
			case "SSD":
				emoji = "💿"
				unit = "Usage"
			case "CPU":
				emoji = "🧠"
				unit = "Usage"
			case "RAM":
				emoji = "💾"
				unit = "Usage"
			case "Swap":
				emoji = "🔄"
				unit = "Usage"
			}

			msg := fmt.Sprintf("%s *%s stress*\n\n%s: `%.0f%%` for `%s`\n\n_Watching..._", emoji, resource, unit, currentValue, stressDuration.Round(time.Second))
			m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)

			tracker.Notified = true
			ctx.State.AddEvent("warning", fmt.Sprintf("%s high (%.0f%%) for %s", resource, currentValue, stressDuration.Round(time.Second)))
		}
	} else {
		if !tracker.CurrentStart.IsZero() {
			stressDuration := time.Since(tracker.CurrentStart)
			tracker.TotalStress += stressDuration
			if stressDuration > tracker.LongestStress {
				tracker.LongestStress = stressDuration
			}

			if tracker.Notified && !ctx.IsQuietHours() {
				msg := fmt.Sprintf("✅ *%s back to normal* after `%s`", resource, stressDuration.Round(time.Second))
				m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
				ctx.State.AddEvent("info", fmt.Sprintf("%s normalized after %s", resource, stressDuration.Round(time.Second)))
			}

			tracker.CurrentStart = time.Time{}
			tracker.Notified = false
		}
	}
}

func getStressSummary(ctx *AppContext) string {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	var parts []string
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		tracker := ctx.State.ResourceStress[res]
		if tracker == nil || tracker.StressCount == 0 {
			continue
		}
		if tracker.LongestStress < 5*time.Minute {
			continue
		}

		entry := fmt.Sprintf("%s %dx", res, tracker.StressCount)
		if tracker.LongestStress > 0 {
			entry += fmt.Sprintf(" `%s`", format.FormatDuration(tracker.LongestStress))
		}
		parts = append(parts, entry)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

func resetStressCounters(ctx *AppContext) {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	for _, tracker := range ctx.State.ResourceStress {
		tracker.StressCount = 0
		tracker.LongestStress = 0
		tracker.TotalStress = 0
	}
}
