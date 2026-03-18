package main

import pcommands "nasbot/pkg/commands"

type CommandRegistry = pcommands.CommandRegistry

func NewCommandRegistry() *CommandRegistry { return pcommands.NewCommandRegistry() }

type StatusCmd = pcommands.StatusCmd
type TopCmd = pcommands.TopCmd
type SysInfoCmd = pcommands.SysInfoCmd
type TempCmd = pcommands.TempCmd
type PowerCmd = pcommands.PowerCmd
type DockerMenuCmd = pcommands.DockerMenuCmd
type DockerStatsCmd = pcommands.DockerStatsCmd
type ContainerCmd = pcommands.ContainerCmd
type RestartDockerCmd = pcommands.RestartDockerCmd
type KillCmd = pcommands.KillCmd
type NetCmd = pcommands.NetCmd
type SpeedtestCmd = pcommands.SpeedtestCmd
type PingCmd = pcommands.PingCmd
type LogsCmd = pcommands.LogsCmd
type LogSearchCmd = pcommands.LogSearchCmd
type AskCmd = pcommands.AskCmd
type HelpCmd = pcommands.HelpCmd
type QuickCmd = pcommands.QuickCmd
type DiskPredCmd = pcommands.DiskPredCmd
type HealthCmd = pcommands.HealthCmd
type UpdateCmd = pcommands.UpdateCmd
type ReportCmd = pcommands.ReportCmd
type ConfigCmd = pcommands.ConfigCmd
type ConfigJSONCmd = pcommands.ConfigJSONCmd
type ConfigSetCmd = pcommands.ConfigSetCmd
type LanguageCmd = pcommands.LanguageCmd
type SettingsCmd = pcommands.SettingsCmd

func getStatusText(ctx *AppContext) string  { return pcommands.GetStatusText(ctx) }
func getTempText(ctx *AppContext) string    { return pcommands.GetTempText(ctx) }
func getTopProcText(ctx *AppContext) string { return pcommands.GetTopProcText(ctx) }
func getNetworkText(ctx *AppContext) string { return pcommands.GetNetworkText(ctx) }
func getHelpText(ctx *AppContext) string    { return pcommands.GetHelpText(ctx) }
func getPingText(ctx *AppContext) string    { return pcommands.GetPingText(ctx) }
func getConfigText(ctx *AppContext) string  { return pcommands.GetConfigText(ctx) }
func getQuickText(ctx *AppContext) string   { return pcommands.GetQuickText(ctx) }
func getDiskPredictionText(ctx *AppContext) string {
	return pcommands.GetDiskPredictionText(ctx)
}
func recordDiskUsage(ctx *AppContext) { pcommands.RecordDiskUsage(ctx) }
