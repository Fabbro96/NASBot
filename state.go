package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"time"
)

// BotState for persistence (DTO)
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

	// Healthchecks.io tracking
	Healthchecks HealthchecksState `json:"healthchecks"`
}

// Ensure HealthchecksState and DowntimeLog are defined here or in types.go
// If they are in types.go, I shouldn't redefine them unless they were only in state.go.
// They were in state.go in the original file, so I need them here.
type HealthchecksState struct {
	TotalPings      int           `json:"total_pings"`
	SuccessfulPings int           `json:"successful_pings"`
	FailedPings     int           `json:"failed_pings"`
	LastPingTime    time.Time     `json:"last_ping_time"`
	LastPingSuccess bool          `json:"last_ping_success"`
	LastFailure     time.Time     `json:"last_failure"`
	DowntimeEvents  []DowntimeLog `json:"downtime_events"`
}

type DowntimeLog struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Duration  string    `json:"duration"`
	Reason    string    `json:"reason"`
}

const StateFile = "nasbot_state.json"

func loadState(ctx *AppContext) {
	data, err := os.ReadFile(StateFile)
	if err != nil {
		slog.Info("First run - no state found")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("State load error", "err", err)
		return
	}

	ctx.State.mu.Lock()
	ctx.State.LastReport = state.LastReportTime
	ctx.State.mu.Unlock()

	ctx.Docker.mu.Lock()
	if state.AutoRestarts != nil {
		ctx.Docker.AutoRestarts = state.AutoRestarts
	}
	ctx.Docker.mu.Unlock()

	ctx.Monitor.mu.Lock()
	ctx.Monitor.Healthchecks = state.Healthchecks
	ctx.Monitor.mu.Unlock()

	ctx.Settings.mu.Lock()
	if state.Language != "" {
		ctx.Settings.Language = state.Language
	}
	if state.ReportMode > 0 {
		ctx.Settings.ReportMode = state.ReportMode
	}

	if state.ReportMorningHour > 0 || state.ReportMorningMinute > 0 {
		ctx.Settings.ReportMorning = TimePoint{Hour: state.ReportMorningHour, Minute: state.ReportMorningMinute}
	}
	if state.ReportEveningHour > 0 || state.ReportEveningMinute > 0 {
		ctx.Settings.ReportEvening = TimePoint{Hour: state.ReportEveningHour, Minute: state.ReportEveningMinute}
	}

	if state.QuietStartHour > 0 || state.QuietStartMinute > 0 {
		ctx.Settings.QuietHours.Enabled = state.QuietHoursEnabled
		ctx.Settings.QuietHours.Start = TimePoint{Hour: state.QuietStartHour, Minute: state.QuietStartMinute}
		ctx.Settings.QuietHours.End = TimePoint{Hour: state.QuietEndHour, Minute: state.QuietEndMinute}
	}

	if state.DockerPruneDay != "" {
		ctx.Settings.DockerPrune.Enabled = state.DockerPruneEnabled
		ctx.Settings.DockerPrune.Day = state.DockerPruneDay
		ctx.Settings.DockerPrune.Hour = state.DockerPruneHour
	}
	ctx.Settings.mu.Unlock()
}

func saveState(ctx *AppContext) {
	ctx.State.mu.Lock()
	lastReport := ctx.State.LastReport
	ctx.State.mu.Unlock()

	ctx.Docker.mu.RLock()
	autoRestarts := ctx.Docker.AutoRestarts
	ctx.Docker.mu.RUnlock()

	ctx.Monitor.mu.Lock()
	healthchecks := ctx.Monitor.Healthchecks
	ctx.Monitor.mu.Unlock()

	ctx.Settings.mu.RLock()
	settings := ctx.Settings
	ctx.Settings.mu.RUnlock()

	state := BotState{
		LastReportTime:      lastReport,
		AutoRestarts:        autoRestarts,
		Language:            settings.Language,
		ReportMode:          settings.ReportMode,
		ReportMorningHour:   settings.ReportMorning.Hour,
		ReportMorningMinute: settings.ReportMorning.Minute,
		ReportEveningHour:   settings.ReportEvening.Hour,
		ReportEveningMinute: settings.ReportEvening.Minute,
		QuietHoursEnabled:   settings.QuietHours.Enabled,
		QuietStartHour:      settings.QuietHours.Start.Hour,
		QuietStartMinute:    settings.QuietHours.Start.Minute,
		QuietEndHour:        settings.QuietHours.End.Hour,
		QuietEndMinute:      settings.QuietHours.End.Minute,
		DockerPruneEnabled:  settings.DockerPrune.Enabled,
		DockerPruneDay:      settings.DockerPrune.Day,
		DockerPruneHour:     settings.DockerPrune.Hour,
		Healthchecks:        healthchecks,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		slog.Error("State marshal error", "err", err)
		return
	}

	if err := os.WriteFile(StateFile, data, 0644); err != nil {
		slog.Error("State save error", "err", err)
	}
}
