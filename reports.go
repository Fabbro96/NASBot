package main

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

func generateDailyReport(ctx *AppContext, greeting string, isMorning bool, onModelChange func(string)) string {
	s, _ := ctx.Stats.Get()
	now := time.Now().In(ctx.State.TimeLocation)

	events := ctx.State.GetEvents()

	ctx.State.mu.Lock()
	lastReportTime := ctx.State.LastReport
	ctx.State.mu.Unlock()

	events = filterEventsSince(events, lastReportTime)
	events = filterSignificantEvents(events)

	periodDesc := ""
	if !lastReportTime.IsZero() {
		periodDesc = fmt.Sprintf("%s → %s", lastReportTime.In(ctx.State.TimeLocation).Format("15:04"), now.Format("15:04"))
	} else {
		midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ctx.State.TimeLocation)
		periodDesc = fmt.Sprintf("%s → %s", midnight.Format("15:04"), now.Format("15:04"))
	}

	aiReport, aiErr := generateAIReportWithPeriod(ctx, s, events, isMorning, periodDesc, onModelChange)
	if aiErr == nil && aiReport != "" {
		resetStressCounters(ctx)
		return aiReport
	}
	if aiErr != nil {
		slog.Error("Gemini AI report error", "err", aiErr)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("*%s*\n", greeting))
	b.WriteString(fmt.Sprintf("_%s_\n\n", now.Format("Mon 02/01")))

	healthIcon, healthText, _ := getHealthStatus(ctx, s)
	b.WriteString(fmt.Sprintf("📝 %s %s\n\n", healthIcon, healthText))

	if len(events) > 0 {
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

	b.WriteString(fmt.Sprintf("*%s*\n", ctx.Tr("report_resources")))
	b.WriteString(fmt.Sprintf("🧠 CPU %s %2.0f%%\n", format.MakeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("💾 RAM %s %2.0f%%\n", format.MakeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("🔄 Swap %s %2.0f%%\n", format.MakeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\n💿 SSD %2.0f%% · %s free\n", s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("🗄 HDD %2.0f%% · %s free\n", s.VolHDD.Used, format.FormatBytes(s.VolHDD.Free)))

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
		ctx.Monitor.mu.Lock()
		hc := ctx.Monitor.Healthchecks
		ctx.Monitor.mu.Unlock()
		status := "❌"
		if hc.LastPingSuccess {
			status = "✅"
		}
		b.WriteString(fmt.Sprintf("\n💓 Healthchecks: %s (%.1f%%)", status, float64(hc.SuccessfulPings)/float64(maxInt(hc.TotalPings, 1))*100))
	}
	b.WriteString(fmt.Sprintf("\n🐳 %d %s", running, containerLabel))
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d %s", stopped, ctx.Tr("containers_stopped")))
	}

	stressSummary := getStressSummary(ctx)
	if stressSummary != "" {
		b.WriteString(fmt.Sprintf("\n\n💨 *%s*\n", ctx.Tr("report_stress")))
		b.WriteString(stressSummary)
	}

	b.WriteString(fmt.Sprintf("\n\n_⏱ Up for %s_", format.FormatUptime(s.Uptime)))

	resetStressCounters(ctx)
	return b.String()
}

func getHealthStatus(ctx *AppContext, s Stats) (icon, text string, hasProblems bool) {
	events := ctx.State.GetEvents()
	criticalCount := 0
	warningCount := 0
	for _, e := range events {
		switch e.Type {
		case "critical":
			criticalCount++
		case "warning":
			warningCount++
		}
	}

	cfg := ctx.Config
	if criticalCount > 0 || s.CPU > cfg.Notifications.CPU.CriticalThreshold ||
		s.RAM > cfg.Notifications.RAM.CriticalThreshold ||
		s.VolSSD.Used > cfg.Notifications.DiskSSD.CriticalThreshold {
		return "⚠️", ctx.Tr("health_critical"), true
	}

	if warningCount > 0 || s.CPU > cfg.Notifications.CPU.WarningThreshold*0.9 ||
		s.RAM > cfg.Notifications.RAM.WarningThreshold*0.95 {
		return "👀", ctx.Tr("health_warning"), true
	}
	return "✨", ctx.Tr("health_ok"), false
}

func generateReport(ctx *AppContext, manual bool, onModelChange func(string)) string {
	if !manual {
		return generateDailyReport(ctx, "> *NAS Report*", true, onModelChange)
	}

	s, _ := ctx.Stats.Get()
	now := time.Now().In(ctx.State.TimeLocation)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, ctx.State.TimeLocation)

	events := ctx.State.GetEvents()
	events = filterEventsSince(events, midnight)
	filteredEvents := filterSignificantEvents(events)

	periodDesc := fmt.Sprintf("00:00 → %s (today)", now.Format("15:04"))
	isMorning := now.Hour() < 12

	aiReport, aiErr := generateAIReportWithPeriod(ctx, s, filteredEvents, isMorning, periodDesc, onModelChange)
	if aiErr == nil && aiReport != "" {
		return aiReport
	}

	var b strings.Builder
	b.WriteString(ctx.Tr("report_title"))
	b.WriteString(fmt.Sprintf("%s\n\n", now.Format("02/01 15:04")))

	healthIcon, healthText, _ := getHealthStatus(ctx, s)
	b.WriteString(fmt.Sprintf("📝 %s %s\n\n", healthIcon, healthText))

	if aiErr != nil {
		b.WriteString(fmt.Sprintf(ctx.Tr("llm_error"), aiErr))
	}

	// ... (Rest of manual report construction similar to simple report) ...
	// Resource summary
	b.WriteString(fmt.Sprintf("*%s*\n", ctx.Tr("report_resources")))
	b.WriteString(fmt.Sprintf("CPU %s %.1f%%\n", format.MakeProgressBar(s.CPU), s.CPU))
	// Details truncated for brevity

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

// maxInt removed
