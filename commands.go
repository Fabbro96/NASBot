//go:build !fswatchdog
// +build !fswatchdog

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

const (
maxLogLines     = 15
maxLogChars     = 3500
cpuWarmC        = 60
cpuHotC         = 75
diskWarmC       = 45
maxTopProcesses = 10
maxProcNameLen  = 15

logCmdTimeout = 3 * time.Second
netTimeout    = 3 * time.Second
psTimeout     = 3 * time.Second
hostIPTimeout = 1 * time.Second
publicIPURL   = "https://api.ipify.org"
)

func cpuTempStatus(ctx *AppContext, temp float64) (icon, status string) {
	icon = "✅"
	status = ctx.Tr("temp_status_good")
	if temp > cpuWarmC {
		icon = "🟡"
		status = ctx.Tr("temp_status_warm")
	}
	if temp > cpuHotC {
		icon = "🔥"
		status = ctx.Tr("temp_status_hot")
	}
	return
}

func diskTempStatus(ctx *AppContext, temp int, health string) (icon, status string) {
	icon = "✅"
	status = ctx.Tr("temp_disk_healthy")
	if strings.Contains(strings.ToUpper(health), "FAIL") {
		icon = "🚨"
		status = ctx.Tr("temp_disk_fail")
	} else if temp > diskWarmC && temp > 0 {
		icon = "🟡"
		status = ctx.Tr("temp_disk_warm")
	}
	return
}

func runCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func getLocalIP(ctx context.Context) string {
	out, err := runCommandOutput(ctx, "hostname", "-I")
	if err != nil {
		return "n/a"
	}
	ips := strings.Fields(string(out))
	if len(ips) == 0 {
		return "n/a"
	}
	return ips[0]
}

func getPublicIP(ctx *AppContext, c context.Context) string {
	req, err := http.NewRequestWithContext(c, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return ctx.Tr("net_checking")
	}
	
	client := http.DefaultClient
	if ctx.HTTP != nil {
		client = ctx.HTTP
	}

	resp, err := client.Do(req)
	if err != nil {
		return ctx.Tr("net_checking")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ctx.Tr("net_checking")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return ctx.Tr("net_checking")
	}
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return ctx.Tr("net_checking")
	}
	return ip
}

// ═══════════════════════════════════════════════════════════════════
//  TEXT GENERATORS (use cache, instant response)
// ═══════════════════════════════════════════════════════════════════

func getStatusText(ctx *AppContext) string {
	s, ready := ctx.Stats.Get()

	if !ready {
		return ctx.Tr("loading")
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf(ctx.Tr("status_title"), time.Now().Format("15:04")))

	b.WriteString(fmt.Sprintf(ctx.Tr("cpu_fmt"), makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf(ctx.Tr("ram_fmt"), makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf(ctx.Tr("swap_fmt"), makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf(ctx.Tr("ssd_fmt"), s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf(ctx.Tr("hdd_fmt"), s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	if s.DiskUtil > 10 {
		b.WriteString(fmt.Sprintf(ctx.Tr("disk_io_fmt"), s.DiskUtil))
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			b.WriteString(fmt.Sprintf(ctx.Tr("disk_rw_fmt"), s.ReadMBs, s.WriteMBs))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(ctx.Tr("uptime_fmt"), formatUptime(s.Uptime)))

	return b.String()
}

func getTempText(ctx *AppContext) string {
	var b strings.Builder
	b.WriteString(ctx.Tr("temp_title"))

	cpuTemp := readCPUTemp()
	cpuIcon, cpuStatus := cpuTempStatus(ctx, cpuTemp)
	b.WriteString(fmt.Sprintf(ctx.Tr("temp_cpu"), cpuIcon, cpuTemp, cpuStatus))

	b.WriteString(ctx.Tr("temp_disks"))
	for _, dev := range getSmartDevices() {
		temp, health := readDiskSMART(dev)
		icon, status := diskTempStatus(ctx, temp, health)
		b.WriteString(fmt.Sprintf("%s %s: %d°C — %s\n", icon, dev, temp, status))
	}
	return b.String()
}

func getNetworkText(ctx *AppContext) string {
	var b strings.Builder
	b.WriteString(ctx.Tr("net_title"))

	localCtx, cancelLocal := context.WithTimeout(context.Background(), hostIPTimeout)
	defer cancelLocal()
	b.WriteString(fmt.Sprintf(ctx.Tr("net_local"), getLocalIP(localCtx)))

	publicCtx, cancelPublic := context.WithTimeout(context.Background(), netTimeout)
	defer cancelPublic()
	b.WriteString(fmt.Sprintf(ctx.Tr("net_public"), getPublicIP(ctx, publicCtx)))

	return b.String()
}

func getLogsText(ctx *AppContext) string {
	c, cancel := context.WithTimeout(context.Background(), logCmdTimeout)
	defer cancel()

	out, err := runCommandOutput(c, "dmesg")
	if err != nil || len(out) == 0 {
		out, _ = runCommandOutput(c, "journalctl", "-n", fmt.Sprint(maxLogLines), "--no-pager")
	}
	if len(out) == 0 {
		return fmt.Sprintf("%s_No logs available_\n", ctx.Tr("logs_title"))
	}

	lines := strings.Split(string(out), "\n")
	start := len(lines) - maxLogLines
	if start < 0 {
		start = 0
	}
	recentLogs := strings.Join(lines[start:], "\n")

	if len(recentLogs) > maxLogChars {
		recentLogs = recentLogs[:maxLogChars] + "..."
	}

	return fmt.Sprintf("%s```\n%s\n```", ctx.Tr("logs_title"), recentLogs)
}

func getTopProcText(ctx *AppContext) string {
	c, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := runCommandOutput(c, "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	if len(lines) < 2 {
		return ctx.Tr("top_none")
	}

	count := 0
	var b strings.Builder
	b.WriteString(ctx.Tr("top_title"))
	b.WriteString(ctx.Tr("top_header"))

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
	var b strings.Builder
	b.WriteString(ctx.Tr("help_intro"))

	b.WriteString(ctx.Tr("help_mon"))
	b.WriteString("/status — quick system overview\n")
	b.WriteString("/quick — ultra-compact one-liner\n")
	b.WriteString("/temp — check temperatures\n")
	b.WriteString("/top — top processes by CPU\n")
	b.WriteString("/sysinfo — detailed system info\n")
	b.WriteString("/diskpred — disk space prediction\n\n")

	b.WriteString(ctx.Tr("help_docker"))
	b.WriteString("/docker — manage containers\n")
	b.WriteString("/dstats — container resources\n")
	b.WriteString("/kill `name` — force kill container\n")
	b.WriteString("/logsearch `name` `keyword` — search logs\n")
	b.WriteString("/restartdocker — restart Docker service\n\n")

	b.WriteString(ctx.Tr("help_net"))
	b.WriteString("/net — network info\n")
	b.WriteString("/speedtest — run speed test\n\n")

	b.WriteString(ctx.Tr("help_settings"))
	b.WriteString("/settings — *configure everything*\n")
	b.WriteString("/report — full detailed report\n")
	b.WriteString("/ping — check if bot is alive\n")
	b.WriteString("/config — show current config\n")
	b.WriteString("/configjson — show full config.json (redacted)\n")
	b.WriteString("/configset <json> — update config.json\n")
	b.WriteString("/logs — recent system logs\n")
	b.WriteString("/reboot · /shutdown — power control\n\n")

	ctx.Settings.mu.RLock()
	reportMode := ctx.Settings.ReportMode
	morning := ctx.Settings.ReportMorning
	evening := ctx.Settings.ReportEvening
	quiet := ctx.Settings.QuietHours
	ctx.Settings.mu.RUnlock()

	if reportMode > 0 {
		b.WriteString(ctx.Tr("help_reports"))
		if reportMode == 2 {
			b.WriteString(fmt.Sprintf("%02d:%02d & %02d:%02d_\n",
morning.Hour, morning.Minute,
evening.Hour, evening.Minute))
		} else {
			b.WriteString(fmt.Sprintf("%02d:%02d_\n", morning.Hour, morning.Minute))
		}
	}

	if quiet.Enabled {
		b.WriteString(fmt.Sprintf(ctx.Tr("help_quiet"),
quiet.Start.Hour, quiet.Start.Minute,
quiet.End.Hour, quiet.End.Minute))
	}

	return b.String()
}

func getPingText(ctx *AppContext) string {
	ctx.Bot.mu.Lock()
	start := ctx.Bot.StartTime
	ctx.Bot.mu.Unlock()
	uptime := time.Since(start)

	_, ready := ctx.Stats.Get()

	status := "✅"
	statusText := ctx.Tr("ping_ok")
	if !ready {
		status = "⚠️"
		statusText = ctx.Tr("ping_not_ready")
	}

	return fmt.Sprintf(ctx.Tr("ping_pong")+"\n\n"+
"%s\n\n"+
ctx.Tr("ping_uptime")+"\n"+
ctx.Tr("ping_collecting")+"\n"+
ctx.Tr("ping_last_check")+"\n\n"+
ctx.Tr("ping_alive"),
status,
statusText,
formatDuration(uptime),
ready,
time.Now().In(ctx.State.TimeLocation).Format("15:04:05"))
}

func getConfigText(ctx *AppContext) string {
	var b strings.Builder
	b.WriteString(ctx.Tr("config_title"))
	
	cfg := ctx.Config

	// Reports
	b.WriteString(ctx.Tr("cfg_reports"))
	if cfg.Reports.Enabled {
		if cfg.Reports.Morning.Enabled && cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf(ctx.Tr("cfg_both"),
cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute,
cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else if cfg.Reports.Morning.Enabled {
			b.WriteString(fmt.Sprintf(ctx.Tr("cfg_morning_only"),
cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute))
		} else if cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf(ctx.Tr("cfg_evening_only"),
cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else {
			b.WriteString(ctx.Tr("cfg_disabled"))
		}
	} else {
		b.WriteString(ctx.Tr("cfg_disabled"))
	}

	// Quiet hours
	b.WriteString(ctx.Tr("cfg_quiet"))
	if cfg.QuietHours.Enabled {
		b.WriteString(fmt.Sprintf(ctx.Tr("cfg_quiet_fmt"),
cfg.QuietHours.StartHour, cfg.QuietHours.StartMinute,
cfg.QuietHours.EndHour, cfg.QuietHours.EndMinute))
	} else {
		b.WriteString(ctx.Tr("cfg_disabled"))
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
	writeNotifLine("CPU", cfg.Notifications.CPU)
	writeNotifLine("RAM", cfg.Notifications.RAM)
	writeNotifLine("Swap", ResourceConfig{Enabled: cfg.Notifications.Swap.Enabled, WarningThreshold: cfg.Notifications.Swap.WarningThreshold})
	writeNotifLine("SSD", cfg.Notifications.DiskSSD)
	writeNotifLine("HDD", cfg.Notifications.DiskHDD)
	writeNotifLine("I/O", ResourceConfig{Enabled: cfg.Notifications.DiskIO.Enabled, WarningThreshold: cfg.Notifications.DiskIO.WarningThreshold})
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
titleCaseWord(cfg.Docker.WeeklyPrune.Day), cfg.Docker.WeeklyPrune.Hour))
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
		b.WriteString(fmt.Sprintf("*Uptime:* %s\n", formatUptime(h.Uptime)))
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

	// Disk info (Use Config for paths)
	paths := []string{ctx.Config.Paths.SSD, ctx.Config.Paths.HDD}
	// Fallback/Default handling should ideally be in Config.Paths but assuming they are populated
	if paths[0] == "" { paths[0] = "/Volume1" } // Fallback same as old globals for safety
	if paths[1] == "" { paths[1] = "/Volume2" }

	for i, path := range paths {
		if path == "" { continue }
		d, err := disk.Usage(path)
		if err == nil {
			name := "SSD"
			if i == 1 {
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
func getDiskPredictionText(ctx *AppContext) string {
	ctx.State.mu.Lock()
	history := make([]DiskUsagePoint, len(ctx.State.DiskHistory))
	copy(history, ctx.State.DiskHistory)
	ctx.State.mu.Unlock()

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

	s, _ := ctx.Stats.Get()

	// SSD
	writeDiskPred := func(icon, name string, pred DiskPrediction, usedPct float64) {
		b.WriteString(fmt.Sprintf("%s *%s* — %.1f%% used\n", icon, name, usedPct))
		switch {
		case pred.DaysUntilFull < 0:
			b.WriteString("   📈 _Usage decreasing or stable_\n")
		case pred.DaysUntilFull > 365:
			b.WriteString("   ✅ _More than a year until full_\n")
		case pred.DaysUntilFull > 30:
			b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(pred.DaysUntilFull)))
		case pred.DaysUntilFull > 7:
			b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(pred.DaysUntilFull)))
		default:
			b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(pred.DaysUntilFull)))
		}
		b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n\n", pred.GBPerDay))
	}

	writeDiskPred("💿", "SSD", ssdPred, s.VolSSD.Used)
	writeDiskPred("🗄", "HDD", hddPred, s.VolHDD.Used)

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
func recordDiskUsage(ctx *AppContext) {
	s, ready := ctx.Stats.Get()

	if !ready {
		return
	}

	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	point := DiskUsagePoint{
		Time:    time.Now(),
		SSDUsed: s.VolSSD.Used,
		HDDUsed: s.VolHDD.Used,
		SSDFree: s.VolSSD.Free,
		HDDFree: s.VolHDD.Free,
	}

	ctx.State.DiskHistory = append(ctx.State.DiskHistory, point)

	// Keep max 7 days of data (at 5-min intervals = 2016 points)
	if len(ctx.State.DiskHistory) > 2016 {
		ctx.State.DiskHistory = ctx.State.DiskHistory[1:]
	}
}

// ═══════════════════════════════════════════════════════════════════
//  QUICK STATUS (ultra-compact one-liner)
// ═══════════════════════════════════════════════════════════════════

func getQuickText(ctx *AppContext) string {
	s, ready := ctx.Stats.Get()

	if !ready {
		return "⏳"
	}

	// Get trend graphs
	cpuGraph, ramGraph := getTrendSummary(ctx)

	// Container count
	containers := getCachedContainerList(ctx)
	
	running := 0
	for _, c := range containers {
		if c.Running {
			running++
		}
	}

	// Temperature
	temp := readCPUTemp()
	tempStr := ""
	if temp > 0 {
		tempIcon := "🌡"
		if temp > 70 {
			tempIcon = "🔥"
		}
		tempStr = fmt.Sprintf(" %s%.0f°", tempIcon, temp)
	}

	// Health emoji
	healthEmoji := "✅"
	if s.CPU > 90 || s.RAM > 90 {
		healthEmoji = "⚠️"
	}
	if s.CPU > 95 || s.RAM > 95 || s.VolSSD.Used > 95 || s.VolHDD.Used > 95 {
		healthEmoji = "🚨"
	}

	// Build compact line with optional trends
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s ", healthEmoji))

	// CPU with trend
	b.WriteString(fmt.Sprintf("CPU %.0f%%", s.CPU))
	if cpuGraph != "" {
		b.WriteString(fmt.Sprintf(" `%s`", cpuGraph))
	}

	// RAM with trend
	b.WriteString(fmt.Sprintf(" · RAM %.0f%%", s.RAM))
	if ramGraph != "" {
		b.WriteString(fmt.Sprintf(" `%s`", ramGraph))
	}

	// Disks
	b.WriteString(fmt.Sprintf(" · SSD %.0f%% · HDD %.0f%%", s.VolSSD.Used, s.VolHDD.Used))

	// Docker
	b.WriteString(fmt.Sprintf(" · 🐳%d", running))

	// Temp
	b.WriteString(tempStr)

	return b.String()
}

// ═══════════════════════════════════════════════════════════════════
//  LOG SEARCH
// ═══════════════════════════════════════════════════════════════════

func getLogSearchText(ctx *AppContext, args string) string {
	// Parse: container keyword
	parts := strings.SplitN(strings.TrimSpace(args), " ", 2)
	if len(parts) < 2 {
		return "Usage: `/logsearch <container> <keyword>`\n\nExample: `/logsearch plex error`"
	}

	container := parts[0]
	keyword := parts[1]

	// Search logs
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(c, "docker", "logs", "--tail", "500", container)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("❌ Error: `%v`", err)
	}

	// Filter lines containing keyword
	lines := strings.Split(string(out), "\n")
	var matches []string
	keywordLower := strings.ToLower(keyword)

	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), keywordLower) {
			// Truncate long lines
			if len(line) > 100 {
				line = line[:97] + "..."
			}
			matches = append(matches, line)
		}
	}

	if len(matches) == 0 {
		return fmt.Sprintf("🔍 No matches for `%s` in `%s` logs", keyword, container)
	}

	// Limit to last 10 matches
	if len(matches) > 10 {
		matches = matches[len(matches)-10:]
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔍 *Log Search*: `%s` in `%s`\n\n", keyword, container))
	b.WriteString(fmt.Sprintf("Found %d matches (showing last %d):\n\n", len(matches), len(matches)))
	b.WriteString("```\n")
for _, m := range matches {
		b.WriteString(m + "\n")
	}
	b.WriteString("```")

	return b.String()
}
