package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"nasbot/internal/format"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// ═══════════════════════════════════════════════════════════════════
//  TEXT GENERATORS (use cache, instant response)
// ═══════════════════════════════════════════════════════════════════

func getStatusText(ctx *AppContext) string {
	tr := ctx.Tr
	s, ready := ctx.Stats.Get()

	if !ready {
		return tr("loading")
	}

	var b strings.Builder
	now := time.Now().In(ctx.State.TimeLocation)

	b.WriteString(fmt.Sprintf(tr("status_title"), now.Format("15:04")))

	b.WriteString(fmt.Sprintf(tr("cpu_fmt"), format.MakeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf(tr("ram_fmt"), format.MakeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf(tr("swap_fmt"), format.MakeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf(tr("ssd_fmt"), s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf(tr("hdd_fmt"), s.VolHDD.Used, format.FormatBytes(s.VolHDD.Free)))

	if s.DiskUtil > 10 {
		b.WriteString(fmt.Sprintf(tr("disk_io_fmt"), s.DiskUtil))
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			b.WriteString(fmt.Sprintf(tr("disk_rw_fmt"), s.ReadMBs, s.WriteMBs))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(tr("uptime_fmt"), format.FormatUptime(s.Uptime)))

	return b.String()
}

func getTempText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("temp_title"))

	cpuTemp := readCPUTemp()
	cpuIcon, cpuStatus := cpuTempStatus(ctx, cpuTemp)
	b.WriteString(fmt.Sprintf(tr("temp_cpu"), cpuIcon, cpuTemp, cpuStatus))

	b.WriteString(tr("temp_disks"))
	for _, dev := range getSmartDevices(ctx) {
		temp, health := readDiskSMART(dev)
		icon, status := diskTempStatus(ctx, temp, health)
		b.WriteString(fmt.Sprintf("%s %s: %d°C — %s\n", icon, dev, temp, status))
	}
	return b.String()
}

func getNetworkText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("net_title"))

	localCtx, cancelLocal := context.WithTimeout(context.Background(), hostIPTimeout)
	defer cancelLocal()
	b.WriteString(fmt.Sprintf(tr("net_local"), getLocalIP(localCtx)))

	publicCtx, cancelPublic := context.WithTimeout(context.Background(), netTimeout)
	defer cancelPublic()
	b.WriteString(fmt.Sprintf(tr("net_public"), getPublicIP(ctx, publicCtx)))

	return b.String()
}

func getTopProcText(ctx *AppContext) string {
	tr := ctx.Tr
	reqCtx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return tr("top_none")
	}

	count := 0
	var b strings.Builder
	b.WriteString(tr("top_title"))
	b.WriteString(tr("top_header"))

	for i := 1; i < len(lines) && count < maxTopProcesses; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid := fields[0]
		cmdName := fields[1]
		cpuPct := fields[2]
		memPct := fields[3]

		if len(cmdName) > maxProcNameLen {
			cmdName = cmdName[:maxProcNameLen-2] + ".."
		}

		b.WriteString(fmt.Sprintf("`%-5s %-4s %-4s %s`\n",
			pid, cpuPct, memPct, cmdName))

		count++
	}

	return b.String()
}

func getHelpText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("help_intro"))

	b.WriteString(tr("help_mon"))
	b.WriteString("/status — quick system overview\n")
	b.WriteString("/quick — ultra-compact one-liner\n")
	b.WriteString("/temp — check temperatures\n")
	b.WriteString("/top — top processes by CPU\n")
	b.WriteString("/sysinfo — detailed system info\n")
	b.WriteString("/diskpred — disk space prediction\n\n")

	b.WriteString(tr("help_docker"))
	b.WriteString("/docker — manage containers\n")
	b.WriteString("/dstats — container resources\n")
	b.WriteString("/kill `name` — force kill container\n")
	b.WriteString("/logsearch `name` `keyword` — search logs\n")
	b.WriteString("/restartdocker — restart Docker service\n\n")

	b.WriteString(tr("help_net"))
	b.WriteString("/net — network info\n")
	b.WriteString("/speedtest — run speed test\n\n")

	b.WriteString(tr("help_settings"))
	b.WriteString("/settings — *configure everything*\n")
	b.WriteString("/report — full detailed report\n")
	b.WriteString("/ping — check if bot is alive\n")
	b.WriteString("/config — show current config\n")
	b.WriteString("/configjson — show full config.json (redacted)\n")
	b.WriteString("/configset <json> — update config.json\n")
	b.WriteString("/logs — recent system logs\n")
	b.WriteString("/ask <question> — ask AI about recent logs\n")
	b.WriteString("/reboot · /shutdown — power control\n")
	b.WriteString("/reboot force · /forcereboot — forced reboot (no confirm)\n\n")

	ctx.Settings.mu.RLock()
	reportMode := ctx.Settings.ReportMode
	reportMorning := ctx.Settings.ReportMorning
	reportEvening := ctx.Settings.ReportEvening
	quiet := ctx.Settings.QuietHours
	ctx.Settings.mu.RUnlock()

	if reportMode > 0 {
		b.WriteString(tr("help_reports"))
		if reportMode == 2 {
			b.WriteString(fmt.Sprintf("%02d:%02d & %02d:%02d_\n",
				reportMorning.Hour, reportMorning.Minute,
				reportEvening.Hour, reportEvening.Minute))
		} else {
			b.WriteString(fmt.Sprintf("%02d:%02d_\n", reportMorning.Hour, reportMorning.Minute))
		}
	}

	if quiet.Enabled {
		b.WriteString(fmt.Sprintf(tr("help_quiet"),
			quiet.Start.Hour, quiet.Start.Minute,
			quiet.End.Hour, quiet.End.Minute))
	}

	return b.String()
}

func getPingText(ctx *AppContext) string {
	tr := ctx.Tr
	ctx.Bot.mu.Lock()
	startTime := ctx.Bot.StartTime
	ctx.Bot.mu.Unlock()

	uptime := time.Since(startTime)

	_, ready := ctx.Stats.Get()

	status := "✅"
	statusText := tr("ping_ok")
	if !ready {
		status = "⚠️"
		statusText = tr("ping_not_ready")
	}

	now := time.Now().In(ctx.State.TimeLocation)
	return fmt.Sprintf(tr("ping_pong")+"\n\n"+
		"%s\n\n"+
		tr("ping_uptime")+"\n"+
		tr("ping_collecting")+"\n"+
		tr("ping_last_check")+"\n\n"+
		tr("ping_alive"),
		status,
		statusText,
		format.FormatDuration(uptime),
		ready,
		now.Format("15:04:05"))
}

func getConfigText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("config_title"))

	// Reports
	b.WriteString(tr("cfg_reports"))
	if ctx.Config.Reports.Enabled {
		if ctx.Config.Reports.Morning.Enabled && ctx.Config.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf(tr("cfg_both"),
				ctx.Config.Reports.Morning.Hour, ctx.Config.Reports.Morning.Minute,
				ctx.Config.Reports.Evening.Hour, ctx.Config.Reports.Evening.Minute))
		} else if ctx.Config.Reports.Morning.Enabled {
			b.WriteString(fmt.Sprintf(tr("cfg_morning_only"),
				ctx.Config.Reports.Morning.Hour, ctx.Config.Reports.Morning.Minute))
		} else if ctx.Config.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf(tr("cfg_evening_only"),
				ctx.Config.Reports.Evening.Hour, ctx.Config.Reports.Evening.Minute))
		} else {
			b.WriteString(tr("cfg_disabled"))
		}
	} else {
		b.WriteString(tr("cfg_disabled"))
	}

	// Quiet hours
	b.WriteString(tr("cfg_quiet"))
	if ctx.Config.QuietHours.Enabled {
		b.WriteString(fmt.Sprintf(tr("cfg_quiet_fmt"),
			ctx.Config.QuietHours.StartHour, ctx.Config.QuietHours.StartMinute,
			ctx.Config.QuietHours.EndHour, ctx.Config.QuietHours.EndMinute))
	} else {
		b.WriteString(tr("cfg_disabled"))
	}

	// Notifications
	b.WriteString("\n*Notifications:*\n")
	writeNotifLine := func(name string, rc ResourceConfig) {
		if rc.Enabled {
			if rc.CriticalThreshold > 0 {
				b.WriteString(fmt.Sprintf("  %s: ⚠️ >%.0f%% | 🚨 >%.0f%%\n", name, rc.WarningThreshold, rc.CriticalThreshold))
			} else {
				b.WriteString(fmt.Sprintf("  %s: ⚠️ >%.0f%%\n", name, rc.WarningThreshold))
			}
		} else {
			b.WriteString(fmt.Sprintf("  %s: ❌\n", name))
		}
	}
	writeNotifLine("CPU", ctx.Config.Notifications.CPU)
	writeNotifLine("RAM", ctx.Config.Notifications.RAM)
	writeNotifLine("Swap", ResourceConfig{Enabled: ctx.Config.Notifications.Swap.Enabled, WarningThreshold: ctx.Config.Notifications.Swap.WarningThreshold})
	writeNotifLine("SSD", ctx.Config.Notifications.DiskSSD)
	writeNotifLine("HDD", ctx.Config.Notifications.DiskHDD)
	writeNotifLine("I/O", ResourceConfig{Enabled: ctx.Config.Notifications.DiskIO.Enabled, WarningThreshold: ctx.Config.Notifications.DiskIO.WarningThreshold})
	b.WriteString(fmt.Sprintf("  SMART: %s\n", format.BoolToEmoji(ctx.Config.Notifications.SMART.Enabled)))

	// Docker
	b.WriteString("\n*Docker:*\n")
	if ctx.Config.Docker.Watchdog.Enabled {
		b.WriteString(fmt.Sprintf("  Watchdog: ✅ %dm timeout\n", ctx.Config.Docker.Watchdog.TimeoutMinutes))
	} else {
		b.WriteString("  Watchdog: ❌\n")
	}
	if ctx.Config.Docker.WeeklyPrune.Enabled {
		b.WriteString(fmt.Sprintf("  Prune: ✅ %s @ %02d:00\n",
			format.TitleCaseWord(ctx.Config.Docker.WeeklyPrune.Day), ctx.Config.Docker.WeeklyPrune.Hour))
	} else {
		b.WriteString("  Prune: ❌\n")
	}
	if ctx.Config.Docker.AutoRestartOnRAMCritical.Enabled {
		b.WriteString(fmt.Sprintf("  Auto-restart: ✅ RAM >%.0f%%\n",
			ctx.Config.Docker.AutoRestartOnRAMCritical.RAMThreshold))
	} else {
		b.WriteString("  Auto-restart: ❌\n")
	}

	// Network watchdog force reboot
	b.WriteString("\n*Network Watchdog:*\n")
	if ctx.Config.NetworkWatchdog.Enabled {
		if ctx.Config.NetworkWatchdog.ForceRebootOnDown {
			b.WriteString(fmt.Sprintf("  Force reboot: ✅ after %d min down\n", ctx.Config.NetworkWatchdog.ForceRebootAfterMins))
		} else {
			b.WriteString("  Force reboot: ❌\n")
		}
	} else {
		b.WriteString("  Enabled: ❌\n")
	}

	// Intervals
	b.WriteString(fmt.Sprintf("\n*Intervals:* Stats %ds · Monitor %ds",
		ctx.Config.Intervals.StatsSeconds, ctx.Config.Intervals.MonitorSeconds))

	return b.String()
}

// getSysInfoText returns detailed system information
func getSysInfoText(ctx *AppContext) string {
	var b strings.Builder
	b.WriteString("🖥 *System Information*\n\n")

	// Host info
	h, err := host.Info()
	if err == nil {
		b.WriteString(fmt.Sprintf("*Hostname:* `%s`\n", h.Hostname))
		b.WriteString(fmt.Sprintf("*OS:* %s %s\n", h.Platform, h.PlatformVersion))
		b.WriteString(fmt.Sprintf("*Kernel:* %s\n", h.KernelVersion))
		b.WriteString(fmt.Sprintf("*Architecture:* %s\n", h.KernelArch))
		b.WriteString(fmt.Sprintf("*Uptime:* %s\n", format.FormatUptime(h.Uptime)))
		b.WriteString(fmt.Sprintf("*Boot Time:* %s\n", time.Unix(int64(h.BootTime), 0).In(ctx.State.TimeLocation).Format("02/01/2006 15:04")))
	}

	// CPU info
	cpuInfo, err := cpu.Info()
	if err == nil && len(cpuInfo) > 0 {
		b.WriteString(fmt.Sprintf("\n*CPU:* %s\n", cpuInfo[0].ModelName))
		b.WriteString(fmt.Sprintf("*Cores:* %d physical, %d logical\n", cpuInfo[0].Cores, len(cpuInfo)))
		if cpuInfo[0].Mhz > 0 {
			b.WriteString(fmt.Sprintf("*Frequency:* %.0f MHz\n", cpuInfo[0].Mhz))
		}
	}

	// Memory info
	v, err := mem.VirtualMemory()
	if err == nil {
		b.WriteString(fmt.Sprintf("\n*Total RAM:* %.1f GB\n", float64(v.Total)/1024/1024/1024))
	}

	// Disk info
	paths := []struct {
		name string
		path string
	}{
		{name: "SSD", path: ctx.Config.Paths.SSD},
		{name: "HDD", path: ctx.Config.Paths.HDD},
	}
	for _, p := range paths {
		if p.path == "" {
			continue
		}
		d, err := disk.Usage(p.path)
		if err == nil {
			b.WriteString(fmt.Sprintf("*%s (%s):* %.0f GB total\n", p.name, p.path, float64(d.Total)/1024/1024/1024))
		}
	}

	// Go runtime info
	b.WriteString("\n*NASBot Version:* 1.0.0\n")
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(fmt.Sprintf("*Go Version:* %s\n", buildInfo.GoVersion))
	}

	return b.String()
}
