package commands

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
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

	var sections []string
	now := time.Now().In(ctx.State.TimeLocation)

	// Section 1: Header
	sections = append(sections, fmt.Sprintf(tr("status_title"), now.Format("15:04")))

	// Section 2: Compute (CPU, RAM, Swap)
	var computeLines []string
	cpuGraph, ramGraph := getTrendSummary(ctx)

	cpuLine := fmt.Sprintf(tr("cpu_fmt"), format.MakeProgressBar(s.CPU), s.CPU)
	if cpuGraph != "" {
		cpuLine += "  `" + cpuGraph + "`"
	}
	computeLines = append(computeLines, cpuLine)

	ramLine := fmt.Sprintf(tr("ram_fmt"), format.MakeProgressBar(s.RAM), s.RAM)
	if ramGraph != "" {
		ramLine += "  `" + ramGraph + "`"
	}
	computeLines = append(computeLines, ramLine)

	if s.Swap > 5 {
		computeLines = append(computeLines, fmt.Sprintf(tr("swap_fmt"), format.MakeProgressBar(s.Swap), s.Swap))
	}
	sections = append(sections, strings.Join(computeLines, "\n"))

	// Section 3: Storage (SSD & Secondary Disks)
	var storageLines []string
	storageLines = append(storageLines, fmt.Sprintf(tr("ssd_fmt"), s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	secKeys := make([]string, 0, len(s.SecondaryVols))
	for m := range s.SecondaryVols {
		secKeys = append(secKeys, m)
	}
	sort.Strings(secKeys)
	for _, m := range secKeys {
		vol := s.SecondaryVols[m]
		shortName := escapeMarkdown(mountShortName(m))
		storageLines = append(storageLines, fmt.Sprintf(tr("disk_sec_fmt"), shortName, vol.Used, format.FormatBytes(vol.Free)))
	}
	sections = append(sections, strings.Join(storageLines, "\n"))

	// Section 4: Disk I/O (if active)
	if s.DiskUtil > 10 {
		ioLine := fmt.Sprintf(tr("disk_io_fmt"), s.DiskUtil)
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			ioLine += fmt.Sprintf(tr("disk_rw_fmt"), s.ReadMBs, s.WriteMBs)
		}
		sections = append(sections, ioLine)
	}

	// Section 5: Docker Container Summary & Uptime footer
	var footerLines []string
	containers := getCachedContainerList(ctx)
	if len(containers) > 0 {
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
		if stopped > 0 {
			footerLines = append(footerLines, fmt.Sprintf("🐳 %d %s · %d %s", running, containerLabel, stopped, tr("containers_stopped")))
		} else {
			footerLines = append(footerLines, fmt.Sprintf("🐳 %d %s", running, containerLabel))
		}
	}
	footerLines = append(footerLines, fmt.Sprintf(tr("uptime_fmt"), format.FormatUptime(s.Uptime)))
	sections = append(sections, strings.Join(footerLines, "\n"))

	return strings.Join(sections, "\n\n")
}

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "_", "\\_")
	s = strings.ReplaceAll(s, "*", "\\*")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "[", "\\[")
	return s
}

func GetStatusText(ctx *AppContext) string { return getStatusText(ctx) }

func getTempText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("temp_title"))

	cpuTemp := readCPUTemp()
	cpuIcon, cpuStatus := cpuTempStatus(ctx, cpuTemp)
	if cpuTemp <= 0 {
		b.WriteString(fmt.Sprintf("%s CPU: N/A — %s\n\n", cpuIcon, cpuStatus))
	} else {
		b.WriteString(fmt.Sprintf(tr("temp_cpu"), cpuIcon, cpuTemp, cpuStatus))
	}

	b.WriteString(tr("temp_disks"))
	for _, dev := range getSmartDevices(ctx) {
		temp, health := readDiskSMART(dev)
		icon, status := diskTempStatus(ctx, temp, health)
		if temp < 0 {
			b.WriteString(fmt.Sprintf("%s %s: N/A — %s\n", icon, dev, status))
		} else {
			b.WriteString(fmt.Sprintf("%s %s: %d°C — %s\n", icon, dev, temp, status))
		}
	}
	return b.String()
}

func GetTempText(ctx *AppContext) string { return getTempText(ctx) }

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

	s, ready := ctx.Stats.Get()
	if ready {
		b.WriteString(tr("net_traffic_title"))
		b.WriteString(fmt.Sprintf(tr("net_rx"), s.NetRxMbps, formatData(s.NetRxTotalMB)))
		b.WriteString(fmt.Sprintf(tr("net_tx"), s.NetTxMbps, formatData(s.NetTxTotalMB)))
	}

	return b.String()
}

func formatData(mb float64) string {
	if mb > 1024 {
		return fmt.Sprintf("%.2f GB", mb/1024)
	}
	return fmt.Sprintf("%.2f MB", mb)
}

func GetNetworkText(ctx *AppContext) string { return getNetworkText(ctx) }

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

func GetTopProcText(ctx *AppContext) string { return getTopProcText(ctx) }

func getHelpText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("help_intro"))

	b.WriteString(tr("help_mon"))
	b.WriteString(fmt.Sprintf("/status — %s\n", tr("cmd_status_desc")))
	b.WriteString(fmt.Sprintf("/quick — %s\n", tr("cmd_quick_desc")))
	b.WriteString(fmt.Sprintf("/temp — %s\n", tr("cmd_temp_desc")))
	b.WriteString(fmt.Sprintf("/top — %s\n", tr("cmd_top_desc")))
	b.WriteString(fmt.Sprintf("/sysinfo — %s\n", tr("cmd_sysinfo_desc")))
	b.WriteString(fmt.Sprintf("/diskpred — %s\n\n", tr("cmd_diskpred_desc")))

	b.WriteString(tr("help_docker"))
	b.WriteString(fmt.Sprintf("/docker — %s\n", tr("cmd_docker_desc")))
	b.WriteString(fmt.Sprintf("/dstats — %s\n", tr("cmd_dstats_desc")))
	b.WriteString(fmt.Sprintf("/kill `name` — %s\n", tr("cmd_kill_desc")))
	b.WriteString(fmt.Sprintf("/logsearch `name` `keyword` — %s\n", tr("cmd_logsearch_desc")))
	b.WriteString(fmt.Sprintf("/restartdocker — %s\n\n", tr("cmd_restartdocker_desc")))

	b.WriteString(tr("help_net"))
	b.WriteString(fmt.Sprintf("/net — %s\n", tr("cmd_net_desc")))
	b.WriteString(fmt.Sprintf("/speedtest — %s\n\n", tr("cmd_speedtest_desc")))

	b.WriteString(tr("help_settings"))
	b.WriteString(fmt.Sprintf("/settings — *%s*\n", tr("cmd_settings_desc")))
	b.WriteString(fmt.Sprintf("/report — %s\n", tr("cmd_report_desc")))
	b.WriteString(fmt.Sprintf("/ping — %s\n", tr("cmd_ping_desc")))
	b.WriteString(fmt.Sprintf("/version — %s\n", tr("cmd_version_desc")))
	b.WriteString(fmt.Sprintf("/health — %s\n", tr("cmd_health_desc")))
	b.WriteString(fmt.Sprintf("/config — %s\n", tr("cmd_config_desc")))
	b.WriteString(fmt.Sprintf("/configjson — %s\n", tr("cmd_configjson_desc")))
	b.WriteString(fmt.Sprintf("/configset <json> — %s\n", tr("cmd_configset_desc")))
	b.WriteString(fmt.Sprintf("/logs — %s\n", tr("cmd_logs_desc")))
	b.WriteString(fmt.Sprintf("/ask <question> — %s\n", tr("cmd_ask_desc")))
	b.WriteString(fmt.Sprintf("/update — %s\n", tr("cmd_update_desc")))
	b.WriteString(fmt.Sprintf("/reboot · /shutdown — %s\n", tr("cmd_power_desc")))
	b.WriteString(fmt.Sprintf("/reboot force · /forcereboot — %s\n\n", tr("cmd_forcereboot_desc")))

	reportsEnabled, reportInterval, reportTimes := ctx.Settings.GetReportsSettings()

	ctx.Settings.Mu.RLock()
	quiet := ctx.Settings.QuietHours
	ctx.Settings.Mu.RUnlock()

	if reportsEnabled {
		b.WriteString(tr("help_reports"))
		for _, t := range reportTimes {
			b.WriteString(fmt.Sprintf("%02d:%02d ", t.Hour, t.Minute))
		}
		b.WriteString(fmt.Sprintf("%s\n", fmt.Sprintf(tr("help_every_days"), reportInterval)))
	}

	if quiet.Enabled {
		b.WriteString(fmt.Sprintf(tr("help_quiet"),
			quiet.Start.Hour, quiet.Start.Minute,
			quiet.End.Hour, quiet.End.Minute))
	}

	return b.String()
}

func GetHelpText(ctx *AppContext) string { return getHelpText(ctx) }

func getPingText(ctx *AppContext) string {
	tr := ctx.Tr
	ctx.Bot.Mu.Lock()
	startTime := ctx.Bot.StartTime
	ctx.Bot.Mu.Unlock()

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

func GetPingText(ctx *AppContext) string { return getPingText(ctx) }

func getConfigText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("config_title"))

	// Reports
	b.WriteString(tr("cfg_reports"))
	if ctx.Config.Reports.Enabled {
		b.WriteString(fmt.Sprintf(tr("cfg_every_days"), ctx.Config.Reports.IntervalDays))
		for _, t := range ctx.Config.Reports.Times {
			b.WriteString(fmt.Sprintf("%02d:%02d ", t.Hour, t.Minute))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(tr("cfg_disabled") + "\n")
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
	for mount, diskCfg := range ctx.Config.Notifications.SecondaryDisks {
		writeNotifLine("Disk "+mountShortName(mount), diskCfg)
	}
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

func GetConfigText(ctx *AppContext) string { return getConfigText(ctx) }

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
	}
	for mount := range ctx.Config.Notifications.SecondaryDisks {
		paths = append(paths, struct {
			name string
			path string
		}{name: "Disk " + mountShortName(mount), path: mount})
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
	b.WriteString(fmt.Sprintf("\n*NASBot Version:* %s\n", getVersion()))
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(fmt.Sprintf("*Go Version:* %s\n", buildInfo.GoVersion))
	}

	return b.String()
}

func getVersionText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(fmt.Sprintf(tr("version_title"), getVersion()))

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(fmt.Sprintf(tr("version_go"), buildInfo.GoVersion))
	}

	h, err := host.Info()
	if err == nil {
		b.WriteString(fmt.Sprintf(tr("version_arch"), h.KernelArch))
		b.WriteString(fmt.Sprintf(tr("version_os"), h.Platform, h.PlatformVersion))
	}

	ctx.Bot.Mu.Lock()
	startTime := ctx.Bot.StartTime
	ctx.Bot.Mu.Unlock()
	b.WriteString(fmt.Sprintf(tr("version_uptime"), format.FormatDuration(time.Since(startTime))))

	return b.String()
}

func GetVersionText(ctx *AppContext) string { return getVersionText(ctx) }

// mountShortName extracts the last meaningful path component from a mount point
// for human-readable display. e.g. "/mnt/data" -> "data", "/" -> "root".
func mountShortName(mount string) string {
	parts := strings.Split(mount, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return "root"
}
