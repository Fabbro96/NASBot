package app

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"

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

// thresholdReached reports whether currentValue crossed threshold.
// A threshold <= 0 means "not configured": the config sanitizer accepts 0 as a
// valid minimum, and `value >= 0` is true for every reading, which turned
// "critical_threshold": 0 into a critical alert every 30 minutes with the real
// temperature, and "warning_threshold": 0 into permanent stress on CPU, RAM,
// Swap and SSD.
func thresholdReached(currentValue, threshold float64) bool {
	if threshold <= 0 {
		return false
	}
	return currentValue >= threshold
}

type CPUMonitor struct{}

func (m *CPUMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Cfg().Notifications.CPU
	if !cfg.Enabled {
		return alerts
	}
	if thresholdReached(s.CPU, cfg.CriticalThreshold) {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_cpu_crit"), s.CPU)})
	} else if thresholdReached(s.CPU, cfg.WarningThreshold) {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf(ctx.Tr("mon_cpu_high"), s.CPU)})
	}
	return alerts
}

type RAMMonitor struct{}

func (m *RAMMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Cfg().Notifications.RAM
	if !cfg.Enabled {
		return alerts
	}
	if thresholdReached(s.RAM, cfg.CriticalThreshold) {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_ram_crit"), s.RAM)})
	} else if thresholdReached(s.RAM, cfg.WarningThreshold) {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf(ctx.Tr("mon_ram_high"), s.RAM)})
	}
	return alerts
}

type SwapMonitor struct{}

func (m *SwapMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Cfg().Notifications.Swap
	if !cfg.Enabled {
		return alerts
	}
	// Note: Swap has no critical threshold check currently
	if thresholdReached(s.Swap, cfg.WarningThreshold) {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf(ctx.Tr("mon_swap_high"), s.Swap)})
	}
	return alerts
}

type SSDMonitor struct{}

func (m *SSDMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	cfg := ctx.Cfg().Notifications.DiskSSD
	if !cfg.Enabled {
		return alerts
	}
	if thresholdReached(s.VolSSD.Used, cfg.CriticalThreshold) {
		alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_ssd_crit"), s.VolSSD.Used)})
	} else if thresholdReached(s.VolSSD.Used, cfg.WarningThreshold) {
		alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf(ctx.Tr("mon_ssd_high"), s.VolSSD.Used)})
	}
	return alerts
}

type SecondaryDiskMonitor struct{}

func (m *SecondaryDiskMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	for mountPoint, volStats := range s.SecondaryVols {
		diskCfg, ok := ctx.Cfg().Notifications.SecondaryDisks[mountPoint]
		if !ok {
			diskCfg = ResourceConfig{Enabled: true, WarningThreshold: 90.0, CriticalThreshold: 95.0}
		}
		if diskCfg.Enabled {
			if thresholdReached(volStats.Used, diskCfg.CriticalThreshold) {
				alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_disk_crit"), mountPoint, volStats.Used)})
			} else if thresholdReached(volStats.Used, diskCfg.WarningThreshold) {
				alerts = append(alerts, MonitorAlert{"warning", fmt.Sprintf(ctx.Tr("mon_disk_high"), mountPoint, volStats.Used)})
			}
		}
	}
	return alerts
}

type SMARTMonitor struct{}

func (m *SMARTMonitor) Check(ctx *AppContext, s *Stats) []MonitorAlert {
	var alerts []MonitorAlert
	if !ctx.Cfg().Notifications.SMART.Enabled {
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
			alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_disk_failing"), dev)})
		}
		if res.Temp > 0 && thresholdReached(float64(res.Temp), ctx.Cfg().Temperature.CriticalThreshold) {
			alerts = append(alerts, MonitorAlert{"critical", fmt.Sprintf(ctx.Tr("mon_disk_temp_crit"), dev, res.Temp)})
		}
	}
	return alerts
}

func monitorAlerts(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	ticker := time.NewTicker(time.Duration(ctx.Cfg().Intervals.MonitorSeconds) * time.Second)
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

			cfg := ctx.Cfg()
			cooldown := time.Duration(cfg.Intervals.CriticalAlertCooldownMins) * time.Minute
			ctx.Monitor.Mu.Lock()
			lastAlert := ctx.Monitor.LastCriticalAlert
			ctx.Monitor.Mu.Unlock()

			if len(criticalAlerts) > 0 && time.Since(lastAlert) >= cooldown && !ctx.IsQuietHours() {
				msg := ctx.Tr("critical_title") + strings.Join(criticalAlerts, "\n")
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

// monotonicDelta returns curr-prev, or 0 when the counter went backwards.
// /proc/diskstats and /proc/net/dev counters are not monotonic across a device
// re-enumeration: a USB disk unplugged and plugged back in under the same name
// restarts its counters at zero, and the unsigned subtraction wrapped to about
// 1.8e19, so /status showed "R 18014398509481984 MB/s" until the next sample
// realigned. A counter that moved backwards contributes nothing instead.
func monotonicDelta(curr, prev uint64) uint64 {
	if curr < prev {
		return 0
	}
	return curr - prev
}

// isSecondaryDataMount reports whether a mount point is a secondary data
// volume, i.e. one of the external/internal disks the user cares about.
// The pseudo-filesystem filtering is shared with the disk mount watchdog
// (isVirtualOrIgnoredFS) so a mount on cgroup2 or pstore cannot be a data
// volume in one place and be watched as a disk in the other.
func isSecondaryDataMount(mountpoint string) bool {
	return strings.HasPrefix(mountpoint, "/mnt") || strings.HasPrefix(mountpoint, "/media")
}

func statsCollector(ctx *AppContext, runCtx context.Context) {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	var lastNet []gopsnet.IOCountersStat
	var lastNetTime time.Time

	ticker := time.NewTicker(time.Duration(ctx.Cfg().Intervals.StatsSeconds) * time.Second)
	defer ticker.Stop()

	collect := func() {
		c, _ := cpu.Percent(0, false)
		v, _ := mem.VirtualMemory()
		sw, _ := mem.SwapMemory()
		l, _ := load.Avg()
		h, _ := host.Info()
		var dSSD *disk.UsageStat
		if ctx.Cfg().Paths.SSD != "" {
			dSSD, _ = disk.Usage(ctx.Cfg().Paths.SSD)
		}

		secVols := make(map[string]VolumeStats)
		partitions, err := disk.Partitions(true)
		if err == nil {
			for _, p := range partitions {
				// Single source of truth for pseudo filesystems, loop devices
				// and Docker mounts: the same predicate the disk mount watchdog
				// uses. The old inline list here was missing cgroup2, pstore,
				// ramfs, efivarfs, autofs, binfmt_misc, debugfs and selinuxfs.
				if isVirtualOrIgnoredFS(p.Device, p.Fstype, p.Mountpoint) {
					continue
				}
				// SecondaryVols is the "data volumes" list, so it stays limited
				// to the external mount roots.
				if !isSecondaryDataMount(p.Mountpoint) {
					continue
				}
				if p.Mountpoint == ctx.Cfg().Paths.SSD {
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
						rBytes += monotonicDelta(curr.ReadBytes, prev.ReadBytes)
						wBytes += monotonicDelta(curr.WriteBytes, prev.WriteBytes)
						deltaIOTime := monotonicDelta(curr.IoTime, prev.IoTime)
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

			// len(lastNet) matters: gopsnet can return a non-nil empty slice,
			// and lastNet[0] would have panicked.
			if len(currentNet) > 0 && len(lastNet) > 0 && !lastNetTime.IsZero() {
				elapsed := time.Since(lastNetTime).Seconds()
				if elapsed > 0 {
					rxBytes := monotonicDelta(currentNet[0].BytesRecv, lastNet[0].BytesRecv)
					txBytes := monotonicDelta(currentNet[0].BytesSent, lastNet[0].BytesSent)
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
		if math.IsNaN(cVal) {
			// A CPU total of 0 makes gopsutil return NaN, which used to reach
			// MakeProgressBar and panic the whole bot.
			cVal = 0
		}

		newStats := Stats{
			CPU:           cVal,
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

	cfg := ctx.Cfg()
	if thresholdReached(temp, cfg.Temperature.CriticalThreshold) {
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf(ctx.Tr("temp_crit_alert"), temp, cfg.Temperature.CriticalThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
			// Only consume the cooldown when the alert actually went out. The
			// timestamp used to be written before the quiet-hours check, so a
			// critical temperature during quiet hours started a 30 minute
			// cooldown nobody was ever told about, and a condition that cleared
			// inside that window was never reported at all.
			ctx.Monitor.Mu.Lock()
			ctx.Monitor.LastTempAlert = time.Now()
			ctx.Monitor.Mu.Unlock()
		}
		ctx.State.AddEvent("critical", fmt.Sprintf("CPU temp critical: %.1f°C", temp))
	} else if thresholdReached(temp, cfg.Temperature.WarningThreshold) {
		if !ctx.IsQuietHours() {
			msg := fmt.Sprintf(ctx.Tr("temp_warn_alert"), temp, cfg.Temperature.WarningThreshold)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
			ctx.Monitor.Mu.Lock()
			ctx.Monitor.LastTempAlert = time.Now()
			ctx.Monitor.Mu.Unlock()
		}
		ctx.State.AddEvent("warning", fmt.Sprintf("CPU temp high: %.1f°C", temp))
	}
}

// Trend retention and display widths.
//
// recordTrendPoint runs every 5 minutes, so trendRetainedPoints is 6 hours of
// history. getTrendSummary used to ask for 12 points (1 hour) while 72 were
// kept, so 60 of every 72 collected points were never displayed and the two
// numbers were never tied to each other.
const (
	trendRetainedPoints = 72
	trendGraphPoints    = 24
)

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

	maxPoints := trendRetainedPoints
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

	cpuGraph = getMiniGraph(ctx.Monitor.CPUTrend, trendGraphPoints)
	ramGraph = getMiniGraph(ctx.Monitor.RAMTrend, trendGraphPoints)
	return
}

// getMiniGraph renders the last maxPoints samples as a sparkline of block
// runes. maxPoints <= 0 means "all points".
func getMiniGraph(points []TrendPoint, maxPoints int) string {
	if len(points) == 0 {
		return ""
	}
	if maxPoints > 0 && len(points) > maxPoints {
		points = points[len(points)-maxPoints:]
	}

	chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var result strings.Builder

	for _, p := range points {
		idx := 0
		if !math.IsNaN(p.Value) && p.Value > 0 {
			idx = int(p.Value / 12.5)
		}
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
	if len(ctx.Cfg().CriticalContainers) == 0 {
		return
	}

	containers := getCachedContainerList(ctx)
	containerMap := make(map[string]bool)
	for _, c := range containers {
		containerMap[c.Name] = c.Running
	}

	for _, name := range ctx.Cfg().CriticalContainers {
		running, exists := containerMap[name]
		if !exists || !running {
			ctx.Monitor.Mu.Lock()
			lastAlert, ok := ctx.Monitor.LastCriticalContainerAlert[name]
			ctx.Monitor.Mu.Unlock()

			if ok && time.Since(lastAlert) < 10*time.Minute {
				continue
			}

			if !ctx.IsQuietHours() {
				status := ctx.Tr("status_not_running")
				if !exists {
					status = ctx.Tr("status_not_found")
				}
				msg := fmt.Sprintf(ctx.Tr("crit_cont_alert"), name, status)
				m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
				// The cooldown is consumed only when the alert was delivered:
				// during quiet hours the user got nothing, and a container that
				// came back before the 10 minutes elapsed was never reported.
				ctx.Monitor.Mu.Lock()
				ctx.Monitor.LastCriticalContainerAlert[name] = time.Now()
				ctx.Monitor.Mu.Unlock()
			}
			ctx.State.AddEvent("critical", fmt.Sprintf("Critical container %s down", name))
		}
	}
}

// dockerCacheErrCooldown is how long a failed `docker ps` is remembered before
// the next attempt. Keyed by *DockerManager so two AppContexts (tests, a second
// InitApp) never share it.
//
// TODO(model): this exists only because model.DockerCache has no way to
// distinguish "valid, empty" from "unknown". With a `Valid bool` (or a
// `LastError time.Time`) field on DockerCache this table disappears.
const dockerCacheErrCooldown = 30 * time.Second

var (
	dockerCacheErrMu    model.Mutex
	dockerCacheErrSince = map[*model.DockerManager]time.Time{}
)

// dockerProbeRecentlyFailed reports whether a probe for this manager failed
// within dockerCacheErrCooldown. It does not refresh the marker: a persistent
// outage must not be able to postpone the next attempt forever.
func dockerProbeRecentlyFailed(dm *model.DockerManager) bool {
	dockerCacheErrMu.Lock()
	defer dockerCacheErrMu.Unlock()
	if len(dockerCacheErrSince) > 64 {
		// Keep the table bounded: one entry per manager, pruned lazily.
		cutoff := time.Now().Add(-10 * dockerCacheErrCooldown)
		for k, t := range dockerCacheErrSince {
			if t.Before(cutoff) {
				delete(dockerCacheErrSince, k)
			}
		}
	}
	last, ok := dockerCacheErrSince[dm]
	return ok && time.Since(last) < dockerCacheErrCooldown
}

func markDockerProbeFailed(dm *model.DockerManager) {
	dockerCacheErrMu.Lock()
	defer dockerCacheErrMu.Unlock()
	dockerCacheErrSince[dm] = time.Now()
}

func clearDockerProbeFailed(dm *model.DockerManager) {
	dockerCacheErrMu.Lock()
	defer dockerCacheErrMu.Unlock()
	delete(dockerCacheErrSince, dm)
}

func copyContainerList(in []ContainerInfo) []ContainerInfo {
	if len(in) == 0 {
		return nil
	}
	out := make([]ContainerInfo, len(in))
	copy(out, in)
	return out
}

// getCachedContainerList returns the container list, using the cached copy while
// it is fresh.
//
// Cache contract (the other lane changing getContainerList must preserve it):
//   - A cache entry is VALID as soon as time.Since(Cache.LastUpdate) < ttl.
//     Validity does NOT depend on the number of containers: an empty list is a
//     real answer and must be cached, otherwise a stopped daemon makes
//     checkDockerHealth and checkCriticalContainers run `docker ps` twice every
//     10 seconds for the whole day.
//   - A SUCCESSFUL probe always refreshes Cache.LastUpdate, even when it returns
//     zero containers, and stores a non-nil (possibly empty) slice.
//   - A FAILED probe must NOT touch Cache.Containers nor Cache.LastUpdate, and
//     returns no containers. Serving a stale list as if it were fresh would make
//     "container not found" alerts fire for a Docker outage; serving an empty
//     list as valid would defeat the TTL.
//   - A failed probe is rate limited by dockerCacheErrCooldown so an outage does
//     not turn into a `docker ps` storm. The cache is still not refreshed, so the
//     next successful probe wins.
func getCachedContainerList(ctx *AppContext) []ContainerInfo {
	ttl := time.Duration(ctx.Cfg().Cache.DockerTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 5 * time.Second
	}

	ctx.Docker.Mu.RLock()
	if time.Since(ctx.Docker.Cache.LastUpdate) < ttl {
		result := copyContainerList(ctx.Docker.Cache.Containers)
		ctx.Docker.Mu.RUnlock()
		return result
	}
	ctx.Docker.Mu.RUnlock()

	if dockerProbeRecentlyFailed(ctx.Docker) {
		return nil
	}

	containers, err := getContainerListWithError()
	if err != nil {
		markDockerProbeFailed(ctx.Docker)
		slog.Warn("Docker container list failed, cache left untouched", "err", err)
		return nil
	}
	clearDockerProbeFailed(ctx.Docker)

	// Always a non-nil slice, so "no containers" is cached as a real answer.
	cached := containers
	if cached == nil {
		cached = []ContainerInfo{}
	}
	ctx.Docker.Mu.Lock()
	ctx.Docker.Cache.Containers = cached
	ctx.Docker.Cache.LastUpdate = time.Now()
	ctx.Docker.Mu.Unlock()

	return copyContainerList(containers)
}
