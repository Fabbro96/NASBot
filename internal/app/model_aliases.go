package app

import pmodel "nasbot/pkg/model"

type AppContext = pmodel.AppContext
type BotAPI = pmodel.BotAPI
type TimePoint = pmodel.TimePoint
type QuietSettings = pmodel.QuietSettings
type PruneSettings = pmodel.PruneSettings
type ThreadSafeStats = pmodel.ThreadSafeStats
type RuntimeState = pmodel.RuntimeState
type BotContext = pmodel.BotContext
type DockerManager = pmodel.DockerManager
type MonitorState = pmodel.MonitorState
type UserSettings = pmodel.UserSettings
type HealthchecksState = pmodel.HealthchecksState
type DowntimeLog = pmodel.DowntimeLog
type DiskMountInfo = pmodel.DiskMountInfo

func InitApp(cfg *Config) *AppContext {
	return pmodel.InitApp(cfg)
}

// The published configuration is read through AppContext.Cfg() and the user
// settings through the UserSettings accessors. Both are the only sanctioned
// seams: Cfg returns an immutable snapshot (a reload publishes a new one) and
// each accessor takes the lock exactly once, so no call site can nest a lock
// around another accessor and dead-lock.
