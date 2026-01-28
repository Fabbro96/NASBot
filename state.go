package main

import (
	"encoding/json"
	"log"
	"os"
	"time"
)

// BotState for persistence
type BotState struct {
	LastReportTime time.Time              `json:"last_report_time"`
	AutoRestarts   map[string][]time.Time `json:"auto_restarts"`
	Language       string                 `json:"language"`
	ReportMode     int                    `json:"report_mode"` // 0=disabled, 1=once, 2=twice

	// User-configurable settings
	ReportMorningHour   int `json:"report_morning_hour"`
	ReportMorningMinute int `json:"report_morning_minute"`
	ReportEveningHour   int `json:"report_evening_hour"`
	ReportEveningMinute int `json:"report_evening_minute"`

	QuietHoursEnabled bool `json:"quiet_hours_enabled"`
	QuietStartHour    int  `json:"quiet_start_hour"`
	QuietStartMinute  int  `json:"quiet_start_minute"`
	QuietEndHour      int  `json:"quiet_end_hour"`
	QuietEndMinute    int  `json:"quiet_end_minute"`

	DockerPruneEnabled bool   `json:"docker_prune_enabled"`
	DockerPruneDay     string `json:"docker_prune_day"`
	DockerPruneHour    int    `json:"docker_prune_hour"`
}

func loadState() {
	data, err := os.ReadFile(DefaultStateFile)
	if err != nil {
		log.Printf("[i] First run - no state")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[w] State error: %v", err)
		return
	}
	lastReportTime = state.LastReportTime
	if state.AutoRestarts != nil {
		autoRestarts = state.AutoRestarts
	}
	if state.Language != "" {
		currentLanguage = state.Language
	}
	if state.ReportMode > 0 {
		reportMode = state.ReportMode
	}

	// Load user-configurable settings
	if state.ReportMorningHour > 0 || state.ReportMorningMinute > 0 {
		reportMorningHour = state.ReportMorningHour
		reportMorningMinute = state.ReportMorningMinute
	}
	if state.ReportEveningHour > 0 || state.ReportEveningMinute > 0 {
		reportEveningHour = state.ReportEveningHour
		reportEveningMinute = state.ReportEveningMinute
	}

	if state.QuietStartHour > 0 || state.QuietStartMinute > 0 {
		quietHoursEnabled = state.QuietHoursEnabled
		quietStartHour = state.QuietStartHour
		quietStartMinute = state.QuietStartMinute
		quietEndHour = state.QuietEndHour
		quietEndMinute = state.QuietEndMinute
	}

	if state.DockerPruneDay != "" {
		dockerPruneEnabled = state.DockerPruneEnabled
		dockerPruneDay = state.DockerPruneDay
		dockerPruneHour = state.DockerPruneHour
	}

	log.Printf("[+] State restored")
}

func saveState() {
	autoRestartsMutex.Lock()
	state := BotState{
		LastReportTime:      lastReportTime,
		AutoRestarts:        autoRestarts,
		Language:            currentLanguage,
		ReportMode:          reportMode,
		ReportMorningHour:   reportMorningHour,
		ReportMorningMinute: reportMorningMinute,
		ReportEveningHour:   reportEveningHour,
		ReportEveningMinute: reportEveningMinute,
		QuietHoursEnabled:   quietHoursEnabled,
		QuietStartHour:      quietStartHour,
		QuietStartMinute:    quietStartMinute,
		QuietEndHour:        quietEndHour,
		QuietEndMinute:      quietEndMinute,
		DockerPruneEnabled:  dockerPruneEnabled,
		DockerPruneDay:      dockerPruneDay,
		DockerPruneHour:     dockerPruneHour,
	}
	autoRestartsMutex.Unlock()

	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("[w] Serialize: %v", err)
		return
	}
	if err := os.WriteFile(DefaultStateFile, data, 0644); err != nil {
		log.Printf("[w] Save: %v", err)
	}
}

// isQuietHours checks if we are in quiet hours based on config
func isQuietHours() bool {
	if !quietHoursEnabled {
		return false
	}

	now := time.Now().In(location)
	hour := now.Hour()
	minute := now.Minute()
	currentMins := hour*60 + minute

	startMins := quietStartHour*60 + quietStartMinute
	endMins := quietEndHour*60 + quietEndMinute

	// Handle overnight quiet hours (e.g., 23:30 - 07:00)
	if startMins > endMins {
		return currentMins >= startMins || currentMins < endMins
	}
	return currentMins >= startMins && currentMins < endMins
}
