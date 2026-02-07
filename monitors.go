package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// ═══════════════════════════════════════════════════════════════════
//  AUTONOMOUS MANAGER (automatic decisions)
// ═══════════════════════════════════════════════════════════════════

func autonomousManager(ctx *AppContext, bot BotAPI, runCtx ...context.Context) {
	cfg := ctx.Config
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}

	ticker := time.NewTicker(10 * time.Second)
	diskTicker := time.NewTicker(5 * time.Minute)
	trendTicker := time.NewTicker(5 * time.Minute) // Sample trends every 5 min

	netInterval := time.Duration(cfg.NetworkWatchdog.CheckIntervalSecs) * time.Second
	if netInterval < 10*time.Second {
		netInterval = 60 * time.Second
	}
	netTicker := time.NewTicker(netInterval)

	raidInterval := time.Duration(cfg.RaidWatchdog.CheckIntervalSecs) * time.Second
	if raidInterval < 30*time.Second {
		raidInterval = 5 * time.Minute
	}
	raidTicker := time.NewTicker(raidInterval)

	kwInterval := time.Duration(cfg.KernelWatchdog.CheckIntervalSecs) * time.Second
	if kwInterval < 10*time.Second {
		kwInterval = 60 * time.Second
	}
	kwTicker := time.NewTicker(kwInterval)

	defer ticker.Stop()
	defer diskTicker.Stop()
	defer trendTicker.Stop()
	defer kwTicker.Stop()
	defer netTicker.Stop()
	defer raidTicker.Stop()

	if cfg.KernelWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("kw_started"), cfg.KernelWatchdog.CheckIntervalSecs))
	}
	if cfg.NetworkWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("netwd_started"), cfg.NetworkWatchdog.CheckIntervalSecs))
	}
	if cfg.RaidWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("raidwd_started"), cfg.RaidWatchdog.CheckIntervalSecs))
	}

	for {
		select {
		case <-rc.Done():
			return
		case <-ticker.C:
			s, ready := ctx.Stats.Get()

			if !ready {
				continue
			}

			// Check stress for enabled resources only
			if cfg.StressTracking.Enabled {
				if cfg.Notifications.DiskIO.Enabled {
					checkResourceStress(ctx, bot, "HDD", s.DiskUtil, cfg.Notifications.DiskIO.WarningThreshold)
				}
				if cfg.Notifications.CPU.Enabled {
					checkResourceStress(ctx, bot, "CPU", s.CPU, cfg.Notifications.CPU.WarningThreshold)
				}
				if cfg.Notifications.RAM.Enabled {
					checkResourceStress(ctx, bot, "RAM", s.RAM, cfg.Notifications.RAM.WarningThreshold)
				}
				if cfg.Notifications.Swap.Enabled {
					checkResourceStress(ctx, bot, "Swap", s.Swap, cfg.Notifications.Swap.WarningThreshold)
				}
				if cfg.Notifications.DiskSSD.Enabled {
					checkResourceStress(ctx, bot, "SSD", s.VolSSD.Used, cfg.Notifications.DiskSSD.WarningThreshold)
				}
			}

			// Check temperature
			if cfg.Temperature.Enabled {
				checkTemperatureAlert(ctx, bot)
			}

			// Check critical RAM for auto-restart
			if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
				if s.RAM >= cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold {
					handleCriticalRAM(ctx, bot, s)
				}
			}

			// Clean restart counter (every hour)
			cleanRestartCounter(ctx)

			// Docker watchdog
			if cfg.Docker.Watchdog.Enabled {
				checkDockerHealth(ctx, bot)
			}

			// Check for unexpected container stops
			checkContainerStates(ctx, bot)

			// Check critical containers
			checkCriticalContainers(ctx, bot)

			// Weekly prune
			if cfg.Docker.WeeklyPrune.Enabled {
				checkWeeklyPrune(ctx, bot)
			}

		case <-diskTicker.C:
			recordDiskUsage(ctx)

		case <-trendTicker.C:
			recordTrendPoint(ctx)

		case <-kwTicker.C:
			if cfg.KernelWatchdog.Enabled {
				checkKernelEvents(ctx, bot)
			}

		case <-netTicker.C:
			if cfg.NetworkWatchdog.Enabled {
				checkNetworkHealth(ctx, bot)
			}

		case <-raidTicker.C:
			if cfg.RaidWatchdog.Enabled {
				checkRaidHealth(ctx, bot)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  RAID WATCHDOG
// ═══════════════════════════════════════════════════════════════════

func checkRaidHealth(ctx *AppContext, bot BotAPI) {
	cfg := ctx.Config
	issues := getRaidIssues()

	if len(issues) == 0 {
		var shouldNotify bool
		var downSince time.Time
		ctx.Monitor.mu.Lock()
		if !ctx.Monitor.RaidDownSince.IsZero() {
			shouldNotify = cfg.RaidWatchdog.RecoveryNotify
			downSince = ctx.Monitor.RaidDownSince
			ctx.Monitor.RaidDownSince = time.Time{}
			ctx.Monitor.RaidLastSignature = ""
		}
		ctx.Monitor.mu.Unlock()

		if shouldNotify && !ctx.IsQuietHours() {
			msg := fmt.Sprintf(ctx.Tr("raid_recovered"), format.FormatDuration(time.Since(downSince)))
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		return
	}

	signature := strings.Join(issues, " | ")
	cooldown := time.Duration(cfg.RaidWatchdog.CooldownMins) * time.Minute
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}

	shouldAlert := false
	ctx.Monitor.mu.Lock()
	if ctx.Monitor.RaidDownSince.IsZero() {
		ctx.Monitor.RaidDownSince = time.Now()
	}
	if signature != ctx.Monitor.RaidLastSignature || time.Since(ctx.Monitor.RaidAlertTime) >= cooldown {
		shouldAlert = true
		ctx.Monitor.RaidLastSignature = signature
		ctx.Monitor.RaidAlertTime = time.Now()
	}
	ctx.Monitor.mu.Unlock()

	if shouldAlert {
		msg := fmt.Sprintf(ctx.Tr("raid_alert"), strings.Join(issues, "\n"))
		m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
		ctx.State.AddEvent("critical", "RAID issue detected")
	}
}

func getRaidIssues() []string {
	var issues []string
	if data, err := os.ReadFile("/proc/mdstat"); err == nil {
		text := string(data)
		if strings.Contains(text, "md") {
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				if strings.Contains(line, "md") && strings.Contains(line, "[") && strings.Contains(line, "]") {
					if strings.Contains(line, "_") {
						issues = append(issues, fmt.Sprintf("mdadm degraded: %s", strings.TrimSpace(line)))
					}
					if strings.Contains(line, "recovery") || strings.Contains(line, "resync") || strings.Contains(line, "reshape") {
						issues = append(issues, fmt.Sprintf("mdadm sync: %s", strings.TrimSpace(line)))
					}
				}
			}
		}
	}
	if commandExists("zpool") {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := runCommandStdout(c, "zpool", "status", "-x")
		if err == nil {
			output := strings.TrimSpace(string(out))
			if output != "" && !strings.Contains(strings.ToLower(output), "all pools are healthy") {
				issues = append(issues, fmt.Sprintf("zpool: %s", output))
			}
		}
	}
	return issues
}

// ═══════════════════════════════════════════════════════════════════
//  RESOURCE STRESS
// ═══════════════════════════════════════════════════════════════════

func checkResourceStress(ctx *AppContext, bot BotAPI, resource string, currentValue, threshold float64) {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	tracker := ctx.State.ResourceStress[resource]
	if tracker == nil {
		tracker = &StressTracker{}
		ctx.State.ResourceStress[resource] = tracker
	}

	isStressed := currentValue >= threshold
	stressDurationThreshold := time.Duration(ctx.Config.StressTracking.DurationThresholdMinutes) * time.Minute

	if isStressed {
		if tracker.CurrentStart.IsZero() {
			tracker.CurrentStart = time.Now()
			tracker.StressCount++
			tracker.Notified = false
		}

		stressDuration := time.Since(tracker.CurrentStart)
		if stressDuration >= stressDurationThreshold && !tracker.Notified && !ctx.IsQuietHours() {
			var emoji, unit string
			switch resource {
			case "HDD":
				emoji = "💾"
				unit = "I/O"
			case "SSD":
				emoji = "💿"
				unit = "Usage"
			case "CPU":
				emoji = "🧠"
				unit = "Usage"
			case "RAM":
				emoji = "💾"
				unit = "Usage"
			case "Swap":
				emoji = "🔄"
				unit = "Usage"
			}

			msg := fmt.Sprintf("%s *%s stress*\n\n"+
				"%s: `%.0f%%` for `%s`\n\n"+
				"_Watching..._",
				emoji, resource, unit, currentValue,
				stressDuration.Round(time.Second))

			m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)

			tracker.Notified = true
			ctx.State.AddEvent("warning", fmt.Sprintf("%s high (%.0f%%) for %s", resource, currentValue, stressDuration.Round(time.Second)))
		}
	} else {
		if !tracker.CurrentStart.IsZero() {
			stressDuration := time.Since(tracker.CurrentStart)
			tracker.TotalStress += stressDuration

			if stressDuration > tracker.LongestStress {
				tracker.LongestStress = stressDuration
			}

			if tracker.Notified && !ctx.IsQuietHours() {
				msg := fmt.Sprintf("✅ *%s back to normal* after `%s`", resource, stressDuration.Round(time.Second))
				m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
				ctx.State.AddEvent("info", fmt.Sprintf("%s normalized after %s", resource, stressDuration.Round(time.Second)))
			}

			tracker.CurrentStart = time.Time{}
			tracker.Notified = false
		}
	}
}

// getStressSummary returns a summary of significant stress events
func getStressSummary(ctx *AppContext) string {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	var parts []string

	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		tracker := ctx.State.ResourceStress[res]
		if tracker == nil || tracker.StressCount == 0 {
			continue
		}

		if tracker.LongestStress < 5*time.Minute {
			continue
		}

		entry := fmt.Sprintf("%s %dx", res, tracker.StressCount)
		if tracker.LongestStress > 0 {
			entry += fmt.Sprintf(" `%s`", format.FormatDuration(tracker.LongestStress))
		}
		parts = append(parts, entry)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// resetStressCounters resets stress counters for new report period
func resetStressCounters(ctx *AppContext) {
	ctx.State.mu.Lock()
	defer ctx.State.mu.Unlock()

	for _, tracker := range ctx.State.ResourceStress {
		tracker.StressCount = 0
		tracker.LongestStress = 0
		tracker.TotalStress = 0
	}
}

// ═══════════════════════════════════════════════════════════════════
//  MONITOR ALERTS
// ═══════════════════════════════════════════════════════════════════

func monitorAlerts(ctx *AppContext, bot BotAPI, runCtx ...context.Context) {
	ticker := time.NewTicker(time.Duration(ctx.Config.Intervals.MonitorSeconds) * time.Second)
	defer ticker.Stop()
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}

	for {
		select {
		case <-rc.Done():
			return
		case <-ticker.C:
			s, ready := ctx.Stats.Get()

			if !ready {
				continue
			}

			var criticalAlerts []string
			cfg := ctx.Config

			if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.CriticalThreshold {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("💿 SSD critical: `%.1f%%`", s.VolSSD.Used))
			}
			if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.CriticalThreshold {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("🗄 HDD critical: `%.1f%%`", s.VolHDD.Used))
			}

			if cfg.Notifications.SMART.Enabled {
				for _, dev := range getSmartDevices(ctx) {
					_, health := readDiskSMART(dev)
					if strings.Contains(strings.ToUpper(health), "FAIL") {
						criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 Disk %s FAILING — backup now!", dev))
					}
				}
			}

			if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.CriticalThreshold {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("🧠 CPU critical: `%.1f%%`", s.CPU))
			}
			if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.CriticalThreshold {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("💾 RAM critical: `%.1f%%`", s.RAM))
			}

			cooldown := time.Duration(cfg.Intervals.CriticalAlertCooldownMins) * time.Minute
			ctx.Monitor.mu.Lock()
			lastAlert := ctx.Monitor.LastCriticalAlert
			ctx.Monitor.mu.Unlock()

			if len(criticalAlerts) > 0 && time.Since(lastAlert) >= cooldown && !ctx.IsQuietHours() {
				msg := "🚨 *Critical*\n\n" + strings.Join(criticalAlerts, "\n")
				m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
				ctx.Monitor.mu.Lock()
				ctx.Monitor.LastCriticalAlert = time.Now()
				ctx.Monitor.mu.Unlock()
			}

			if len(criticalAlerts) > 0 {
				for _, alert := range criticalAlerts {
					ctx.State.AddEvent("critical", alert)
				}
			}

			if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.WarningThreshold && s.CPU < cfg.Notifications.CPU.CriticalThreshold {
				ctx.State.AddEvent("warning", fmt.Sprintf("CPU high: %.1f%%", s.CPU))
			}
			if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.WarningThreshold && s.RAM < cfg.Notifications.RAM.CriticalThreshold {
				ctx.State.AddEvent("warning", fmt.Sprintf("RAM high: %.1f%%", s.RAM))
			}
			if cfg.Notifications.Swap.Enabled && s.Swap >= cfg.Notifications.Swap.WarningThreshold {
				ctx.State.AddEvent("warning", fmt.Sprintf("Swap high: %.1f%%", s.Swap))
			}
			if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.WarningThreshold && s.VolSSD.Used < cfg.Notifications.DiskSSD.CriticalThreshold {
				ctx.State.AddEvent("warning", fmt.Sprintf("SSD at %.1f%%", s.VolSSD.Used))
			}
			if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.WarningThreshold && s.VolHDD.Used < cfg.Notifications.DiskHDD.CriticalThreshold {
				ctx.State.AddEvent("warning", fmt.Sprintf("HDD at %.1f%%", s.VolHDD.Used))
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  STATS COLLECTOR
// ═══════════════════════════════════════════════════════════════════

func statsCollector(ctx *AppContext, runCtx ...context.Context) {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	ticker := time.NewTicker(time.Duration(ctx.Config.Intervals.StatsSeconds) * time.Second)
	defer ticker.Stop()
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}

	collect := func() {
		c, _ := cpu.Percent(0, false)
		v, _ := mem.VirtualMemory()
		sw, _ := mem.SwapMemory()
		l, _ := load.Avg()
		h, _ := host.Info()
		var dSSD, dHDD *disk.UsageStat
		if ctx.Config.Paths.SSD != "" {
			dSSD, _ = disk.Usage(ctx.Config.Paths.SSD)
		}
		if ctx.Config.Paths.HDD != "" {
			dHDD, _ = disk.Usage(ctx.Config.Paths.HDD)
		}

		currentIO, _ := disk.IOCounters()
		var readMBs, writeMBs, diskUtil float64
		if lastIO != nil && !lastIOTime.IsZero() {
			elapsed := time.Since(lastIOTime).Seconds()
			if elapsed > 0 {
				var rBytes, wBytes uint64
				var maxUtil float64
				for k, curr := range currentIO {
					if prev, ok := lastIO[k]; ok {
						rBytes += curr.ReadBytes - prev.ReadBytes
						wBytes += curr.WriteBytes - prev.WriteBytes
						deltaIOTime := curr.IoTime - prev.IoTime
						util := float64(deltaIOTime) / (elapsed * 10)
						if util > 100 {
							util = 100
						}
						if util > maxUtil {
							maxUtil = util
						}
					}
				}
				readMBs = float64(rBytes) / elapsed / 1024 / 1024
				writeMBs = float64(wBytes) / elapsed / 1024 / 1024
				diskUtil = maxUtil
			}
		}
		lastIO = currentIO
		lastIOTime = time.Now()

		topCPU, topRAM := getTopProcesses(5)

		cVal := 0.0
		if len(c) > 0 {
			cVal = c[0]
		}

		newStats := Stats{
			CPU:        format.SafeFloat([]float64{cVal}, 0),
			RAM:        v.UsedPercent,
			RAMFreeMB:  v.Available / 1024 / 1024,
			RAMTotalMB: v.Total / 1024 / 1024,
			Swap:       sw.UsedPercent,
			Load1m:     l.Load1,
			Load5m:     l.Load5,
			Load15m:    l.Load15,
			Uptime:     h.Uptime,
			ReadMBs:    readMBs,
			WriteMBs:   writeMBs,
			DiskUtil:   diskUtil,
			TopCPU:     topCPU,
			TopRAM:     topRAM,
		}

		if dSSD != nil {
			newStats.VolSSD = VolumeStats{Used: dSSD.UsedPercent, Free: dSSD.Free}
		}
		if dHDD != nil {
			newStats.VolHDD = VolumeStats{Used: dHDD.UsedPercent, Free: dHDD.Free}
		}

		ctx.Stats.Set(newStats)
	}

	collect()

	for {
		select {
		case <-rc.Done():
			return
		case <-ticker.C:
			collect()
		}
	}
}

func getTopProcesses(limit int) (topCPU, topRAM []ProcInfo) {
	ps, err := process.Processes()
	if err != nil {
		return nil, nil
	}

	var list []ProcInfo
	for _, p := range ps {
		name, _ := p.Name()
		memP, _ := p.MemoryPercent()
		cpuP, _ := p.CPUPercent()
		if name != "" && (memP > 0.1 || cpuP > 0.1) {
			list = append(list, ProcInfo{Name: name, Mem: float64(memP), Cpu: cpuP})
		}
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Cpu > list[j].Cpu })
	if len(list) > limit {
		topCPU = append([]ProcInfo{}, list[:limit]...)
	} else {
		topCPU = append([]ProcInfo{}, list...)
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Mem > list[j].Mem })
	if len(list) > limit {
		topRAM = append([]ProcInfo{}, list[:limit]...)
	} else {
		topRAM = append([]ProcInfo{}, list...)
	}

	return topCPU, topRAM
}

// ═══════════════════════════════════════════════════════════════════
//   TEMPERATURE
// ═══════════════════════════════════════════════════════════════════

func checkTemperatureAlert(ctx *AppContext, bot BotAPI) {
	temp := readCPUTemp()
	if temp <= 0 {
		return
	}

	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	if time.Since(ctx.Monitor.LastTempAlert) < 30*time.Minute {
		return
	}

	cfg := ctx.Config
	if temp >= cfg.Temperature.CriticalThreshold {
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf("🔥 *CPU Temperature Critical!*\n\n"+
				"Current: `%.1f°C`\n"+
				"Threshold: `%.0f°C`\n\n"+
				"_Consider checking cooling or reducing load_",
				temp, cfg.Temperature.CriticalThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.State.AddEvent("critical", fmt.Sprintf("CPU temp critical: %.1f°C", temp))
	} else if temp >= cfg.Temperature.WarningThreshold {
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf("🌡 *CPU Temperature Warning*\n\n"+
				"Current: `%.1f°C`\n"+
				"Threshold: `%.0f°C`",
				temp, cfg.Temperature.WarningThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.State.AddEvent("warning", fmt.Sprintf("CPU temp high: %.1f°C", temp))
	}
}

// ═══════════════════════════════════════════════════════════════════
//  TRENDS
// ═══════════════════════════════════════════════════════════════════

func recordTrendPoint(ctx *AppContext) {
	s, ready := ctx.Stats.Get()
	if !ready {
		return
	}

	now := time.Now()
	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	ctx.Monitor.CPUTrend = append(ctx.Monitor.CPUTrend, TrendPoint{Time: now, Value: s.CPU})
	ctx.Monitor.RAMTrend = append(ctx.Monitor.RAMTrend, TrendPoint{Time: now, Value: s.RAM})

	maxPoints := 72
	if len(ctx.Monitor.CPUTrend) > maxPoints {
		ctx.Monitor.CPUTrend = ctx.Monitor.CPUTrend[len(ctx.Monitor.CPUTrend)-maxPoints:]
	}
	if len(ctx.Monitor.RAMTrend) > maxPoints {
		ctx.Monitor.RAMTrend = ctx.Monitor.RAMTrend[len(ctx.Monitor.RAMTrend)-maxPoints:]
	}
}

func getTrendSummary(ctx *AppContext) (cpuGraph, ramGraph string) {
	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	cpuGraph = getMiniGraph(ctx.Monitor.CPUTrend, 12)
	ramGraph = getMiniGraph(ctx.Monitor.RAMTrend, 12)
	return
}

func getMiniGraph(points []TrendPoint, maxPoints int) string {
	if len(points) == 0 {
		return ""
	}
	if len(points) > maxPoints {
		points = points[len(points)-maxPoints:]
	}

	chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var result strings.Builder

	for _, p := range points {
		idx := int(p.Value / 12.5)
		if idx < 0 {
			idx = 0
		}
		if idx > 7 {
			idx = 7
		}
		result.WriteRune(chars[idx])
	}

	return result.String()
}

// ═══════════════════════════════════════════════════════════════════
//  CRITICAL CONTAINERS
// ═══════════════════════════════════════════════════════════════════

func checkCriticalContainers(ctx *AppContext, bot BotAPI) {
	if len(ctx.Config.CriticalContainers) == 0 {
		return
	}

	containers := getCachedContainerList(ctx)
	containerMap := make(map[string]bool)
	for _, c := range containers {
		containerMap[c.Name] = c.Running
	}

	ctx.Monitor.mu.Lock()
	defer ctx.Monitor.mu.Unlock()

	for _, name := range ctx.Config.CriticalContainers {
		running, exists := containerMap[name]

		if !exists || !running {
			if lastAlert, ok := ctx.Monitor.LastCriticalContainerAlert[name]; ok {
				if time.Since(lastAlert) < 10*time.Minute {
					continue
				}
			}

			if !ctx.IsQuietHours() {
				// Use ctx.Tr if available or raw strings
				status := ctx.Tr("status_not_running")
				if !exists {
					status = ctx.Tr("status_not_found")
				}
				msg := fmt.Sprintf(ctx.Tr("crit_cont_alert"), name, status)
				m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
			}

			ctx.Monitor.LastCriticalContainerAlert[name] = time.Now()
			ctx.State.AddEvent("critical", fmt.Sprintf("Critical container %s down", name))
		}
	}
}

func getCachedContainerList(ctx *AppContext) []ContainerInfo {
	ctx.Docker.mu.RLock()
	ttl := time.Duration(ctx.Config.Cache.DockerTTLSeconds) * time.Second
	if time.Since(ctx.Docker.Cache.LastUpdate) < ttl && len(ctx.Docker.Cache.Containers) > 0 {
		result := ctx.Docker.Cache.Containers
		ctx.Docker.mu.RUnlock()
		return result
	}
	ctx.Docker.mu.RUnlock()

	// Assuming getContainerList is defined in docker.go
	containers := getContainerList()

	ctx.Docker.mu.Lock()
	ctx.Docker.Cache.Containers = containers
	ctx.Docker.Cache.LastUpdate = time.Now()
	ctx.Docker.mu.Unlock()

	return containers
}
