package app

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
	gopsnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

func monitorAlerts(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	ticker := time.NewTicker(time.Duration(ctx.Config.Intervals.MonitorSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-runCtx.Done():
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
			for mountPoint, volStats := range s.SecondaryVols {
				diskCfg, ok := cfg.Notifications.SecondaryDisks[mountPoint]
				if !ok {
					// Use a default config if not configured
					diskCfg = ResourceConfig{Enabled: true, WarningThreshold: 90.0, CriticalThreshold: 95.0}
				}
				if diskCfg.Enabled && volStats.Used >= diskCfg.CriticalThreshold {
					criticalAlerts = append(criticalAlerts, fmt.Sprintf("🗄 Disk %s critical: `%.1f%%`", mountPoint, volStats.Used))
				}
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
			ctx.Monitor.Mu.Lock()
			lastAlert := ctx.Monitor.LastCriticalAlert
			ctx.Monitor.Mu.Unlock()

			if len(criticalAlerts) > 0 && time.Since(lastAlert) >= cooldown && !ctx.IsQuietHours() {
				msg := "🚨 *Critical*\n\n" + strings.Join(criticalAlerts, "\n")
				m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
				m.ParseMode = "Markdown"

				kb := tgbotapi.NewInlineKeyboardMarkup(
					tgbotapi.NewInlineKeyboardRow(
						tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("analyze_with_ai"), "ai_analyze_critical"),
					),
				)
				m.ReplyMarkup = kb

				safeSend(bot, m)
				ctx.Monitor.Mu.Lock()
				ctx.Monitor.LastCriticalAlert = time.Now()
				ctx.Monitor.Mu.Unlock()
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
			for mountPoint, volStats := range s.SecondaryVols {
				diskCfg, ok := cfg.Notifications.SecondaryDisks[mountPoint]
				if !ok {
					diskCfg = ResourceConfig{Enabled: true, WarningThreshold: 90.0, CriticalThreshold: 95.0}
				}
				if diskCfg.Enabled && volStats.Used >= diskCfg.WarningThreshold && volStats.Used < diskCfg.CriticalThreshold {
					ctx.State.AddEvent("warning", fmt.Sprintf("Disk %s at %.1f%%", mountPoint, volStats.Used))
				}
			}
		}
	}
}

func statsCollector(ctx *AppContext, runCtx context.Context) {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	var lastNet []gopsnet.IOCountersStat
	var lastNetTime time.Time

	ticker := time.NewTicker(time.Duration(ctx.Config.Intervals.StatsSeconds) * time.Second)
	defer ticker.Stop()

	collect := func() {
		c, _ := cpu.Percent(0, false)
		v, _ := mem.VirtualMemory()
		sw, _ := mem.SwapMemory()
		l, _ := load.Avg()
		h, _ := host.Info()
		var dSSD *disk.UsageStat
		if ctx.Config.Paths.SSD != "" {
			dSSD, _ = disk.Usage(ctx.Config.Paths.SSD)
		}

		secVols := make(map[string]VolumeStats)
		partitions, err := disk.Partitions(true)
		if err == nil {
			for _, p := range partitions {
				// Filter out loop devices, virtual filesystems, and docker overlays
				if strings.HasPrefix(p.Device, "/dev/loop") || p.Fstype == "squashfs" || p.Fstype == "tmpfs" || p.Fstype == "devtmpfs" || p.Fstype == "overlay" || p.Fstype == "proc" || p.Fstype == "sysfs" || p.Fstype == "cgroup" || p.Fstype == "nsfs" || p.Fstype == "bpf" || p.Fstype == "tracefs" {
					continue
				}
				// Also skip if it's clearly not a real device/mount
				if p.Device == "none" || p.Device == "sunrpc" || p.Device == "devpts" {
					continue
				}
				// Only include specific data mount points (internal/external HDDs)
				if !(strings.HasPrefix(p.Mountpoint, "/mnt") || strings.HasPrefix(p.Mountpoint, "/media")) {
					continue
				}
				if p.Mountpoint == ctx.Config.Paths.SSD || p.Mountpoint == "/boot" || p.Mountpoint == "/boot/efi" {
					continue
				}
				dSec, err := disk.Usage(p.Mountpoint)
				if err == nil {
					secVols[p.Mountpoint] = VolumeStats{Used: dSec.UsedPercent, Free: dSec.Free}
				}
			}
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

		currentNet, _ := gopsnet.IOCounters(false)
		var rxMbps, txMbps float64
		var rxTotal, txTotal float64
		if len(currentNet) > 0 {
			rxTotal = float64(currentNet[0].BytesRecv) / 1024 / 1024
			txTotal = float64(currentNet[0].BytesSent) / 1024 / 1024

			if lastNet != nil && !lastNetTime.IsZero() {
				elapsed := time.Since(lastNetTime).Seconds()
				if elapsed > 0 {
					rxBytes := currentNet[0].BytesRecv - lastNet[0].BytesRecv
					txBytes := currentNet[0].BytesSent - lastNet[0].BytesSent
					// Convert bytes/sec to Megabits/sec (Mbps)
					rxMbps = (float64(rxBytes) * 8 / 1000000) / elapsed
					txMbps = (float64(txBytes) * 8 / 1000000) / elapsed
				}
			}
			lastNet = currentNet
			lastNetTime = time.Now()
		}

		topCPU, topRAM := getTopProcesses(5)
		cVal := 0.0
		if len(c) > 0 {
			cVal = c[0]
		}

		newStats := Stats{
			CPU:           format.SafeFloat([]float64{cVal}, 0),
			RAM:           v.UsedPercent,
			RAMFreeMB:     v.Available / 1024 / 1024,
			RAMTotalMB:    v.Total / 1024 / 1024,
			Swap:          sw.UsedPercent,
			Load1m:        l.Load1,
			Load5m:        l.Load5,
			Load15m:       l.Load15,
			Uptime:        h.Uptime,
			ReadMBs:       readMBs,
			WriteMBs:      writeMBs,
			DiskUtil:      diskUtil,
			NetRxMbps:     rxMbps,
			NetTxMbps:     txMbps,
			NetRxTotalMB:  rxTotal,
			NetTxTotalMB:  txTotal,
			TopCPU:        topCPU,
			TopRAM:        topRAM,
			SecondaryVols: secVols,
		}

		if dSSD != nil {
			newStats.VolSSD = VolumeStats{Used: dSSD.UsedPercent, Free: dSSD.Free}
		}

		ctx.Stats.Set(newStats)
	}

	collect()
	for {
		select {
		case <-runCtx.Done():
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

	ctx.Monitor.Mu.Lock()
	if time.Since(ctx.Monitor.LastTempAlert) < 30*time.Minute {
		ctx.Monitor.Mu.Unlock()
		return
	}
	ctx.Monitor.Mu.Unlock()

	cfg := ctx.Config
	if temp >= cfg.Temperature.CriticalThreshold {
		var m tgbotapi.MessageConfig
		sendMsg := false
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf("🔥 *CPU Temperature Critical!*\n\nCurrent: `%.1f°C`\nThreshold: `%.0f°C`\n\n_Consider checking cooling or reducing load_", temp, cfg.Temperature.CriticalThreshold)
			m = tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			sendMsg = true
		}

		ctx.Monitor.Mu.Lock()
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.Monitor.Mu.Unlock()

		if sendMsg {
			safeSend(bot, m)
		}
		ctx.State.AddEvent("critical", fmt.Sprintf("CPU temp critical: %.1f°C", temp))
	} else if temp >= cfg.Temperature.WarningThreshold {
		var m tgbotapi.MessageConfig
		sendMsg := false
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf("🌡 *CPU Temperature Warning*\n\nCurrent: `%.1f°C`\nThreshold: `%.0f°C`", temp, cfg.Temperature.WarningThreshold)
			m = tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			sendMsg = true
		}

		ctx.Monitor.Mu.Lock()
		ctx.Monitor.LastTempAlert = time.Now()
		ctx.Monitor.Mu.Unlock()

		if sendMsg {
			safeSend(bot, m)
		}
		ctx.State.AddEvent("warning", fmt.Sprintf("CPU temp high: %.1f°C", temp))
	}
}

func recordTrendPoint(ctx *AppContext) {
	s, ready := ctx.Stats.Get()
	if !ready {
		return
	}

	now := time.Now()
	ctx.Monitor.Mu.Lock()
	defer ctx.Monitor.Mu.Unlock()

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
	ctx.Monitor.Mu.Lock()
	defer ctx.Monitor.Mu.Unlock()

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

	for _, name := range ctx.Config.CriticalContainers {
		running, exists := containerMap[name]
		if !exists || !running {
			ctx.Monitor.Mu.Lock()
			lastAlert, ok := ctx.Monitor.LastCriticalContainerAlert[name]
			ctx.Monitor.Mu.Unlock()

			if ok && time.Since(lastAlert) < 10*time.Minute {
				continue
			}

			var m tgbotapi.MessageConfig
			sendMsg := false
			if !ctx.IsQuietHours() {
				status := ctx.Tr("status_not_running")
				if !exists {
					status = ctx.Tr("status_not_found")
				}
				msg := fmt.Sprintf(ctx.Tr("crit_cont_alert"), name, status)
				m = tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				sendMsg = true
			}

			ctx.Monitor.Mu.Lock()
			ctx.Monitor.LastCriticalContainerAlert[name] = time.Now()
			ctx.Monitor.Mu.Unlock()

			if sendMsg {
				safeSend(bot, m)
			}
			ctx.State.AddEvent("critical", fmt.Sprintf("Critical container %s down", name))
		}
	}
}

func getCachedContainerList(ctx *AppContext) []ContainerInfo {
	ctx.Docker.Mu.RLock()
	ttl := time.Duration(ctx.Config.Cache.DockerTTLSeconds) * time.Second
	if time.Since(ctx.Docker.Cache.LastUpdate) < ttl && len(ctx.Docker.Cache.Containers) > 0 {
		result := ctx.Docker.Cache.Containers
		ctx.Docker.Mu.RUnlock()
		return result
	}
	ctx.Docker.Mu.RUnlock()

	containers := getContainerList()

	ctx.Docker.Mu.Lock()
	ctx.Docker.Cache.Containers = containers
	ctx.Docker.Cache.LastUpdate = time.Now()
	ctx.Docker.Mu.Unlock()

	return containers
}
