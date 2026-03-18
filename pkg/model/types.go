package model

import (
	imodel "nasbot/internal/model"
	"time"
)

// Aliases to shared internal model types.
type VolumeStats = imodel.VolumeStats
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
