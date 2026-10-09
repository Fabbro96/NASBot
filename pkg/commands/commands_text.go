package commands

import (
	"context"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"nasbot/internal/format"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// ═══════════════════════════════════════════════════════════════════
//  SAFE FORMATTING OF TRANSLATED TEMPLATES
// ═══════════════════════════════════════════════════════════════════

// trf formats the translated template key with args.
//
// It refuses to let a template/arguments mismatch reach the user: fmt would
// append a diagnostic dump ("%!(EXTRA float64=50, string=0G)") to the message,
// which both leaks internals and makes assertions on the rendered text pass
// for the wrong reason. A mismatch is reported explicitly instead.
func trf(tr func(string) string, key string, args ...any) string {
	tmpl := tr(key)
	if got, want := countVerbs(tmpl), len(args); got != want {
		return fmt.Sprintf("⚠️ [fmt:%s] template has %d verb(s) for %d argument(s)", key, got, want)
	}
	return fmt.Sprintf(tmpl, args...)
}

// countVerbs counts the formatting verbs of a template, skipping escaped
// percent signs ("%%") and flag/width/precision runs ("%2.0f", "%-5s", "%02d").
func countVerbs(tmpl string) int {
	const specChars = "+-# 0123456789."
	count := 0
	for i := 0; i < len(tmpl); i++ {
		if tmpl[i] != '%' {
			continue
		}
		if i+1 < len(tmpl) && tmpl[i+1] == '%' {
			i++ // literal percent
			continue
		}
		j := i + 1
		for j < len(tmpl) && strings.IndexByte(specChars, tmpl[j]) >= 0 {
			j++
		}
		if j < len(tmpl) {
			count++
			i = j
		}
	}
	return count
}

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
	sections = append(sections, trf(tr, "status_title", now.Format("15:04")))

	// Section 2: Compute (CPU, RAM, Swap)
	var computeLines []string
	cpuGraph, ramGraph := getTrendSummary(ctx)

	cpuLine := trf(tr, "cpu_fmt", format.MakeProgressBar(s.CPU), s.CPU)
	if cpuGraph != "" {
		cpuLine += "  `" + cpuGraph + "`"
	}
	computeLines = append(computeLines, cpuLine)

	ramLine := trf(tr, "ram_fmt", format.MakeProgressBar(s.RAM), s.RAM)
	if ramGraph != "" {
		ramLine += "  `" + ramGraph + "`"
	}
	computeLines = append(computeLines, ramLine)

	if s.Swap > swapVisiblePct {
		computeLines = append(computeLines, trf(tr, "swap_fmt", format.MakeProgressBar(s.Swap), s.Swap))
	}
	sections = append(sections, strings.Join(computeLines, "\n"))

	// Section 3: Storage (SSD & Secondary Disks) — separated visually
	sections = append(sections, "")
	var storageLines []string
	storageLines = append(storageLines, trf(tr, "ssd_fmt", s.VolSSD.Used, format.FormatBytes(s.VolSSD.Free)))
	for _, m := range volumeMounts(s.SecondaryVols) {
		vol := s.SecondaryVols[m]
		storageLines = append(storageLines, trf(tr, "disk_sec_fmt", diskDisplayName(m), vol.Used, format.FormatBytes(vol.Free)))
	}
	sections = append(sections, strings.Join(storageLines, "\n"))

	// Section 4: Disk I/O (if active)
	if s.DiskUtil > diskIOVisiblePct {
		ioLine := trf(tr, "disk_io_fmt", s.DiskUtil)
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			ioLine += trf(tr, "disk_rw_fmt", s.ReadMBs, s.WriteMBs)
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
	footerLines = append(footerLines, trf(tr, "uptime_fmt", format.FormatUptime(s.Uptime)))
	sections = append(sections, strings.Join(footerLines, "\n"))

	return strings.Join(sections, "\n")
}

func escapeMarkdown(s string) string {
	s = strings.ReplaceAll(s, "_", "\\_")
	s = strings.ReplaceAll(s, "*", "\\*")
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "[", "\\[")
	return s
}

// diskDisplayName returns the escaped short name of a mount point, safe to
// inline in a Markdown message: a mount like /mnt/data_archive must not inject
// entities (Telegram answers 400 and the message is re-sent as plain text,
// asterisks included).
func diskDisplayName(mount string) string {
	return escapeMarkdown(mountShortName(mount))
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
		b.WriteString(trf(tr, "temp_cpu", cpuIcon, cpuTemp, cpuStatus))
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
	b.WriteString(trf(tr, "net_local", getLocalIP(localCtx)))

	publicCtx, cancelPublic := context.WithTimeout(context.Background(), netTimeout)
	defer cancelPublic()
	b.WriteString(trf(tr, "net_public", getPublicIP(ctx, publicCtx)))

	s, ready := ctx.Stats.Get()
	if ready {
		b.WriteString(tr("net_traffic_title"))
		b.WriteString(trf(tr, "net_rx", s.NetRxMbps, formatTotalMB(s.NetRxTotalMB)))
		b.WriteString(trf(tr, "net_tx", s.NetTxMbps, formatTotalMB(s.NetTxTotalMB)))
	}

	return b.String()
}

func GetNetworkText(ctx *AppContext) string { return getNetworkText(ctx) }

func getTopProcText(ctx *AppContext) string {
	tr := ctx.Tr
	procs, err := collectTopProcesses(maxTopProcesses, maxProcNameLen)
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err)
	}
	if len(procs) == 0 {
		return tr("top_none")
	}

	var b strings.Builder
	b.WriteString(tr("top_title"))
	b.WriteString(tr("top_header"))
	for _, p := range procs {
		b.WriteString(fmt.Sprintf("`%-5s %-4s %-4s %s`\n", p.PID, p.CPU, p.MEM, p.Name))
	}

	return b.String()
}

func GetTopProcText(ctx *AppContext) string { return getTopProcText(ctx) }

// ═══════════════════════════════════════════════════════════════════
//  PROCESS LIST PARSING (shared by /top and the process manager)
// ═══════════════════════════════════════════════════════════════════

// psProcess is one row of `ps -Ao pid,comm,pcpu,pmem --sort=-pcpu`.
type psProcess struct {
	PID  string
	Name string
	CPU  string
	MEM  string
}

// collectTopProcesses runs ps and returns at most maxCount processes, sorted
// by CPU as ps reports them. Name is truncated to maxNameLen runes.
//
// /top and /processes used to parse ps independently and diverged (one cut the
// name by bytes, the other by runes); they now share this function.
func collectTopProcesses(maxCount, maxNameLen int) ([]psProcess, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), psTimeout)
	defer cancel()

	out, err := runCommandOutput(reqCtx, "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	if err != nil {
		// Minimal environments (Alpine without procps, whose ps ignores
		// --sort) fail here. Try the host's ps through the /hostfs bind mount
		// from docker-compose.yml (a busybox chroot applet is enough), then
		// native gopsutil enumeration, which unlike the monitor path below
		// keeps every named process: gopsutil reports 0% CPU on the first
		// sample, so filtering by activity would always come back empty.
		if hostOut, hostErr := runCommandOutput(reqCtx, "chroot", "/hostfs", "ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu"); hostErr == nil {
			out = hostOut
			err = nil
		}
	}
	if err != nil {
		if procs := collectTopProcessesNative(maxCount, maxNameLen); len(procs) > 0 {
			return procs, nil
		}
		return nil, err
	}

	lines := strings.Split(string(out), "\n")
	procs := make([]psProcess, 0, maxCount)
	for i := 1; i < len(lines) && len(procs) < maxCount; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		name := fields[1]
		if maxNameLen > 2 {
			if r := []rune(name); len(r) > maxNameLen {
				name = string(r[:maxNameLen-2]) + ".."
			}
		}
		procs = append(procs, psProcess{
			PID:  fields[0],
			Name: name,
			CPU:  fields[2],
			MEM:  fields[3],
		})
	}
	return procs, nil
}

// collectTopProcessesNative enumerates processes via gopsutil instead of ps.
// Unlike getTopProcesses in internal/app (which drops anything under 0.1% CPU
// or memory), it keeps every named process: CPUPercent is ~0 on the first
// sample, so filtering would always return an empty list here.
func collectTopProcessesNative(maxCount, maxNameLen int) []psProcess {
	psList, err := process.Processes()
	if err != nil {
		return nil
	}
	type procData struct {
		pid  int32
		name string
		cpu  float64
		mem  float32
	}
	var data []procData
	for _, p := range psList {
		name, err := p.Name()
		if err != nil || name == "" {
			continue
		}
		cpu, _ := p.CPUPercent()
		mem, _ := p.MemoryPercent()
		data = append(data, procData{
			pid:  p.Pid,
			name: name,
			cpu:  cpu,
			mem:  mem,
		})
	}
	sort.Slice(data, func(i, j int) bool {
		if data[i].cpu == data[j].cpu {
			return data[i].mem > data[j].mem
		}
		return data[i].cpu > data[j].cpu
	})
	if len(data) > maxCount {
		data = data[:maxCount]
	}
	procs := make([]psProcess, 0, len(data))
	for _, d := range data {
		name := d.name
		if maxNameLen > 2 {
			if r := []rune(name); len(r) > maxNameLen {
				name = string(r[:maxNameLen-2]) + ".."
			}
		}
		procs = append(procs, psProcess{
			PID:  strconv.Itoa(int(d.pid)),
			Name: name,
			CPU:  fmt.Sprintf("%.1f", d.cpu),
			MEM:  fmt.Sprintf("%.1f", d.mem),
		})
	}
	return procs
}

func getHelpText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(tr("help_intro"))
	b.WriteString("\n")

	b.WriteString(tr("help_mon"))
	b.WriteString(fmt.Sprintf("/status — %s\n", tr("cmd_status_desc")))
	b.WriteString(fmt.Sprintf("/quick — %s\n", tr("cmd_quick_desc")))
	b.WriteString(fmt.Sprintf("/temp — %s\n", tr("cmd_temp_desc")))
	b.WriteString(fmt.Sprintf("/top — %s\n", tr("cmd_top_desc")))
	b.WriteString(fmt.Sprintf("/sysinfo — %s\n", tr("cmd_sysinfo_desc")))
	b.WriteString(fmt.Sprintf("/diskpred — %s\n\n", tr("cmd_diskpred_desc")))
	b.WriteString("\n")

	b.WriteString(tr("help_docker"))
	b.WriteString(fmt.Sprintf("/docker — %s\n", tr("cmd_docker_desc")))
	b.WriteString(fmt.Sprintf("/dstats — %s\n", tr("cmd_dstats_desc")))
	b.WriteString(fmt.Sprintf("/kill `name` — %s\n", tr("cmd_kill_desc")))
	b.WriteString(fmt.Sprintf("/logsearch `name` `keyword` — %s\n", tr("cmd_logsearch_desc")))
	b.WriteString(fmt.Sprintf("/restartdocker — %s\n\n", tr("cmd_restartdocker_desc")))
	b.WriteString("\n")

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
	b.WriteString(fmt.Sprintf("/changelog — %s\n", tr("cmd_changelog_desc")))
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
		b.WriteString(fmt.Sprintf("\n%s\n", trf(tr, "help_every_days", reportInterval)))
	}

	if quiet.Enabled {
		b.WriteString(fmt.Sprintf("\n%s\n",
			trf(tr, "help_quiet",
				quiet.Start.Hour, quiet.Start.Minute,
				quiet.End.Hour, quiet.End.Minute)))
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

	// Each translated line is formatted on its own: concatenating five
	// templates and passing five arguments shifts every value by one slot
	// (uptime ends up in the "collecting stats" slot) and leaves an
	// %!(EXTRA …) tail.
	var b strings.Builder
	b.WriteString(trf(tr, "ping_pong", status))
	b.WriteString("\n\n")
	b.WriteString(statusText)
	b.WriteString("\n\n")
	b.WriteString(trf(tr, "ping_uptime", format.FormatDuration(uptime)))
	b.WriteString("\n\n")
	b.WriteString(trf(tr, "ping_collecting", ready))
	b.WriteString("\n")
	b.WriteString(trf(tr, "ping_last_check", now.Format("15:04:05")))
	b.WriteString("\n\n")
	b.WriteString(tr("ping_alive"))

	return b.String()
}

func GetPingText(ctx *AppContext) string { return getPingText(ctx) }

func getConfigText(ctx *AppContext) string {
	tr := ctx.Tr
	c := cfg(ctx)
	var b strings.Builder
	b.WriteString(tr("config_title"))

	// Reports
	b.WriteString(tr("cfg_reports"))
	if c.Reports.Enabled {
		b.WriteString(trf(tr, "cfg_every_days", c.Reports.IntervalDays))
		for _, t := range c.Reports.Times {
			b.WriteString(fmt.Sprintf("%02d:%02d ", t.Hour, t.Minute))
		}
		b.WriteString("\n")
	} else {
		b.WriteString(tr("cfg_disabled") + "\n")
	}

	// Quiet hours
	b.WriteString(tr("cfg_quiet"))
	if c.QuietHours.Enabled {
		b.WriteString(trf(tr, "cfg_quiet_fmt",
			c.QuietHours.StartHour, c.QuietHours.StartMinute,
			c.QuietHours.EndHour, c.QuietHours.EndMinute))
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
	writeNotifLine("CPU", c.Notifications.CPU)
	writeNotifLine("RAM", c.Notifications.RAM)
	// Swap is a full ResourceConfig: rebuilding it field by field used to drop
	// CriticalThreshold from this summary.
	writeNotifLine("Swap", c.Notifications.Swap)
	writeNotifLine("SSD", c.Notifications.DiskSSD)
	for _, mount := range configMounts(c.Notifications.SecondaryDisks) {
		writeNotifLine("Disk "+diskDisplayName(mount), c.Notifications.SecondaryDisks[mount])
	}
	// DiskIO has no critical threshold in the config type.
	writeNotifLine("I/O", ResourceConfig{Enabled: c.Notifications.DiskIO.Enabled, WarningThreshold: c.Notifications.DiskIO.WarningThreshold})
	b.WriteString(fmt.Sprintf("  SMART: %s\n", format.BoolToEmoji(c.Notifications.SMART.Enabled)))

	// Docker
	b.WriteString("\n*Docker:*\n")
	if c.Docker.Watchdog.Enabled {
		b.WriteString(fmt.Sprintf("  Watchdog: ✅ %dm timeout\n", c.Docker.Watchdog.TimeoutMinutes))
	} else {
		b.WriteString("  Watchdog: ❌\n")
	}
	if c.Docker.WeeklyPrune.Enabled {
		b.WriteString(fmt.Sprintf("  Prune: ✅ %s @ %02d:00\n",
			format.TitleCaseWord(c.Docker.WeeklyPrune.Day), c.Docker.WeeklyPrune.Hour))
	} else {
		b.WriteString("  Prune: ❌\n")
	}
	if c.Docker.AutoRestartOnRAMCritical.Enabled {
		// Both thresholds: the RAM one decides whether the tick acts at all, the
		// container one decides which container. Printing only the first left the
		// user unable to explain why a container was or was not restarted.
		b.WriteString(fmt.Sprintf("  Auto-restart: ✅ RAM >%.0f%% · container >%.0f%%\n",
			c.Docker.AutoRestartOnRAMCritical.RAMThreshold,
			heavyContainerMemPercent(c)))
	} else {
		b.WriteString("  Auto-restart: ❌\n")
	}

	// Network watchdog force reboot
	b.WriteString("\n*Network Watchdog:*\n")
	if c.NetworkWatchdog.Enabled {
		if c.NetworkWatchdog.ForceRebootOnDown {
			b.WriteString(fmt.Sprintf("  Force reboot: ✅ after %d min down\n", c.NetworkWatchdog.ForceRebootAfterMins))
		} else {
			b.WriteString("  Force reboot: ❌\n")
		}
	} else {
		b.WriteString("  Enabled: ❌\n")
	}

	// Intervals
	b.WriteString(fmt.Sprintf("\n*Intervals:* Stats %ds · Monitor %ds",
		c.Intervals.StatsSeconds, c.Intervals.MonitorSeconds))

	return b.String()
}

func GetConfigText(ctx *AppContext) string { return getConfigText(ctx) }

// heavyContainerMemPercent returns the per-container memory share that governs
// the Docker auto-restart, with the same fallback the bot applies in
// dockerHeavyContainerThreshold (internal/app/docker_automation.go): a value at
// or below zero is "not configured", never "every container is heavy".
func heavyContainerMemPercent(c *Config) float64 {
	if c != nil && c.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent > 0 {
		return c.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent
	}
	return 20.0
}

func getSysInfoText(ctx *AppContext) string {
	tr := ctx.Tr
	c := cfg(ctx)
	var b strings.Builder
	b.WriteString(tr("sysinfo_title"))

	// Host info
	h, err := host.Info()
	if err == nil {
		b.WriteString(trf(tr, "sysinfo_hostname", h.Hostname))
		b.WriteString(trf(tr, "sysinfo_os", h.Platform, h.PlatformVersion))
		b.WriteString(trf(tr, "sysinfo_kernel", h.KernelVersion))
		b.WriteString(trf(tr, "sysinfo_arch", h.KernelArch))
		b.WriteString(trf(tr, "sysinfo_uptime", format.FormatUptime(h.Uptime)))
		b.WriteString(trf(tr, "sysinfo_boot_time", time.Unix(int64(h.BootTime), 0).In(ctx.State.TimeLocation).Format("02/01/2006 15:04")))
	}

	// CPU info
	cpuInfo, err := cpu.Info()
	if err == nil && len(cpuInfo) > 0 {
		b.WriteString(trf(tr, "sysinfo_cpu", cpuInfo[0].ModelName))
		b.WriteString(trf(tr, "sysinfo_cores", cpuInfo[0].Cores, len(cpuInfo)))
		if cpuInfo[0].Mhz > 0 {
			b.WriteString(trf(tr, "sysinfo_freq", cpuInfo[0].Mhz))
		}
	}

	// Memory info
	v, err := mem.VirtualMemory()
	if err == nil {
		b.WriteString(trf(tr, "sysinfo_ram", float64(v.Total)/1024/1024/1024))
	}

	// Disk info
	b.WriteString(tr("sysinfo_disks"))
	paths := []struct {
		name string
		path string
	}{
		{name: "SSD", path: c.Paths.SSD},
	}
	for _, mount := range configMounts(c.Notifications.SecondaryDisks) {
		paths = append(paths, struct {
			name string
			path string
		}{name: "Disk " + diskDisplayName(mount), path: mount})
	}
	for _, p := range paths {
		if p.path == "" {
			continue
		}
		d, err := disk.Usage(p.path)
		if err == nil {
			b.WriteString(trf(tr, "sysinfo_disk_entry", p.name, p.path, float64(d.Total)/1024/1024/1024))
		}
	}

	// Go runtime info
	b.WriteString(trf(tr, "sysinfo_version", getVersion()))
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(trf(tr, "sysinfo_go", buildInfo.GoVersion))
	}

	return b.String()
}

// getCmdResultText renders the reply of a successful /cmd run: the command that
// was executed, then its output.
//
// The command line is display-sanitized data (see displayCommandLine), not a
// translation: it is the caller's own input, and displayCommandLine has already
// replaced every character that could close the code span the template puts it
// in. The output keeps going through cmd_title, whose template still owns the
// fence around it — changing that key's arity is not needed and would have to
// land in every language at once.
func getCmdResultText(ctx *AppContext, commandLine, output string) string {
	return trf(ctx.Tr, "cmd_running", commandLine) + "\n\n" + trf(ctx.Tr, "cmd_title", output)
}

func getVersionText(ctx *AppContext) string {
	tr := ctx.Tr
	var b strings.Builder
	b.WriteString(trf(tr, "version_title", getVersion()))

	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(trf(tr, "version_go", buildInfo.GoVersion))
	}

	h, err := host.Info()
	if err == nil {
		b.WriteString(trf(tr, "version_arch", h.KernelArch))
		b.WriteString(trf(tr, "version_os", h.Platform, h.PlatformVersion))
	}

	ctx.Bot.Mu.Lock()
	startTime := ctx.Bot.StartTime
	ctx.Bot.Mu.Unlock()
	b.WriteString(trf(tr, "version_uptime", format.FormatDuration(time.Since(startTime))))

	return b.String()
}

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
