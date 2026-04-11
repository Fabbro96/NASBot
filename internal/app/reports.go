package app

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"nasbot/internal/format"
)

// ═══════════════════════════════════════════════════════════════════
//  DAILY REPORTS
// ═══════════════════════════════════════════════════════════════════

func generateDailyReport(ctx *AppContext, greeting string, onModelChange func(string)) string {
	s, _ := ctx.Stats.Get()
	now := time.Now().In(ctx.State.TimeLocation)

	events := ctx.State.GetEvents()

	ctx.State.Mu.Lock()
	lastReportTime := ctx.State.LastReport
	ctx.State.Mu.Unlock()

	events = filterEventsSince(events, lastReportTime)
	events = filterSignificantEvents(events)

	periodDesc := ""
	if !lastReportTime.IsZero() {
		periodDesc = fmt.Sprintf("%s → %s", lastReportTime.In(ctx.State.TimeLocation).Format("15:04"), now.Format("15:04"))
	} else {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ctx.State.TimeLocation)
		periodDesc = fmt.Sprintf("%s → %s", midnight.Format("15:04"), now.Format("15:04"))
	}

	aiReport, aiErr := generateAIReportWithPeriod(ctx, events, periodDesc, onModelChange)
	if aiErr != nil {
		slog.Error("Gemini AI report error", "err", aiErr)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("*%s*\n", greeting))
	b.WriteString(fmt.Sprintf("_%s_\n\n", now.Format("Mon 02/01")))

	if aiReport != "" {
		b.WriteString(aiReport)
		b.WriteString("\n\n")
	} else if len(events) > 0 {
		b.WriteString(fmt.Sprintf("*%s*\n", ctx.Tr("report_events")))
		for _, e := range events {
			icon := "·"
			switch e.Type {
			case "warning":
				icon = "~"
			case "critical":
				icon = "!"
			case "action":
				icon = ">"
			}
			timeStr := e.Time.In(ctx.State.TimeLocation).Format("15:04")
			b.WriteString(fmt.Sprintf("%s %s %s\n", icon, timeStr, format.Truncate(e.Message, 28)))
		}
		b.WriteString("\n")
	}

	containers := getCachedContainerList(ctx) // use cache
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}

	containerLabel := ctx.Tr("containers_running")
	if running == 1 {
		containerLabel = ctx.Tr("container_running")
	}

	if ctx.Config.Healthchecks.Enabled {
		ctx.Monitor.Mu.Lock()
		hc := ctx.Monitor.Healthchecks
		ctx.Monitor.Mu.Unlock()
		status := "❌"
		if hc.LastPingSuccess {
			status = "✅"
		}
		b.WriteString(fmt.Sprintf("\nHealthchecks: %s (%.1f%%)", status, float64(hc.SuccessfulPings)/float64(maxInt(hc.TotalPings, 1))*100))
	}
	b.WriteString(fmt.Sprintf("\nContainers: %d %s", running, containerLabel))
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d %s", stopped, ctx.Tr("containers_stopped")))
	}

	stressSummary := getStressSummary(ctx)
	if stressSummary != "" {
		b.WriteString(fmt.Sprintf("\n\n*%s*\n", ctx.Tr("report_stress")))
		b.WriteString(stressSummary)
	}

	b.WriteString(fmt.Sprintf("\n\n_Up for %s_\n", format.FormatUptime(s.Uptime)))
	if periodDesc != "" {
		b.WriteString(fmt.Sprintf("_Period: %s_", periodDesc))
	}

	resetStressCounters(ctx)
	return b.String()
}

func generateReport(ctx *AppContext, manual bool, onModelChange func(string)) string {
	if !manual {
		return generateDailyReport(ctx, "> *NAS Report*", onModelChange)
	}

	s, _ := ctx.Stats.Get()
	now := time.Now().In(ctx.State.TimeLocation)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ctx.State.TimeLocation)

	events := ctx.State.GetEvents()
	events = filterEventsSince(events, midnight)
	filteredEvents := filterSignificantEvents(events)

	periodDesc := fmt.Sprintf("00:00 → %s (today)", now.Format("15:04"))

	aiReport, aiErr := generateAIReportWithPeriod(ctx, filteredEvents, periodDesc, onModelChange)

	var b strings.Builder
	b.WriteString(ctx.Tr("report_title"))
	b.WriteString(fmt.Sprintf("%s\n\n", now.Format("02/01 15:04")))

	if aiErr != nil {
		b.WriteString(fmt.Sprintf(ctx.Tr("llm_error"), aiErr))
	}

	if aiReport != "" {
		b.WriteString(fmt.Sprintf("%s\n\n", aiReport))
	} else if len(filteredEvents) > 0 {
		b.WriteString(fmt.Sprintf("*%s*\n", ctx.Tr("report_events")))
		for _, e := range filteredEvents {
			b.WriteString(fmt.Sprintf("- %s %s\n", e.Time.In(ctx.State.TimeLocation).Format("15:04"), format.Truncate(e.Message, 64)))
		}
		b.WriteString("\n")
	}

	running, stopped := 0, 0
	for _, c := range getCachedContainerList(ctx) {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	b.WriteString(fmt.Sprintf("\nContainers: %d running, %d stopped\n", running, stopped))

	b.WriteString(fmt.Sprintf("\n_Up for %s_\n", format.FormatUptime(s.Uptime)))
	if periodDesc != "" {
		b.WriteString(fmt.Sprintf("_Period: %s_", periodDesc))
	}

	return b.String()
}

func filterSignificantEvents(events []ReportEvent) []ReportEvent {
	var filtered []ReportEvent
	for _, e := range events {
		msg := strings.ToLower(e.Message)
		// Basic filtering logic
		if strings.Contains(msg, "for 30s") || strings.Contains(msg, "for 1m") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}

func filterEventsSince(events []ReportEvent, since time.Time) []ReportEvent {
	var filtered []ReportEvent
	for _, e := range events {
		if e.Time.After(since) {
			filtered = append(filtered, e)
		}
	}
	return filtered
}
