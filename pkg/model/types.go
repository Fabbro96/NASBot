package model

import (
	imodel "nasbot/internal/model"
	"time"
)

// Aliases to shared internal model types.
type VolumeStats = imodel.VolumeStats
type DiskMountInfo = imodel.DiskMountInfo
type Stats = imodel.Stats
type ProcInfo = imodel.ProcInfo
type ContainerInfo = imodel.ContainerInfo
type DiskUsagePoint = imodel.DiskUsagePoint
type ReportEvent = imodel.ReportEvent
type StressTracker = imodel.StressTracker
type DiskPrediction = imodel.DiskPrediction
type TrendPoint = imodel.TrendPoint
type DockerCache = imodel.DockerCache

// HealthchecksState tracks healthchecks.io metrics and downtime history.
type HealthchecksState struct {
	TotalPings              int       `json:"total_pings"`
	SuccessfulPings         int       `json:"successful_pings"`
	FailedPings             int       `json:"failed_pings"`
	ReportBaseTotal         int       `json:"report_base_total"`
	ReportBaseSuccessful    int       `json:"report_base_successful"`
	LastPingTime            time.Time `json:"last_ping_time"`
	LastPingSuccess         bool      `json:"last_ping_success"`
	LastFailure             time.Time `json:"last_failure"`
	NetForceRebootTriggered bool      `json:"net_force_reboot_triggered"`
	// LastForcedReboot is the instant of the last forced reboot the healthchecks
	// watchdog caused.
	//
	// It is the explicit, purpose-built answer to "has this bot already rebooted
	// for the outage in progress?". The same question used to be answered only by
	// parsing the Reason prefix of a downtime event, which loses its evidence two
	// ways: the log is capped (MaxDowntimeEvents) and it is readable text, so
	// anything that rewrites it — /health history clear — silently re-arms the
	// cycle.
	//
	// It lives here and not on BotState because BotState.Healthchecks is copied
	// wholesale by saveState/loadState, so the field is persisted with no other
	// change. A state file without the key decodes to the zero time, and the
	// readers fall back to the log-derived value.
	LastForcedReboot time.Time     `json:"last_forced_reboot"`
	DowntimeEvents   []DowntimeLog `json:"downtime_events"`
}

type DowntimeLog struct {
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Duration  string    `json:"duration"`
	Reason    string    `json:"reason"`
}
