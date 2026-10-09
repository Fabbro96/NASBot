package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	// reportRetryBaseBackoff is the first delay before retrying a failed report.
	reportRetryBaseBackoff = 5 * time.Minute
	// reportRetryMaxBackoff caps the exponential retry delay.
	reportRetryMaxBackoff = 6 * time.Hour
	// reportMaxSendAttempts bounds the retries of a single scheduled report, so
	// a permanently unreachable Telegram API stops after ~75 minutes and the
	// next scheduled slot tries again from scratch.
	reportMaxSendAttempts = 5
	// reportDisabledPoll is the ceiling used while reports are disabled, in case
	// the settings wake-up channel is not wired.
	reportDisabledPoll = 1 * time.Hour
	// reportSettingsPollInterval is the wake-up ceiling for a pending report.
	reportSettingsPollInterval = 10 * time.Minute
	// reportDueTolerance absorbs timer/scheduling jitter when deciding whether
	// a wake-up means the scheduled slot really arrived. Anything waking up
	// more than this before the slot (poll ceiling, settings change) just
	// recomputes the schedule instead of sending a report.
	reportDueTolerance = 30 * time.Second
)

// getNextReportTime calculates the next report time based on settings (interval or specific days of week)
func getNextReportTime(ctx *AppContext) (time.Time, TimePoint) {
	enabled, intervalDays, times, days := ctx.Settings.GetReportsDetailedSettings()
	loc := ctx.State.TimeLocation
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)

	if !enabled || len(times) == 0 {
		return now.Add(24 * 365 * time.Hour), TimePoint{}
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

	const gracePeriod = 5 * time.Minute
	nowDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	// --- Mode 1: Specific Days of the Week ---
	if len(days) > 0 {
		dayMap := make(map[int]bool, len(days))
		for _, d := range days {
			dayMap[d] = true
		}

		for offset := 0; offset <= 14; offset++ {
			candidateDate := nowDate.AddDate(0, 0, offset)
			weekday := int(candidateDate.Weekday()) // 0=Sunday, 1=Monday, ..., 6=Saturday
			if !dayMap[weekday] {
				continue
			}

			for _, tp := range times {
				reportTime := time.Date(candidateDate.Year(), candidateDate.Month(), candidateDate.Day(), tp.Hour, tp.Minute, 0, 0, loc)

				if offset == 0 {
					sameDay := !lastReport.IsZero() &&
						lastReport.Year() == now.Year() &&
						lastReport.Month() == now.Month() &&
						lastReport.Day() == now.Day()

					alreadySent := sameDay && (lastReport.Hour() > tp.Hour || (lastReport.Hour() == tp.Hour && lastReport.Minute() >= tp.Minute))
					if alreadySent {
						continue
					}

					if now.After(reportTime) && now.Before(reportTime.Add(gracePeriod)) {
						slog.Info("Report: Missed report on scheduled day, triggering now (grace period)")
						return now, tp
					}

					if now.Before(reportTime) {
						return reportTime, tp
					}
				} else {
					return reportTime, tp
				}
			}
		}
		return now.Add(24 * 365 * time.Hour), TimePoint{}
	}

	// --- Mode 2: Interval of Days ---
	if intervalDays < 1 {
		intervalDays = 1
	}

	lastReportDate := time.Date(lastReport.Year(), lastReport.Month(), lastReport.Day(), 0, 0, 0, 0, loc)
	if lastReport.IsZero() {
		lastReportDate = nowDate.AddDate(0, 0, -intervalDays)
	}

	daysSinceLast := int(nowDate.Sub(lastReportDate).Hours() / 24)
	if daysSinceLast < 0 {
		daysSinceLast = intervalDays // force recalculation for clock skew
	}

	isReportDay := daysSinceLast >= intervalDays || daysSinceLast == 0

	if isReportDay {
		for _, tp := range times {
			reportTime := time.Date(now.Year(), now.Month(), now.Day(), tp.Hour, tp.Minute, 0, 0, loc)

			sameDay := !lastReport.IsZero() &&
				lastReport.Year() == now.Year() &&
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
	enabled, _, _, _ := ctx.Settings.GetReportsDetailedSettings()
	if !enabled {
		return ctx.Tr("report_disabled")
	}

	nextReport, tp := getNextReportTime(ctx)
	loc := ctx.State.TimeLocation
	if loc == nil {
		loc = time.Local
	}
	now := time.Now().In(loc)
	tmr := now.AddDate(0, 0, 1)

	if nextReport.Year() == now.Year() && nextReport.Month() == now.Month() && nextReport.Day() == now.Day() {
		return fmt.Sprintf(ctx.Tr("report_next"), tp.Hour, tp.Minute)
	} else if nextReport.Year() == tmr.Year() && nextReport.Month() == tmr.Month() && nextReport.Day() == tmr.Day() {
		return fmt.Sprintf(ctx.Tr("report_next_tmr"), tp.Hour, tp.Minute)
	}

	weekdayName := getWeekdayShortName(ctx, int(nextReport.Weekday()))
	return fmt.Sprintf("%s %02d/%02d %02d:%02d", weekdayName, nextReport.Day(), int(nextReport.Month()), tp.Hour, tp.Minute)
}

func getWeekdayShortName(ctx *AppContext, weekday int) string {
	switch weekday {
	case 1:
		return ctx.Tr("mon_short")
	case 2:
		return ctx.Tr("tue_short")
	case 3:
		return ctx.Tr("wed_short")
	case 4:
		return ctx.Tr("thu_short")
	case 5:
		return ctx.Tr("fri_short")
	case 6:
		return ctx.Tr("sat_short")
	case 0:
		return ctx.Tr("sun_short")
	default:
		return ""
	}
}

func periodicReport(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	interval := time.Duration(ctx.Cfg().Intervals.StatsSeconds) * time.Second
	if !sleepWithContext(runCtx, interval*2) {
		return
	}

	for {
		enabled, _, _ := ctx.Settings.GetReportsSettings()

		if !enabled {
			// Wake up on a settings change as well as on the poll ceiling, so
			// enabling reports takes effect immediately instead of up to 1h later.
			if !sleepReportWake(runCtx, ctx, reportDisabledPoll) {
				return
			}
			continue
		}

		nextReport, tp := getNextReportTime(ctx)
		sleepDuration := time.Until(nextReport)
		if sleepDuration < 0 {
			// getNextReportTime can return "now" (grace period) or a time already
			// in the past; never hand a negative duration to the timer.
			sleepDuration = 0
		}

		// Greeting based on time of day
		greeting := ctx.Tr("good_morning")
		if tp.Hour >= 12 && tp.Hour < 18 {
			greeting = ctx.Tr("good_afternoon")
		} else if tp.Hour >= 18 {
			greeting = ctx.Tr("good_evening")
		}

		slog.Info("Next report scheduled", "time", nextReport.Format("02/01 15:04"))
		if !sleepReportWake(runCtx, ctx, sleepDuration) {
			return
		}

		// sleepReportWake returns on three events: the timer expiring (report
		// due), a settings change, or the 10-minute poll ceiling. Only the
		// first one means the report is due: without this guard every poll
		// wake-up looked like an expired timer (reportsStillDue below still
		// matched the unchanged schedule) and a report was generated every
		// 10 minutes.
		if !reportSlotDue(nextReport) {
			slog.Info("Report woke early (settings/poll), recomputing",
				"time", nextReport.Format("02/01 15:04"))
			continue
		}

		// Re-read the settings after waking up: the user may have disabled the
		// reports, or moved the time/day, while we were sleeping.
		if !reportsStillDue(ctx, nextReport) {
			slog.Info("Report schedule changed while waiting, recomputing")
			continue
		}

		// One AI call per scheduled slot. The retries below re-send this exact
		// text, they never regenerate it.
		report := generateDailyReport(ctx, greeting, nil)

		// Record the attempt, successful or not, BEFORE sending. generateDailyReport
		// calls the AI, and with LastReport left untouched a failed send used to be
		// retried in a tight loop: getNextReportTime saw a stale timestamp, the
		// 5-minute grace window matched, sleepDuration was 0, and the next
		// iteration generated another report and called Gemini again, forever.
		ctx.State.Mu.Lock()
		ctx.State.LastReport = time.Now()
		ctx.State.Mu.Unlock()

		if !deliverReport(ctx, bot, report, runCtx) {
			return
		}

		goSafe("save-state-after-report", func() { saveState(ctx) })
		goSafe("logs-prune-after-report", func() {
			prunePersistentLogsAfterReport(ctx)
		})
	}
}

// deliverReport sends an already generated report, retrying with exponential
// backoff. It returns false only when runCtx is done (shutdown), and true once
// the report was delivered or the attempt budget ran out.
//
// The backoff matters for every failure cause, not just an oversized message: a
// network hiccup must not turn into a retry per loop iteration either, and
// re-generating the report on each retry would be a Gemini call per retry.
func deliverReport(ctx *AppContext, bot BotAPI, report string, runCtx context.Context) bool {
	chatID := ctx.Cfg().AllowedUserID
	notice := ctx.Tr("report_truncated")

	for attempt := 1; attempt <= reportMaxSendAttempts; attempt++ {
		if err := sendScheduledReportSplit(bot, chatID, report, notice); err == nil {
			return true
		} else {
			slog.Error("Failed to send scheduled report", "err", sanitizeErr(err),
				"attempt", attempt, "max_attempts", reportMaxSendAttempts)
		}

		if attempt == reportMaxSendAttempts {
			break
		}
		// Sleep the full backoff: sleepReportWake would wake up early on the
		// 10-minute poll ceiling and retry sooner than intended.
		if !sleepWithContext(runCtx, reportRetryBackoff(attempt)) {
			return false
		}
	}

	slog.Error("Scheduled report gave up after the retry budget; waiting for the next slot",
		"attempts", reportMaxSendAttempts)
	return true
}

// reportRetryBackoff returns the delay before retrying a failed report send.
// Exponential from reportRetryBaseBackoff (5m) and capped at reportRetryMaxBackoff
// (6h), so a permanently oversized or unreachable target costs a bounded number
// of attempts instead of one per loop iteration. `attempt` is 1-based.
func reportRetryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	// Cap the shift so the multiply cannot overflow on a very long-lived failure.
	if attempt > 20 {
		attempt = 20
	}
	d := reportRetryBaseBackoff << uint(attempt-1)
	if d > reportRetryMaxBackoff {
		d = reportRetryMaxBackoff
	}
	return d
}

// reportSlotDue reports whether a scheduled slot has actually arrived,
// tolerating timer/scheduling jitter. Wake-ups from the 10-minute poll ceiling
// or a settings change arrive well before the slot and must only recompute the
// schedule, never send a report.
func reportSlotDue(scheduled time.Time) bool {
	return time.Until(scheduled) <= reportDueTolerance
}

// reportsStillDue re-validates a scheduled slot against the current settings.
// It is called after the sleep so a report already planned for 18:30 is not
// delivered at 18:30 if the user disabled reports or moved the slot in the
// meantime.
func reportsStillDue(ctx *AppContext, scheduled time.Time) bool {
	enabled, _, times, _ := ctx.Settings.GetReportsDetailedSettings()
	if !enabled || len(times) == 0 {
		return false
	}
	next, _ := getNextReportTime(ctx)
	return !next.After(scheduled)
}

// reportsChangeSignaler is the optional interface model.UserSettings must
// implement to let the scheduler wake up as soon as report settings change.
//
// Contract for the settings lane (see internal/app/reports_schedule.go):
//
//	func (s *UserSettings) OnReportsChanged() <-chan struct{}
//
// Every setter that can change what the scheduler should do — SetReportsDays,
// ToggleReportDay, SetReportsSettings, and any future one — must, while holding
// s.Mu, swap s.reportsChanged for a fresh make(chan struct{}) and then close the
// old channel *after* releasing the lock. Replacing before closing is what makes
// the broadcast safe: a reader that already grabbed the closed channel must be
// able to re-read and get a live one, otherwise the scheduler spins.
func reportsChangeSignaler(ctx *AppContext) <-chan struct{} {
	if s, ok := any(ctx.Settings).(interface {
		OnReportsChanged() <-chan struct{}
	}); ok {
		return s.OnReportsChanged()
	}
	// Not implemented yet: a nil channel blocks forever, and sleepReportWake
	// still has a poll ceiling, so the scheduler keeps working, just less
	// responsive to /settings changes.
	return nil
}

// sleepReportWake waits for d and returns early when the user changes report
// settings. It returns false only when runCtx is done (shutdown).
//
// The poll timer is a ceiling, not the mechanism: it bounds the damage if the
// wake-up channel is not wired yet, and it prevents a hot spin if the computed
// schedule ever degenerates to "due right now".
func sleepReportWake(runCtx context.Context, ctx *AppContext, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-runCtx.Done():
			return false
		default:
			return true
		}
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	poll := time.NewTimer(reportSettingsPollInterval)
	defer poll.Stop()
	sig := reportsChangeSignaler(ctx)

	for {
		select {
		case <-runCtx.Done():
			return false
		case <-timer.C:
			return true
		case <-poll.C:
			return true
		case <-sig:
			// Re-read: the sender replaced the channel before closing it.
			sig = reportsChangeSignaler(ctx)
			poll.Reset(reportSettingsPollInterval)
			return true
		}
	}
}

// telegramMessageBudget is the per-message character budget used when splitting
// a report. The Bot API limit is 4096 characters; the margin absorbs the fact
// that Telegram counts an astral-plane rune (emoji) as two UTF-16 code units
// while utf8.RuneCountInString counts it as one.
const telegramMessageBudget = 4000

// maxReportChunks bounds how many messages one report may be split into, so a
// pathological day cannot flood the chat with 50 messages.
const maxReportChunks = 4

// splitTelegramMessage splits text into chunks that respect budget, preferring
// line boundaries so Markdown blocks are not cut in half. Returns at most
// maxChunks chunks; when text has to be dropped, notice is appended to the last
// chunk so the user knows the report is incomplete.
func splitTelegramMessage(text string, budget int, maxChunks int, notice string) []string {
	if budget <= 0 {
		budget = telegramMessageBudget
	}
	if maxChunks <= 0 {
		maxChunks = maxReportChunks
	}
	// Report text embeds kernel logs, filenames and container output, which can
	// carry non-UTF-8 bytes (latin-1, binary garbage). Telegram rejects such a
	// message outright, losing the whole report. Normalize once here so every
	// chunk below — including the short-circuit return — is valid UTF-8.
	text = strings.ToValidUTF8(text, "�")
	if utf8.RuneCountInString(text) <= budget {
		return []string{text}
	}

	var chunks []string
	rest := text
	for len(chunks) < maxChunks {
		if utf8.RuneCountInString(rest) <= budget {
			chunks = append(chunks, rest)
			rest = ""
			break
		}
		// prefix is always a byte prefix of rest, so the slice arithmetic below
		// can never split a UTF-8 sequence and always makes progress.
		prefix := takeRunePrefix(rest, budget)
		chunk := prefix
		if idx := strings.LastIndex(prefix, "\n"); idx >= 0 && idx+1 > budget/2 {
			chunk = prefix[:idx]
		}
		chunk = strings.TrimRight(chunk, "\n")
		if chunk == "" {
			// Pathological input (a whole budget of newlines): keep the raw
			// prefix so the loop cannot stall.
			chunk = prefix
		}
		chunks = append(chunks, chunk)
		rest = strings.TrimLeft(rest[len(chunk):], "\n")
	}

	if rest != "" && notice != "" {
		last := chunks[len(chunks)-1]
		chunks[len(chunks)-1] = last + "\n" + notice
	}
	return chunks
}

// takeRunePrefix returns the longest prefix of s holding at most n runes.
func takeRunePrefix(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// reportTruncatedNotice returns the truncation marker in the language of the
// running bot.
//
// sendScheduledReport takes no *AppContext, so the language comes from the
// package-level app when there is one (bot runtime) and from the "en"
// dictionary otherwise (the watchdog binary, tests). The text is always the
// dictionary entry: the constant that used to live here was the reason a report
// cut in half ended with an English marker inside an Italian message.
func reportTruncatedNotice() string {
	if app != nil {
		return app.Tr("report_truncated")
	}
	return translateByLanguage("", "report_truncated")
}

func sendScheduledReport(bot BotAPI, chatID int64, report string) error {
	return sendScheduledReportSplit(bot, chatID, report, reportTruncatedNotice())
}

// sendScheduledReportSplit sends a report, splitting it to respect the Bot API
// length limit. Telegram rejects anything over 4096 characters, and the daily
// report easily exceeds that: 100 events at 35 runes plus headers is ~4.5k.
// notice is appended when the report had to be cut; it must already be
// localized.
func sendScheduledReportSplit(bot BotAPI, chatID int64, report, notice string) error {
	chunks := splitTelegramMessage(report, telegramMessageBudget, maxReportChunks, notice)
	for i, chunk := range chunks {
		msg := tgbotapi.NewMessage(chatID, chunk)
		msg.ParseMode = "Markdown"
		if _, err := bot.Send(msg); err != nil {
			slog.Warn("Scheduled report Markdown failed, retrying plain text", "err", sanitizeErr(err), "chunk", i+1, "chunks", len(chunks))
			msg.ParseMode = ""
			if _, plainErr := bot.Send(msg); plainErr != nil {
				// Sanitized before it is wrapped, because this error travels to the
				// caller and to the log above it, and every one of those would
				// otherwise re-print the token the Bot API URL carries.
				return fmt.Errorf("chunk %d/%d: markdown send failed: %w; plain text send failed: %v", i+1, len(chunks), sanitizeErr(err), sanitizeErr(plainErr))
			}
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
