package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"nasbot/internal/format"
	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	gopsnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

type MonitorAlert struct {
	Level   string // "critical", "warning"
	Message string
}

type ResourceMonitor interface {
	Check(ctx *AppContext, s *Stats) []MonitorAlert
}

type CPUMonitor struct{}

func (m *CPUMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Config.Notifications.CPU
	if !cfg.Enabled {
		return alerts
	}
	if s.CPU >= cfg.CriticalThreshold {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("🧠 CPU critical: `%.1f%%`", s.CPU)})
	} else if s.CPU >= cfg.WarningThreshold {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf("CPU high: %.1f%%", s.CPU)})
	}
	return alerts
}

type RAMMonitor struct{}

func (m *RAMMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Config.Notifications.RAM
	if !cfg.Enabled {
		return alerts
	}
	if s.RAM >= cfg.CriticalThreshold {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("💾 RAM critical: `%.1f%%`", s.RAM)})
	} else if s.RAM >= cfg.WarningThreshold {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf("RAM high: %.1f%%", s.RAM)})
	}
	return alerts
}

type SwapMonitor struct{}

func (m *SwapMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Config.Notifications.Swap
	if !cfg.Enabled {
		return alerts
	}
	// Note: Swap has no critical threshold check currently
	if s.Swap >= cfg.WarningThreshold {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf("Swap high: %.1f%%", s.Swap)})
	}
	return alerts
}

type SSDMonitor struct{}

func (m *SSDMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Config.Notifications.DiskSSD
	if !cfg.Enabled {
		return alerts
	}
	if s.VolSSD.Used >= cfg.CriticalThreshold {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("💿 SSD critical: `%.1f%%`", s.VolSSD.Used)})
	} else if s.VolSSD.Used >= cfg.WarningThreshold {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf("SSD at %.1f%%", s.VolSSD.Used)})
	}
	return alerts
}

type SecondaryDiskMonitor struct{}

func (m *SecondaryDiskMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	for mountPoint, volStats := range s.SecondaryVols {
		diskCfg, ok := ctx.Config.Notifications.SecondaryDisks[mountPoint]
		if !ok {
			diskCfg = ResourceConfig{Enabled: true, WarningThreshold: 90.0, CriticalThreshold: 95.0}
		}
		if diskCfg.Enabled {
			if volStats.Used >= diskCfg.CriticalThreshold {
				alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("🗄 Disk %s critical: `%.1f%%`", mountPoint, volStats.Used)})
			} else if volStats.Used >= diskCfg.WarningThreshold {
				alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf("Disk %s at %.1f%%", mountPoint, volStats.Used)})
			}
		}
	}
	return alerts
}

type SMARTMonitor struct{}

func (m *SMARTMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	if !ctx.Config.Notifications.SMART.Enabled {
		return alerts
	}

	ctx.Monitor.Mu.Lock()
	needsCheck := time.Since(ctx.Monitor.SmartLastCheckTime) >= 10*time.Minute
	ctx.Monitor.Mu.Unlock()

	var cache map[string]model.SmartResult
	if needsCheck {
		newCache := make(map[string]model.SmartResult)
		for _, dev := range getSmartDevices(ctx) {
			temp, health := readDiskSMART(dev)
			newCache[dev] = model.SmartResult{Temp: temp, Health: health}
		}
		ctx.Monitor.Mu.Lock()
		ctx.Monitor.SmartCache = newCache
		ctx.Monitor.SmartLastCheckTime = time.Now()
		ctx.Monitor.Mu.Unlock()
		cache = newCache
	} else {
		ctx.Monitor.Mu.Lock()
		cache = make(map[string]model.SmartResult)
		for k, v := range ctx.Monitor.SmartCache {
			cache[k] = v
		}
		ctx.Monitor.Mu.Unlock()
	}

	for dev, res := range cache {
		if strings.Contains(strings.ToUpper(res.Health), "FAIL") {
			alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("🚨 Disk %s FAILING — backup now!", dev)})
		}
		if res.Temp > 0 && float64(res.Temp) >= ctx.Config.Temperature.CriticalThreshold {
			alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf("🔥 Disk %s temp critical: %d°C", dev, res.Temp)})
		}
	}
	return alerts
}

func monitorAlerts(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	ticker := time.NewTicker(time.Duration(ctx.Config.Intervals.MonitorSeconds) * time.Second)
	defer ticker.Stop()

	monitors := []ResourceMonitor{
		&CPUMonitor{},
		&RAMMonitor{},
		&SwapMonitor{},
		&SSDMonitor{},
		&SecondaryDiskMonitor{},
		&SMARTMonitor{},
	}

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
			var allAlerts []MonitorAlert

			for _, m := range monitors {
				alerts := m.Check(ctx, &s)
				allAlerts = append(allAlerts, alerts...)
				for _, a := range alerts {
					if a.Level == "critical" {
						criticalAlerts = append(criticalAlerts, a.Message)
					}
				}
			}

			cfg := ctx.Config
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

			for _, a := range allAlerts {
				// We still add them to state, mapping critical string appropriately if needed
				// For the UI state, it expects "critical" or "warning" with the raw text, but without markdown
				// We'll clean up markdown for state log slightly, or just use as is.
				cleanMsg := strings.ReplaceAll(a.Message, "`", "")
				ctx.State.AddEvent(a.Level, cleanMsg)
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
