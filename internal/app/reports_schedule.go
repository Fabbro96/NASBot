package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// getNextReportTime calculates the next report time based on settings
func getNextReportTime(ctx *AppContext) (time.Time, TimePoint) {
	enabled, intervalDays, times := ctx.Settings.GetReportsSettings()
	loc := ctx.State.TimeLocation
	now := time.Now().In(loc)

	if !enabled || len(times) == 0 {
		return now.Add(24 * 365 * time.Hour), TimePoint{}
	}

	if intervalDays < 1 {
		intervalDays = 1
	}

	// Sort times to process chronologically
	sort.Slice(times, func(i, j int) bool {
		if times[i].Hour == times[j].Hour {
			return times[i].Minute < times[j].Minute
		}
		return times[i].Hour < times[j].Hour
	})

	ctx.State.Mu.Lock()
	lastReport := ctx.State.LastReport.In(loc)
	ctx.State.Mu.Unlock()

	lastReportDate := time.Date(lastReport.Year(), lastReport.Month(), lastReport.Day(), 0, 0, 0, 0, loc)
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	
	// Handle uninitialized lastReport
	if lastReport.IsZero() {
		lastReportDate = nowDate.AddDate(0, 0, -intervalDays)
	}

	daysSinceLast := int(nowDate.Sub(lastReportDate).Hours() / 24)
	if daysSinceLast < 0 {
		daysSinceLast = intervalDays // force recalculation for clock skew
	}

	const gracePeriod = 5 * time.Minute
	isReportDay := daysSinceLast >= intervalDays || daysSinceLast == 0

	if isReportDay {
		for _, tp := range times {
			reportTime := time.Date(now.Year(), now.Month(), now.Day(), tp.Hour, tp.Minute, 0, 0, loc)
			
			sameDay := lastReport.Year() == now.Year() &&
				lastReport.Month() == now.Month() &&
				lastReport.Day() == now.Day()
			
			alreadySent := sameDay && (lastReport.Hour() > tp.Hour || (lastReport.Hour() == tp.Hour && lastReport.Minute() >= tp.Minute))
			
			if alreadySent {
				continue
			}

			if now.After(reportTime) && now.Before(reportTime.Add(gracePeriod)) {
				slog.Info("Report: Missed report, triggering now (grace period)")
				return now, tp
			}
			
			if now.Before(reportTime) {
				return reportTime, tp
			}
		}
	}

	daysToAdd := 1
	if !isReportDay {
		daysToAdd = intervalDays - daysSinceLast
	} else if daysSinceLast == 0 {
		daysToAdd = intervalDays
	}

	nextDate := nowDate.AddDate(0, 0, daysToAdd)
	tp := times[0]
	nextReport := time.Date(nextDate.Year(), nextDate.Month(), nextDate.Day(), tp.Hour, tp.Minute, 0, 0, loc)
	return nextReport, tp
}

func getNextReportDescription(ctx *AppContext) string {
	enabled, _, _ := ctx.Settings.GetReportsSettings()
	if !enabled {
		return ctx.Tr("report_disabled")
	}

	nextReport, tp := getNextReportTime(ctx)
	now := time.Now().In(ctx.State.TimeLocation)
	
	if nextReport.Year() == now.Year() && nextReport.Month() == now.Month() && nextReport.Day() == now.Day() {
		return fmt.Sprintf(ctx.Tr("report_next"), tp.Hour, tp.Minute)
	} else if nextReport.Year() == now.Year() && nextReport.Month() == now.Month() && nextReport.Day() == now.AddDate(0, 0, 1).Day() {
		return fmt.Sprintf(ctx.Tr("report_next_tmr"), tp.Hour, tp.Minute)
	}
	
	return fmt.Sprintf(ctx.Tr("report_next_date"), nextReport.Day(), nextReport.Month().String()[:3], tp.Hour, tp.Minute)
}

func periodicReport(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	interval := time.Duration(ctx.Config.Intervals.StatsSeconds) * time.Second
	if !sleepWithContext(runCtx, interval*2) {
		return
	}

	for {
		enabled, _, _ := ctx.Settings.GetReportsSettings()

		if !enabled {
			if !sleepWithContext(runCtx, 1*time.Hour) {
				return
			}
			continue
		}

		nextReport, tp := getNextReportTime(ctx)
		sleepDuration := time.Until(nextReport)

		// Greeting based on time of day
		greeting := ctx.Tr("good_morning")
		if tp.Hour >= 12 && tp.Hour < 18 {
			greeting = ctx.Tr("good_afternoon")
		} else if tp.Hour >= 18 {
			greeting = ctx.Tr("good_evening")
		}

		slog.Info("Next report scheduled", "time", nextReport.Format("02/01 15:04"))
		if !sleepWithContext(runCtx, sleepDuration) {
			return
		}

		report := generateDailyReport(ctx, greeting, nil)
		if err := sendScheduledReport(bot, ctx.Config.AllowedUserID, report); err != nil {
			slog.Error("Failed to send scheduled report", "err", err)
		} else {
			ctx.State.Mu.Lock()
			ctx.State.LastReport = time.Now()
			ctx.State.Mu.Unlock()
			goSafe("save-state-after-report", func() { saveState(ctx) })
			goSafe("logs-prune-after-report", func() {
				prunePersistentLogsAfterReport(ctx)
			})
		}
	}
}

func sendScheduledReport(bot BotAPI, chatID int64, report string) error {
	msg := tgbotapi.NewMessage(chatID, report)
	msg.ParseMode = "Markdown"
	if _, err := bot.Send(msg); err != nil {
		slog.Warn("Scheduled report Markdown failed, retrying plain text", "err", err)
		msg.ParseMode = ""
		if _, plainErr := bot.Send(msg); plainErr != nil {
			return fmt.Errorf("markdown send failed: %w; plain text send failed: %v", err, plainErr)
		}
	}
	return nil
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
