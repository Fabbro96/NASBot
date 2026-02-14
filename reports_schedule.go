package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// getNextReportTime calculates the next report time based on reportMode
func getNextReportTime(ctx *AppContext) (time.Time, bool) {
	ctx.Settings.mu.RLock()
	mode := ctx.Settings.ReportMode
	morning := ctx.Settings.ReportMorning
	evening := ctx.Settings.ReportEvening
	ctx.Settings.mu.RUnlock()

	loc := ctx.State.TimeLocation
	now := time.Now().In(loc)

	if mode == 0 {
		return now.Add(24 * 365 * time.Hour), false
	}

	morningReport := time.Date(now.Year(), now.Month(), now.Day(),
		morning.Hour, morning.Minute, 0, 0, loc)
	eveningReport := time.Date(now.Year(), now.Month(), now.Day(),
		evening.Hour, evening.Minute, 0, 0, loc)

	const gracePeriod = 5 * time.Minute

	ctx.State.mu.Lock()
	lastReportToday := ctx.State.LastReport.In(loc)
	ctx.State.mu.Unlock()

	sameDay := lastReportToday.Year() == now.Year() &&
		lastReportToday.Month() == now.Month() &&
		lastReportToday.Day() == now.Day()

	morningDone := sameDay && lastReportToday.Hour() >= morning.Hour &&
		(lastReportToday.Hour() > morning.Hour || lastReportToday.Minute() >= morning.Minute)
	eveningDone := sameDay && lastReportToday.Hour() >= evening.Hour &&
		(lastReportToday.Hour() > evening.Hour || lastReportToday.Minute() >= evening.Minute)

	if mode == 2 {
		if !morningDone && now.After(morningReport) && now.Before(morningReport.Add(gracePeriod)) {
			slog.Info("Report: Missed morning report, triggering now (grace period)")
			return now, true
		}
		if !eveningDone && now.After(eveningReport) && now.Before(eveningReport.Add(gracePeriod)) {
			slog.Info("Report: Missed evening report, triggering now (grace period)")
			return now, false
		}
		if now.Before(morningReport) {
			return morningReport, true
		} else if now.Before(eveningReport) {
			return eveningReport, false
		}
		return morningReport.Add(24 * time.Hour), true
	}

	if !morningDone && now.After(morningReport) && now.Before(morningReport.Add(gracePeriod)) {
		slog.Info("Report: Missed daily report, triggering now (grace period)")
		return now, true
	}
	if now.Before(morningReport) {
		return morningReport, true
	}
	return morningReport.Add(24 * time.Hour), true
}

func getNextReportDescription(ctx *AppContext) string {
	ctx.Settings.mu.RLock()
	mode := ctx.Settings.ReportMode
	morning := ctx.Settings.ReportMorning
	evening := ctx.Settings.ReportEvening
	ctx.Settings.mu.RUnlock()

	loc := ctx.State.TimeLocation
	now := time.Now().In(loc)

	if mode == 0 {
		return ctx.Tr("reprt_disabled")
	}

	if mode == 2 {
		m := time.Date(now.Year(), now.Month(), now.Day(), morning.Hour, morning.Minute, 0, 0, loc)
		e := time.Date(now.Year(), now.Month(), now.Day(), evening.Hour, evening.Minute, 0, 0, loc)
		if now.Before(m) {
			return fmt.Sprintf(ctx.Tr("report_next"), morning.Hour, morning.Minute)
		} else if now.Before(e) {
			return fmt.Sprintf(ctx.Tr("report_next"), evening.Hour, evening.Minute)
		}
		return fmt.Sprintf(ctx.Tr("report_next_tmr"), morning.Hour, morning.Minute)
	}

	return fmt.Sprintf(ctx.Tr("report_daily"), morning.Hour, morning.Minute)
}

func periodicReport(ctx *AppContext, bot BotAPI, runCtx ...context.Context) {
	interval := time.Duration(ctx.Config.Intervals.StatsSeconds) * time.Second
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}
	if !sleepWithContext(rc, interval*2) {
		return
	}

	for {
		ctx.Settings.mu.RLock()
		mode := ctx.Settings.ReportMode
		ctx.Settings.mu.RUnlock()

		if mode == 0 {
			if !sleepWithContext(rc, 1*time.Hour) {
				return
			}
			continue
		}

		nextReport, isMorning := getNextReportTime(ctx)
		sleepDuration := time.Until(nextReport)

		greeting := ctx.Tr("good_morning")
		if !isMorning {
			greeting = ctx.Tr("good_evening")
		}

		slog.Info("Next report scheduled", "time", nextReport.Format("02/01 15:04"))
		if !sleepWithContext(rc, sleepDuration) {
			return
		}

		report := generateDailyReport(ctx, greeting, isMorning, nil)
		msg := tgbotapi.NewMessage(ctx.Config.AllowedUserID, report)
		msg.ParseMode = "Markdown"
		if _, err := bot.Send(msg); err != nil {
			slog.Error("Failed to send scheduled report", "err", err)
		} else {
			ctx.State.mu.Lock()
			ctx.State.LastReport = time.Now()
			ctx.State.mu.Unlock()
			go saveState(ctx)
		}
	}
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
