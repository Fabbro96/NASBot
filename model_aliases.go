package main

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

func InitApp(cfg *Config) *AppContext {
	return pmodel.InitApp(cfg)
}
