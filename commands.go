package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
)

// ═══════════════════════════════════════════════════════════════════
//  TEXT GENERATORS (use cache, instant response)
// ═══════════════════════════════════════════════════════════════════

func getStatusText() string {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return tr("loading")
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf(tr("status_title"), time.Now().Format("15:04")))

	b.WriteString(fmt.Sprintf(tr("cpu_fmt"), makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf(tr("ram_fmt"), makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf(tr("swap_fmt"), makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf(tr("ssd_fmt"), s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf(tr("hdd_fmt"), s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	if s.DiskUtil > 10 {
		b.WriteString(fmt.Sprintf(tr("disk_io_fmt"), s.DiskUtil))
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			b.WriteString(fmt.Sprintf(tr("disk_rw_fmt"), s.ReadMBs, s.WriteMBs))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(tr("uptime_fmt"), formatUptime(s.Uptime)))

	return b.String()
}

func getTempText() string {
	var b strings.Builder
	b.WriteString("🌡 *Temperatures*\n\n")

	cpuTemp := readCPUTemp()
	cpuIcon := "✅"
	cpuStatus := "looking good"
	if cpuTemp > 60 {
		cpuIcon = "🟡"
		cpuStatus = "a bit warm"
	}
	if cpuTemp > 75 {
		cpuIcon = "🔥"
		cpuStatus = "running hot!"
	}
	b.WriteString(fmt.Sprintf("%s CPU: %.0f°C — %s\n\n", cpuIcon, cpuTemp, cpuStatus))

	b.WriteString("*Disks*\n")
	for _, dev := range []string{"sda", "sdb"} {
		temp, health := readDiskSMART(dev)
		icon := "✅"
		status := "healthy"
		if strings.Contains(strings.ToUpper(health), "FAIL") {
			icon = "🚨"
			status = "FAILING!"
		} else if temp > 45 {
			icon = "🟡"
			status = "warm"
		}
		b.WriteString(fmt.Sprintf("%s %s: %d°C — %s\n", icon, dev, temp, status))
	}
	return b.String()
}

func getNetworkText() string {
	var b strings.Builder
	b.WriteString("🌐 *Network*\n\n")

	localIP := "n/a"
	if out, err := exec.Command("hostname", "-I").Output(); err == nil {
		ips := strings.Fields(string(out))
		if len(ips) > 0 {
			localIP = ips[0]
		}
	}
	b.WriteString(fmt.Sprintf("🏠 Local: `%s`\n", localIP))

	publicIP := "checking..."
	client := http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if body, err := io.ReadAll(resp.Body); err == nil {
			publicIP = string(body)
		}
	}
	b.WriteString(fmt.Sprintf("🌍 Public: `%s`\n", publicIP))

	return b.String()
}

func getLogsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dmesg")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.CommandContext(ctx, "journalctl", "-n", "15", "--no-pager")
		out, _ = cmd.Output()
	}

	lines := strings.Split(string(out), "\n")
	start := len(lines) - 15
	if start < 0 {
		start = 0
	}
	recentLogs := strings.Join(lines[start:], "\n")

	if len(recentLogs) > 3500 {
		recentLogs = recentLogs[:3500] + "..."
	}

	return fmt.Sprintf("*Recent system logs*\n```\n%s\n```", recentLogs)
}

func getTopProcText() string {
	cmd := exec.Command("ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return "_No processes found_"
	}

	count := 0
	var b strings.Builder
	b.WriteString("🔥 *Top Processes (by CPU)*\n\n")
	b.WriteString("`PID   CPU%  MEM%  COMMAND`\n")

	for i := 1; i < len(lines) && count < 10; i++ {
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

		if len(cmdName) > 15 {
			cmdName = cmdName[:13] + ".."
		}

		b.WriteString(fmt.Sprintf("`%-5s %-4s %-4s %s`\n",
			pid, cpuPct, memPct, cmdName))

		count++
	}

	return b.String()
}

func getHelpText() string {
	var b strings.Builder
	b.WriteString("👋 *Hey! I'm NASBot*\n\n")
	b.WriteString("Here's what I can do for you:\n\n")

	b.WriteString("*📊 Monitoring*\n")
	b.WriteString("/status — quick system overview\n")
	b.WriteString("/temp — check temperatures\n")
	b.WriteString("/top — top processes by CPU\n")
	b.WriteString("/sysinfo — detailed system info\n")
	b.WriteString("/diskpred — disk space prediction\n\n")

	b.WriteString("*🐳 Docker*\n")
	b.WriteString("/docker — manage containers\n")
	b.WriteString("/dstats — container resources\n")
	b.WriteString("/kill `name` — force kill container\n")
	b.WriteString("/restartdocker — restart Docker service\n\n")

	b.WriteString("*🌐 Network*\n")
	b.WriteString("/net — network info\n")
	b.WriteString("/speedtest — run speed test\n\n")

	b.WriteString("*⚙️ Settings & System*\n")
	b.WriteString("/settings — *configure everything*\n")
	b.WriteString("/report — full detailed report\n")
	b.WriteString("/ping — check if bot is alive\n")
	b.WriteString("/config — show current config\n")
	b.WriteString("/logs — recent system logs\n")
	b.WriteString("/reboot · /shutdown — power control\n\n")

	if reportMode > 0 {
		b.WriteString("_📨 Reports: ")
		if reportMode == 2 {
			b.WriteString(fmt.Sprintf("%02d:%02d & %02d:%02d_\n",
				reportMorningHour, reportMorningMinute,
				reportEveningHour, reportEveningMinute))
		} else {
			b.WriteString(fmt.Sprintf("%02d:%02d_\n", reportMorningHour, reportMorningMinute))
		}
	}

	if quietHoursEnabled {
		b.WriteString(fmt.Sprintf("_🌙 Quiet: %02d:%02d — %02d:%02d_",
			quietStartHour, quietStartMinute,
			quietEndHour, quietEndMinute))
	}

	return b.String()
}

func getPingText() string {
	uptime := time.Since(botStartTime)

	statsMutex.RLock()
	ready := statsReady
	statsMutex.RUnlock()

	status := "✅"
	statusText := "All systems operational"
	if !ready {
		status = "⚠️"
		statusText = "Stats not ready yet"
	}

	return fmt.Sprintf(`%s *Pong!*

%s

🤖 Bot uptime: `+"`%s`"+`
🖥 Collecting stats: %v
📡 Last check: `+"`%s`"+`

_I'm alive and watching!_`,
		status,
		statusText,
		formatDuration(uptime),
		ready,
		time.Now().In(location).Format("15:04:05"))
}

func getConfigText() string {
	var b strings.Builder
	b.WriteString("⚙️ *Current Configuration*\n\n")

	// Reports
	b.WriteString("*Reports:* ")
	if cfg.Reports.Enabled {
		if cfg.Reports.Morning.Enabled && cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf("✅ %02d:%02d & %02d:%02d\n",
				cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute,
				cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else if cfg.Reports.Morning.Enabled {
			b.WriteString(fmt.Sprintf("✅ Morning only (%02d:%02d)\n",
				cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute))
		} else if cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf("✅ Evening only (%02d:%02d)\n",
				cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else {
			b.WriteString("❌ Disabled\n")
		}
	} else {
		b.WriteString("❌ Disabled\n")
	}

	// Quiet hours
	b.WriteString("*Quiet Hours:* ")
	if cfg.QuietHours.Enabled {
		b.WriteString(fmt.Sprintf("✅ %02d:%02d — %02d:%02d\n",
			cfg.QuietHours.StartHour, cfg.QuietHours.StartMinute,
			cfg.QuietHours.EndHour, cfg.QuietHours.EndMinute))
	} else {
		b.WriteString("❌ Disabled\n")
	}

	// Notifications
	b.WriteString("\n*Notifications:*\n")
	if cfg.Notifications.CPU.Enabled {
		b.WriteString(fmt.Sprintf("  CPU: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.CPU.WarningThreshold, cfg.Notifications.CPU.CriticalThreshold))
	} else {
		b.WriteString("  CPU: ❌\n")
	}
	if cfg.Notifications.RAM.Enabled {
		b.WriteString(fmt.Sprintf("  RAM: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.RAM.WarningThreshold, cfg.Notifications.RAM.CriticalThreshold))
	} else {
		b.WriteString("  RAM: ❌\n")
	}
	if cfg.Notifications.Swap.Enabled {
		b.WriteString(fmt.Sprintf("  Swap: ⚠️ >%.0f%%\n", cfg.Notifications.Swap.WarningThreshold))
	} else {
		b.WriteString("  Swap: ❌\n")
	}
	if cfg.Notifications.DiskSSD.Enabled {
		b.WriteString(fmt.Sprintf("  SSD: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.DiskSSD.WarningThreshold, cfg.Notifications.DiskSSD.CriticalThreshold))
	} else {
		b.WriteString("  SSD: ❌\n")
	}
	if cfg.Notifications.DiskHDD.Enabled {
		b.WriteString(fmt.Sprintf("  HDD: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.DiskHDD.WarningThreshold, cfg.Notifications.DiskHDD.CriticalThreshold))
	} else {
		b.WriteString("  HDD: ❌\n")
	}
	if cfg.Notifications.DiskIO.Enabled {
		b.WriteString(fmt.Sprintf("  I/O: ⚠️ >%.0f%%\n", cfg.Notifications.DiskIO.WarningThreshold))
	} else {
		b.WriteString("  I/O: ❌\n")
	}
	b.WriteString(fmt.Sprintf("  SMART: %s\n", boolToEmoji(cfg.Notifications.SMART.Enabled)))

	// Docker
	b.WriteString("\n*Docker:*\n")
	if cfg.Docker.Watchdog.Enabled {
		b.WriteString(fmt.Sprintf("  Watchdog: ✅ %dm timeout\n", cfg.Docker.Watchdog.TimeoutMinutes))
	} else {
		b.WriteString("  Watchdog: ❌\n")
	}
	if cfg.Docker.WeeklyPrune.Enabled {
		b.WriteString(fmt.Sprintf("  Prune: ✅ %s @ %02d:00\n",
			strings.Title(cfg.Docker.WeeklyPrune.Day), cfg.Docker.WeeklyPrune.Hour))
	} else {
		b.WriteString("  Prune: ❌\n")
	}
	if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
		b.WriteString(fmt.Sprintf("  Auto-restart: ✅ RAM >%.0f%%\n",
			cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold))
	} else {
		b.WriteString("  Auto-restart: ❌\n")
	}

	// Intervals
	b.WriteString(fmt.Sprintf("\n*Intervals:* Stats %ds · Monitor %ds",
		cfg.Intervals.StatsSeconds, cfg.Intervals.MonitorSeconds))

	return b.String()
}

// getSysInfoText returns detailed system information
func getSysInfoText() string {
	var b strings.Builder
	b.WriteString("🖥 *System Information*\n\n")

	// Host info
	h, err := host.Info()
	if err == nil {
		b.WriteString(fmt.Sprintf("*Hostname:* `%s`\n", h.Hostname))
		b.WriteString(fmt.Sprintf("*OS:* %s %s\n", h.Platform, h.PlatformVersion))
		b.WriteString(fmt.Sprintf("*Kernel:* %s\n", h.KernelVersion))
		b.WriteString(fmt.Sprintf("*Architecture:* %s\n", h.KernelArch))
		b.WriteString(fmt.Sprintf("*Uptime:* %s\n", formatUptime(h.Uptime)))
		b.WriteString(fmt.Sprintf("*Boot Time:* %s\n", time.Unix(int64(h.BootTime), 0).In(location).Format("02/01/2006 15:04")))
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
	for _, path := range []string{PathSSD, PathHDD} {
		d, err := disk.Usage(path)
		if err == nil {
			name := "SSD"
			if path == PathHDD {
				name = "HDD"
			}
			b.WriteString(fmt.Sprintf("*%s (%s):* %.0f GB total\n", name, path, float64(d.Total)/1024/1024/1024))
		}
	}

	// Go runtime info
	b.WriteString("\n*NASBot Version:* 1.0.0\n")
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(fmt.Sprintf("*Go Version:* %s\n", buildInfo.GoVersion))
	}

	return b.String()
}

// getDiskPredictionText estimates when disks will be full
func getDiskPredictionText() string {
	diskUsageHistoryMutex.Lock()
	history := make([]DiskUsagePoint, len(diskUsageHistory))
	copy(history, diskUsageHistory)
	diskUsageHistoryMutex.Unlock()

	var b strings.Builder
	b.WriteString("📊 *Disk Space Prediction*\n\n")

	if len(history) < 12 { // Need at least 1 hour of data
		b.WriteString("_Collecting data... need at least 1 hour of history._\n\n")
		b.WriteString(fmt.Sprintf("_Data points: %d/12_", len(history)))
		return b.String()
	}

	// Calculate trend for SSD
	ssdPred := predictDiskFull(history, true)
	hddPred := predictDiskFull(history, false)

	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	// SSD
	b.WriteString(fmt.Sprintf("💿 *SSD* — %.1f%% used\n", s.VolSSD.Used))
	if ssdPred.DaysUntilFull < 0 {
		b.WriteString("   📈 _Usage decreasing or stable_\n")
	} else if ssdPred.DaysUntilFull > 365 {
		b.WriteString("   ✅ _More than a year until full_\n")
	} else if ssdPred.DaysUntilFull > 30 {
		b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(ssdPred.DaysUntilFull)))
	} else if ssdPred.DaysUntilFull > 7 {
		b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(ssdPred.DaysUntilFull)))
	} else {
		b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(ssdPred.DaysUntilFull)))
	}
	b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n\n", ssdPred.GBPerDay))

	// HDD
	b.WriteString(fmt.Sprintf("🗄 *HDD* — %.1f%% used\n", s.VolHDD.Used))
	if hddPred.DaysUntilFull < 0 {
		b.WriteString("   📈 _Usage decreasing or stable_\n")
	} else if hddPred.DaysUntilFull > 365 {
		b.WriteString("   ✅ _More than a year until full_\n")
	} else if hddPred.DaysUntilFull > 30 {
		b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(hddPred.DaysUntilFull)))
	} else if hddPred.DaysUntilFull > 7 {
		b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(hddPred.DaysUntilFull)))
	} else {
		b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(hddPred.DaysUntilFull)))
	}
	b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n", hddPred.GBPerDay))

	b.WriteString(fmt.Sprintf("\n_Based on %d data points (%s of data)_",
		len(history),
		formatDuration(time.Since(history[0].Time))))

	return b.String()
}

// predictDiskFull calculates days until disk is full using linear regression
func predictDiskFull(history []DiskUsagePoint, isSSD bool) DiskPrediction {
	if len(history) < 2 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	first := history[0]
	last := history[len(history)-1]

	var firstFree, lastFree uint64
	if isSSD {
		firstFree = first.SSDFree
		lastFree = last.SSDFree
	} else {
		firstFree = first.HDDFree
		lastFree = last.HDDFree
	}

	timeDiff := last.Time.Sub(first.Time).Hours() / 24 // Days
	if timeDiff < 0.01 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	// GB change (negative means filling up)
	gbChange := float64(int64(lastFree)-int64(firstFree)) / 1024 / 1024 / 1024
	gbPerDay := gbChange / timeDiff

	if gbPerDay >= 0 {
		// Disk is freeing up or stable
		return DiskPrediction{DaysUntilFull: -1, GBPerDay: -gbPerDay}
	}

	// Days until free space = 0
	currentFreeGB := float64(lastFree) / 1024 / 1024 / 1024
	daysUntilFull := currentFreeGB / (-gbPerDay)

	return DiskPrediction{
		DaysUntilFull: daysUntilFull,
		GBPerDay:      -gbPerDay,
	}
}

// recordDiskUsage adds current disk usage to history
func recordDiskUsage() {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return
	}

	diskUsageHistoryMutex.Lock()
	defer diskUsageHistoryMutex.Unlock()

	point := DiskUsagePoint{
		Time:    time.Now(),
		SSDUsed: s.VolSSD.Used,
		HDDUsed: s.VolHDD.Used,
		SSDFree: s.VolSSD.Free,
		HDDFree: s.VolHDD.Free,
	}

	diskUsageHistory = append(diskUsageHistory, point)

	// Keep max 7 days of data (at 5-min intervals = 2016 points)
	if len(diskUsageHistory) > 2016 {
		diskUsageHistory = diskUsageHistory[1:]
	}
}
