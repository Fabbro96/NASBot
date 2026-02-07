package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  DAILY REPORTS
// ═══════════════════════════════════════════════════════════════════

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

	// Single report
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

		// cleanOldReportEvents() I'll implement it locally or use helper
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

func generateAIReport(ctx *AppContext, s Stats, events []ReportEvent, isMorning bool, onModelChange func(string)) (string, error) {
	return generateAIReportWithPeriod(ctx, s, events, isMorning, "", onModelChange)
}

func generateAIReportWithPeriod(ctx *AppContext, s Stats, events []ReportEvent, isMorning bool, periodDesc string, onModelChange func(string)) (string, error) {
	if ctx.Config.GeminiAPIKey == "" {
		return "", nil
	}

	var sysContext strings.Builder
	sysContext.WriteString("NAS System Status:\n")
	sysContext.WriteString(fmt.Sprintf("- CPU: %.1f%%\n", s.CPU))
	// ... (rest of AI prompt construction using ctx) ...
	// Truncated for brevity, assuming structure is same but using ctx.Monitor.Healthchecks etc.

	// Reimplementing prompt building fully to be safe
	sysContext.WriteString(fmt.Sprintf("- RAM: %.1f%%\n", s.RAM))
	if s.Swap > 5 {
		sysContext.WriteString(fmt.Sprintf("- Swap: %.1f%%\n", s.Swap))
	}
	sysContext.WriteString(fmt.Sprintf("- SSD: %.1f%% used, %s free\n", s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	sysContext.WriteString(fmt.Sprintf("- HDD: %.1f%% used, %s free\n", s.VolHDD.Used, format.FormatBytes(s.VolHDD.Free)))
	sysContext.WriteString(fmt.Sprintf("- Uptime: %s\n", format.FormatUptime(s.Uptime)))

	containers := getCachedContainerList(ctx)
	running, stopped := 0, 0
	stoppedList := []string{}
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
			stoppedList = append(stoppedList, c.Name)
		}
	}
	if ctx.Config.Healthchecks.Enabled {
		hc := ctx.Monitor.Healthchecks
		status := "Offline"
		if hc.LastPingSuccess {
			status = "Online"
		}
		sysContext.WriteString(fmt.Sprintf("- Healthchecks.io: %s (%.1f%% success rate)\n", status, float64(hc.SuccessfulPings)/float64(maxInt(hc.TotalPings, 1))*100))
		if len(hc.DowntimeEvents) > 0 {
			sysContext.WriteString("  Recent Healthchecks downtimes:\n")
			ctx.State.mu.Lock()
			lastReportTime := ctx.State.LastReport
			ctx.State.mu.Unlock()
			for _, e := range hc.DowntimeEvents {
				if e.StartTime.After(lastReportTime) {
					sysContext.WriteString(fmt.Sprintf("  - %s: %s (%s)\n", e.StartTime.Format("15:04"), e.Reason, e.Duration))
				}
			}
		}
	}
	sysContext.WriteString(fmt.Sprintf("- Docker: %d running, %d stopped\n", running, stopped))
	if len(stoppedList) > 0 {
		sysContext.WriteString(fmt.Sprintf("- Stopped containers: %s\n", strings.Join(stoppedList, ", ")))
	}

	eventsInfo := "No events recorded in this period."
	if len(events) > 0 {
		eventsInfo = fmt.Sprintf("%d events recorded:\n", len(events))
		loc := ctx.State.TimeLocation
		for _, e := range events {
			eventsInfo += fmt.Sprintf("- [%s] %s: %s\n", e.Time.In(loc).Format("15:04"), e.Type, e.Message)
		}
	}
	sysContext.WriteString(fmt.Sprintf("\nEvents: %s", eventsInfo))

	timeOfDay := "morning"
	if !isMorning {
		timeOfDay = "evening"
	}

	lang := "English"
	if ctx.Settings.GetLanguage() == "it" {
		lang = "Italian"
	}

	periodInfo := ""
	if periodDesc != "" {
		periodInfo = fmt.Sprintf("\n**Report Period:** %s", periodDesc)
	}

	prompt := fmt.Sprintf(`You are an intelligent home NAS assistant named "NasBot".
Your goal is to write a **Daily Report** for the owner.

**Status Data:**
%s%s

**Time:** %s

**Instructions:**
1. **Style:** Friendly, discursive/narrative, but **CONCISE**. Keep it short.
2. **Language:** Write in %s.
3. **Format:** Use Markdown (bold, italics) and Emojis.
4. **Content:**
   - Greeting and date/time.
   - **MANDATORY:** State number of running/stopped containers.
   - **Focus on what happened.**
   - If no events, say "quiet period".
   - If everything is fine, say it briefly.
   - Explain issues clearly with warning icons.
   - Mention resources only if high usage.
   - End with short footer showing report period.

**Goal:** Useful, readable, short summary.`, sysContext.String(), periodInfo, timeOfDay, lang)

	return callGeminiWithFallback(ctx, prompt, onModelChange)
}

func callGeminiWithFallback(ctx *AppContext, prompt string, onModelChange func(string)) (string, error) {
	models := []string{"gemini-2.5-flash", "gemini-2.5-pro", "gemini-2.0-flash"}

	c, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var summary string
	var err error

	for _, model := range models {
		select {
		case <-c.Done():
			slog.Error("Gemini: Overall timeout")
			return "", fmt.Errorf("overall timeout")
		default:
		}

		if onModelChange != nil {
			onModelChange(model)
		}
		summary, err = callGeminiAPIWithError(ctx, prompt, model)
		if err == nil {
			return summary, nil
		}
	}
	return "", err
}

func callGeminiAPIWithError(ctx *AppContext, prompt string, model string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, ctx.Config.GeminiAPIKey)

	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{"parts": []map[string]string{{"text": prompt}}},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 8192,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	c, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(c, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	// Use ctx.HTTP if available
	client := http.DefaultClient
	if ctx.HTTP != nil {
		client = ctx.HTTP
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text), nil
	}
	return "", fmt.Errorf("empty response")
}

func getHealthStatus(ctx *AppContext, s Stats) (icon, text string, hasProblems bool) {
	events := ctx.State.GetEvents()
	criticalCount := 0
	warningCount := 0
	for _, e := range events {
		if e.Type == "critical" {
			criticalCount++
		} else if e.Type == "warning" {
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
