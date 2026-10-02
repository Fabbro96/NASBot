package app

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nasbot/pkg/model"
)

// BotState for persistence (DTO)
type BotState struct {
	LastReportTime      time.Time              `json:"last_report_time"`
	AutoRestarts        map[string][]time.Time `json:"auto_restarts"`
	ReportEvents        []ReportEvent          `json:"report_events,omitempty"`
	LastReleaseNotified string                 `json:"last_release_notified,omitempty"`
	Language            string                 `json:"language"`

	// Deprecated: Migrating to new reports configuration.
	//
	// These are pointers on purpose. They used to be plain ints, so a report
	// legitimately scheduled at 00:00 was indistinguishable from "never
	// configured" and the migration replaced it with the 07:30 default. A
	// pointer tells "the key was absent" from "the key was zero".
	ReportMode          *int `json:"report_mode,omitempty"`
	ReportMorningHour   *int `json:"report_morning_hour,omitempty"`
	ReportMorningMinute *int `json:"report_morning_minute,omitempty"`
	ReportEveningHour   *int `json:"report_evening_hour,omitempty"`
	ReportEveningMinute *int `json:"report_evening_minute,omitempty"`

	// New Reports configuration
	ReportsEnabled *bool       `json:"reports_enabled,omitempty"`
	ReportInterval int         `json:"report_interval,omitempty"`
	ReportTimes    []TimePoint `json:"report_times,omitempty"`
	ReportDays     []int       `json:"report_days,omitempty"`

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

func stateFilePath() string {
	if p := os.Getenv("NASBOT_STATE_FILE"); p != "" {
		return p
	}
	return filepath.Join("var", "nasbot_state.json")
}

func loadState(ctx *AppContext) {
	statePath := stateFilePath()
	data, err := os.ReadFile(statePath)
	if err != nil {
		slog.Info("First run - no state found")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		slog.Warn("State load error", "err", err)
		return
	}

	ctx.State.Mu.Lock()
	ctx.State.LastReport = state.LastReportTime
	ctx.State.LastReleaseNotified = state.LastReleaseNotified
	if len(state.ReportEvents) > 100 {
		ctx.State.ReportEvents = append([]ReportEvent{}, state.ReportEvents[len(state.ReportEvents)-100:]...)
	} else if len(state.ReportEvents) > 0 {
		ctx.State.ReportEvents = append([]ReportEvent{}, state.ReportEvents...)
	}
	ctx.State.Mu.Unlock()

	ctx.Docker.Mu.Lock()
	if state.AutoRestarts != nil {
		ctx.Docker.AutoRestarts = state.AutoRestarts
	}
	ctx.Docker.Mu.Unlock()

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.Healthchecks = state.Healthchecks
	ctx.Monitor.Mu.Unlock()

	// Settings go through the accessors: each one takes the write lock exactly
	// once and no lock is ever held across another accessor.
	if state.Language != "" {
		if _, ok := supportedLanguageByCode[state.Language]; ok {
			ctx.Settings.SetLanguage(state.Language)
		} else {
			// An unknown language in state.json used to be copied verbatim, so
			// every Tr() call fell back to English while the /settings keyboard
			// still highlighted the button that had been pressed.
			slog.Warn("State carries an unsupported language, keeping the current one", "lang", state.Language)
		}
	}

	// Migration logic
	switch {
	case state.ReportsEnabled != nil:
		// The three schedule fields move as a unit, so a concurrent reader never
		// sees a half-migrated schedule.
		_, interval, times := ctx.Settings.GetReportsSettings()
		if state.ReportInterval > 0 {
			interval = state.ReportInterval
		}
		if len(state.ReportTimes) > 0 {
			times = state.ReportTimes
		}
		ctx.Settings.SetReportsSettings(*state.ReportsEnabled, interval, times)
		if len(state.ReportDays) > 0 {
			ctx.Settings.SetReportsDays(state.ReportDays)
		}
	case state.ReportMode != nil:
		// Legacy state file: the reports schedule lived in the four deprecated
		// fields. 0 = off, 1 = morning only, 2 = morning and evening.
		mode := *state.ReportMode
		times := make([]TimePoint, 0, 2)
		if mode >= 1 {
			times = append(times, legacyReportTime(state.ReportMorningHour, state.ReportMorningMinute, 7, 30))
		}
		if mode == 2 {
			times = append(times, legacyReportTime(state.ReportEveningHour, state.ReportEveningMinute, 18, 30))
		}
		ctx.Settings.SetReportsSettings(mode > 0, 1, times)
	}
	// No reports key at all: leave whatever the config seeded, instead of
	// overwriting it with a migration from a file that never had one.

	if state.QuietHoursEnabled || state.QuietStartHour > 0 || state.QuietStartMinute > 0 || state.QuietEndHour > 0 || state.QuietEndMinute > 0 {
		ctx.Settings.SetQuietHours(QuietSettings{
			Enabled: state.QuietHoursEnabled,
			Start:   TimePoint{Hour: state.QuietStartHour, Minute: state.QuietStartMinute},
			End:     TimePoint{Hour: state.QuietEndHour, Minute: state.QuietEndMinute},
		})
	}

	// The weekday is checked against the closed set instead of being run through
	// normalizeDay: that helper also reports "changed" for a mere case
	// difference, so "Monday" would have been dropped along with a real typo.
	day := strings.ToLower(strings.TrimSpace(state.DockerPruneDay))
	switch {
	case pruneWeekdays[day]:
		ctx.Settings.SetDockerPrune(PruneSettings{Enabled: state.DockerPruneEnabled, Day: day, Hour: state.DockerPruneHour})
	case state.DockerPruneDay != "":
		slog.Warn("State carries an unknown prune weekday, keeping the config default", "day", state.DockerPruneDay)
	}
}

// legacyReportTime rebuilds one report time from the deprecated fields.
//
// An absent or out of range pair falls back to the default. A pair that is
// present and valid is used as is, including 00:00, which the previous
// `> 0` test silently discarded.
func legacyReportTime(hour, minute *int, defHour, defMinute int) TimePoint {
	if hour == nil || minute == nil {
		return TimePoint{Hour: defHour, Minute: defMinute}
	}
	h, m := *hour, *minute
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return TimePoint{Hour: defHour, Minute: defMinute}
	}
	return TimePoint{Hour: h, Minute: m}
}

// stateSaveMu serialises whole saves.
//
// saveState has about ten call sites and several run inside goroutines (power
// lifecycle, post-update save, the report scheduler). With a fixed
// statePath + ".tmp" two concurrent saves raced: the loser's rename failed with
// ENOENT and, worse, the file could end up holding one writer's truncation of
// another's JSON, silently losing LastReportTime, Language or ReportTimes.
var stateSaveMu model.Mutex

func saveState(ctx *AppContext) {
	stateSaveMu.Lock()
	defer stateSaveMu.Unlock()

	ctx.State.Mu.Lock()
	lastReport := ctx.State.LastReport
	lastReleaseNotified := ctx.State.LastReleaseNotified
	ctx.State.Mu.Unlock()
	reportEvents := ctx.State.GetEvents()

	ctx.Docker.Mu.RLock()
	autoRestarts := make(map[string][]time.Time, len(ctx.Docker.AutoRestarts))
	for k, v := range ctx.Docker.AutoRestarts {
		vv := make([]time.Time, len(v))
		copy(vv, v)
		autoRestarts[k] = vv
	}
	ctx.Docker.Mu.RUnlock()

	ctx.Monitor.Mu.Lock()
	healthchecks := ctx.Monitor.Healthchecks
	if len(ctx.Monitor.Healthchecks.DowntimeEvents) > 0 {
		downtimeCopy := make([]DowntimeLog, len(ctx.Monitor.Healthchecks.DowntimeEvents))
		copy(downtimeCopy, ctx.Monitor.Healthchecks.DowntimeEvents)
		healthchecks.DowntimeEvents = downtimeCopy
	}
	ctx.Monitor.Mu.Unlock()

	quiet := ctx.Settings.GetQuietHours()
	prune := ctx.Settings.GetDockerPrune()
	ctx.Settings.Mu.RLock()
	language := ctx.Settings.Language
	reportsEnabled := ctx.Settings.ReportsEnabled
	reportInterval := ctx.Settings.ReportInterval
	reportTimes := make([]TimePoint, len(ctx.Settings.ReportTimes))
	copy(reportTimes, ctx.Settings.ReportTimes)
	reportDays := make([]int, len(ctx.Settings.ReportDays))
	copy(reportDays, ctx.Settings.ReportDays)
	ctx.Settings.Mu.RUnlock()

	state := BotState{
		LastReportTime:      lastReport,
		AutoRestarts:        autoRestarts,
		ReportEvents:        reportEvents,
		LastReleaseNotified: lastReleaseNotified,
		Language:            language,
		ReportsEnabled:      &reportsEnabled,
		ReportInterval:      reportInterval,
		ReportTimes:         reportTimes,
		ReportDays:          reportDays,
		QuietHoursEnabled:   quiet.Enabled,
		QuietStartHour:      quiet.Start.Hour,
		QuietStartMinute:    quiet.Start.Minute,
		QuietEndHour:        quiet.End.Hour,
		QuietEndMinute:      quiet.End.Minute,
		DockerPruneEnabled:  prune.Enabled,
		DockerPruneDay:      prune.Day,
		DockerPruneHour:     prune.Hour,
		Healthchecks:        healthchecks,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		slog.Error("State marshal error", "err", err)
		return
	}

	statePath := stateFilePath()
	ensureParentDir(statePath)

	mode := os.FileMode(0o600)
	if st, statErr := os.Stat(statePath); statErr == nil {
		mode = st.Mode().Perm()
	} else if !os.IsNotExist(statErr) {
		slog.Warn("State save warning (stat existing file)", "err", statErr)
	}

	// Atomic write: a unique temporary file in the same directory, then rename.
	// Same directory because rename is only atomic within a filesystem, and
	// unique because os.CreateTemp never collides with a concurrent writer.
	tmp, err := os.CreateTemp(filepath.Dir(statePath), filepath.Base(statePath)+".tmp-*")
	if err != nil {
		slog.Error("State save error (tmp create)", "err", err)
		return
	}
	tmpPath := tmp.Name()
	// Harmless after a successful rename, where the name no longer exists.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := tmp.Chmod(mode); err != nil {
		slog.Warn("State save warning (tmp chmod)", "err", err)
	}
	if _, err := tmp.Write(data); err != nil {
		slog.Error("State save error (tmp write)", "err", err)
		_ = tmp.Close()
		return
	}
	// Without the sync the rename can land before the data does, and a crash
	// then leaves a truncated state file behind.
	if err := tmp.Sync(); err != nil {
		slog.Warn("State save warning (tmp sync)", "err", err)
	}
	if err := tmp.Close(); err != nil {
		slog.Error("State save error (tmp close)", "err", err)
		return
	}
	if err := os.Rename(tmpPath, statePath); err != nil {
		slog.Error("State save error (rename)", "err", err)
	}
}
