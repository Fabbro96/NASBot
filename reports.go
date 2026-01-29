package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  DAILY REPORTS
// ═══════════════════════════════════════════════════════════════════

// getNextReportTime calculates the next report time based on reportMode
func getNextReportTime() (time.Time, bool) {
	now := time.Now().In(location)

	if reportMode == 0 {
		return now.Add(24 * 365 * time.Hour), false
	}

	morningReport := time.Date(now.Year(), now.Month(), now.Day(),
		reportMorningHour, reportMorningMinute, 0, 0, location)
	eveningReport := time.Date(now.Year(), now.Month(), now.Day(),
		reportEveningHour, reportEveningMinute, 0, 0, location)

	if reportMode == 2 {
		if now.Before(morningReport) {
			return morningReport, true
		} else if now.Before(eveningReport) {
			return eveningReport, false
		}
		return morningReport.Add(24 * time.Hour), true
	}

	if now.Before(morningReport) {
		return morningReport, true
	}
	return morningReport.Add(24 * time.Hour), true
}

// getNextReportDescription returns a description of the next scheduled report
func getNextReportDescription() string {
	if reportMode == 0 {
		return "\n📭 _Reports disabled_"
	}

	now := time.Now().In(location)

	if reportMode == 2 {
		morning := time.Date(now.Year(), now.Month(), now.Day(),
			reportMorningHour, reportMorningMinute, 0, 0, location)
		evening := time.Date(now.Year(), now.Month(), now.Day(),
			reportEveningHour, reportEveningMinute, 0, 0, location)

		if now.Before(morning) {
			return fmt.Sprintf("\n📨 _Next report: %02d:%02d_", reportMorningHour, reportMorningMinute)
		} else if now.Before(evening) {
			return fmt.Sprintf("\n📨 _Next report: %02d:%02d_", reportEveningHour, reportEveningMinute)
		}
		return fmt.Sprintf("\n📨 _Next report: %02d:%02d (tomorrow)_", reportMorningHour, reportMorningMinute)
	}

	return fmt.Sprintf("\n📨 _Daily report: %02d:%02d_", reportMorningHour, reportMorningMinute)
}

func periodicReport(bot *tgbotapi.BotAPI) {
	time.Sleep(IntervalStats * 2)

	for {
		if reportMode == 0 {
			time.Sleep(1 * time.Hour)
			continue
		}

		nextReport, isMorning := getNextReportTime()
		sleepDuration := time.Until(nextReport)

		greeting := tr("good_morning")
		if !isMorning {
			greeting = tr("good_evening")
		}

		log.Printf("> Next report: %s", nextReport.Format("02/01 15:04"))
		time.Sleep(sleepDuration)

		report := generateDailyReport(greeting, isMorning)
		msg := tgbotapi.NewMessage(AllowedUserID, report)
		msg.ParseMode = "Markdown"
		bot.Send(msg)

		lastReportTime = time.Now()
		saveState()

		cleanOldReportEvents()
	}
}

func generateDailyReport(greeting string, isMorning bool) string {
	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	b.WriteString(fmt.Sprintf("*%s*\n", greeting))
	b.WriteString(fmt.Sprintf("_%s_\n\n", now.Format("Mon 02/01")))

	reportEventsMutex.Lock()
	events := filterSignificantEvents(reportEvents)
	reportEventsMutex.Unlock()

	aiSummary := generateAISummary(s, events, isMorning)
	if aiSummary != "" {
		b.WriteString(fmt.Sprintf("🤖 _%s_\n\n", aiSummary))
	} else {
		healthIcon, healthText, _ := getHealthStatus(s)
		b.WriteString(fmt.Sprintf("%s %s\n\n", healthIcon, healthText))
	}

	if len(events) > 0 {
		b.WriteString(fmt.Sprintf("*%s*\n", tr("report_events")))
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
			timeStr := e.Time.In(location).Format("15:04")
			b.WriteString(fmt.Sprintf("%s %s %s\n", icon, timeStr, truncate(e.Message, 28)))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("*%s*\n", tr("report_resources")))
	b.WriteString(fmt.Sprintf("🧠 CPU %s %2.0f%%\n", makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("💾 RAM %s %2.0f%%\n", makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("🔄 Swap %s %2.0f%%\n", makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\n💿 SSD %2.0f%% · %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("🗄 HDD %2.0f%% · %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	containerLabel := tr("containers_running")
	if running == 1 {
		containerLabel = tr("container_running")
	}
	b.WriteString(fmt.Sprintf("\n🐳 %d %s", running, containerLabel))
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d %s", stopped, tr("containers_stopped")))
	}

	stressSummary := getStressSummary()
	if stressSummary != "" {
		b.WriteString(fmt.Sprintf("\n\n💨 *%s*\n", tr("report_stress")))
		b.WriteString(stressSummary)
	}

	b.WriteString(fmt.Sprintf("\n\n_⏱ Up for %s_", formatUptime(s.Uptime)))

	resetStressCounters()
	return b.String()
}

// generateAISummary calls Gemini API to generate a brief summary of the NAS status
func generateAISummary(s Stats, events []ReportEvent, isMorning bool) string {
	if cfg.GeminiAPIKey == "" {
		return ""
	}

	var context strings.Builder
	context.WriteString("NAS System Status:\n")
	context.WriteString(fmt.Sprintf("- CPU: %.1f%%\n", s.CPU))
	context.WriteString(fmt.Sprintf("- RAM: %.1f%%\n", s.RAM))
	if s.Swap > 5 {
		context.WriteString(fmt.Sprintf("- Swap: %.1f%%\n", s.Swap))
	}
	context.WriteString(fmt.Sprintf("- SSD: %.1f%% used, %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	context.WriteString(fmt.Sprintf("- HDD: %.1f%% used, %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))
	context.WriteString(fmt.Sprintf("- Uptime: %s\n", formatUptime(s.Uptime)))

	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	context.WriteString(fmt.Sprintf("- Docker: %d running, %d stopped\n", running, stopped))

	if len(events) > 0 {
		context.WriteString("\nRecent Events:\n")
		for _, e := range events {
			context.WriteString(fmt.Sprintf("- [%s] %s: %s\n", e.Time.In(location).Format("15:04"), e.Type, e.Message))
		}
	}

	timeOfDay := "morning"
	if !isMorning {
		timeOfDay = "evening"
	}

	lang := "English"
	if currentLanguage == "it" {
		lang = "Italian"
	}

	prompt := fmt.Sprintf(`You are a friendly NAS monitoring assistant. Based on the following system status, write a brief (1-2 sentences max) %s summary in %s. Be conversational and helpful. If everything is fine, be positive. If there are issues, mention them briefly. Do not use markdown formatting.

%s

Time: %s report`, timeOfDay, lang, context.String(), timeOfDay)

	summary := callGeminiAPI(prompt)
	return summary
}

// callGeminiAPI makes a request to the Gemini API
func callGeminiAPI(prompt string) string {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=%s", cfg.GeminiAPIKey)

	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 100,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		log.Printf("[Gemini] Error marshaling request: %v", err)
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("[Gemini] Error creating request: %v", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("[Gemini] Error calling API: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Gemini] API error (status %d): %s", resp.StatusCode, string(body))
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Gemini] Error reading response: %v", err)
		return ""
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
		log.Printf("[Gemini] Error parsing response: %v", err)
		return ""
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text)
	}

	return ""
}

func getHealthStatus(s Stats) (icon, text string, hasProblems bool) {
	reportEventsMutex.Lock()
	criticalCount := 0
	warningCount := 0
	for _, e := range reportEvents {
		if e.Type == "critical" {
			criticalCount++
		} else if e.Type == "warning" {
			warningCount++
		}
	}
	reportEventsMutex.Unlock()

	cpuCritical := cfg.Notifications.CPU.CriticalThreshold
	ramCritical := cfg.Notifications.RAM.CriticalThreshold
	ssdCritical := cfg.Notifications.DiskSSD.CriticalThreshold
	hddCritical := cfg.Notifications.DiskHDD.CriticalThreshold

	if criticalCount > 0 || s.CPU > cpuCritical || s.RAM > ramCritical || s.VolSSD.Used > ssdCritical || s.VolHDD.Used > hddCritical {
		return "⚠️", tr("health_critical"), true
	}

	cpuWarn := cfg.Notifications.CPU.WarningThreshold
	ramWarn := cfg.Notifications.RAM.WarningThreshold
	ssdWarn := cfg.Notifications.DiskSSD.WarningThreshold
	hddWarn := cfg.Notifications.DiskHDD.WarningThreshold
	ioWarn := cfg.Notifications.DiskIO.WarningThreshold

	if warningCount > 0 || s.CPU > cpuWarn*0.9 || s.RAM > ramWarn*0.95 || s.DiskUtil > ioWarn*0.95 || s.VolSSD.Used > ssdWarn || s.VolHDD.Used > hddWarn {
		return "👀", tr("health_warning"), true
	}
	return "✨", tr("health_ok"), false
}

// generateReport for manual requests (/report)
func generateReport(manual bool) string {
	if !manual {
		return generateDailyReport("> *Report NAS*", true)
	}

	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	b.WriteString("*Report*\n")
	b.WriteString(fmt.Sprintf("%s\n\n", now.Format("02/01 15:04")))

	healthIcon, healthText, _ := getHealthStatus(s)
	b.WriteString(fmt.Sprintf("%s %s\n\n", healthIcon, healthText))

	b.WriteString("*Resources*\n")
	b.WriteString(fmt.Sprintf("CPU %s %.1f%%\n", makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("RAM %s %.1f%% (%s free)\n", makeProgressBar(s.RAM), s.RAM, formatRAM(s.RAMFreeMB)))
	if s.DiskUtil > 5 {
		b.WriteString(fmt.Sprintf("I/O %s %.0f%%\n", makeProgressBar(s.DiskUtil), s.DiskUtil))
	}
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("Swap %s %.1f%%\n", makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\nSSD %.1f%% · %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("HDD %.1f%% · %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	b.WriteString(fmt.Sprintf("\nContainers: %d on · %d off\n", running, stopped))

	b.WriteString(fmt.Sprintf("\n_Up for %s_\n", formatUptime(s.Uptime)))

	reportEventsMutex.Lock()
	events := make([]ReportEvent, len(reportEvents))
	copy(events, reportEvents)
	reportEventsMutex.Unlock()

	if len(events) > 0 {
		b.WriteString("\n*Events*\n")
		for _, e := range events {
			icon := "."
			switch e.Type {
			case "warning", "critical":
				icon = "!"
			case "action":
				icon = ">"
			}
			b.WriteString(fmt.Sprintf("%s `%s` %s\n", icon, e.Time.In(location).Format("15:04"), truncate(e.Message, 24)))
		}
	}

	return b.String()
}

func addReportEvent(eventType, message string) {
	reportEventsMutex.Lock()
	defer reportEventsMutex.Unlock()

	reportEvents = append(reportEvents, ReportEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
	})

	if len(reportEvents) > 20 {
		reportEvents = reportEvents[len(reportEvents)-20:]
	}
}

func cleanOldReportEvents() {
	reportEventsMutex.Lock()
	defer reportEventsMutex.Unlock()

	cutoff := time.Now().Add(-24 * time.Hour)
	var newEvents []ReportEvent
	for _, e := range reportEvents {
		if e.Time.After(cutoff) {
			newEvents = append(newEvents, e)
		}
	}
	reportEvents = newEvents
}

// filterSignificantEvents removes trivial events
func filterSignificantEvents(events []ReportEvent) []ReportEvent {
	var filtered []ReportEvent
	for _, e := range events {
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "for 30s") || strings.Contains(msg, "for 1m") ||
			strings.Contains(msg, "after 30s") || strings.Contains(msg, "after 1m") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
}
