package main

import (
	"context"
	"fmt"
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
			msg := fmt.Sprintf("🔥 *CPU Temperature Critical!*\n\nCurrent: `%.1f°C`\nThreshold: `%.0f°C`\n\n_Consider checking cooling or reducing load_", temp, cfg.Temperature.CriticalThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.State.AddEvent("critical", fmt.Sprintf("CPU temp critical: %.1f°C", temp))
	} else if temp >= cfg.Temperature.WarningThreshold {
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf("🌡 *CPU Temperature Warning*\n\nCurrent: `%.1f°C`\nThreshold: `%.0f°C`", temp, cfg.Temperature.WarningThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.State.AddEvent("warning", fmt.Sprintf("CPU temp high: %.1f°C", temp))
	}
}

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

	containers := getContainerList()

	ctx.Docker.mu.Lock()
	ctx.Docker.Cache.Containers = containers
	ctx.Docker.Cache.LastUpdate = time.Now()
	ctx.Docker.mu.Unlock()

	return containers
}
