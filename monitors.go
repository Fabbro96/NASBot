package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

var (
	// Kernel watchdog state: track last seen signature per event type
	kwLastSignatures map[string]string
	kwInitialized    bool
	kwMutex          sync.Mutex

	// Network watchdog state
	netFailCount     int
	netDownSince     time.Time
	netDownAlertTime time.Time
	netDNSAlertTime  time.Time
	netMutex         sync.Mutex

	// RAID watchdog state
	raidLastSignature string
	raidDownSince     time.Time
	raidAlertTime     time.Time
	raidMutex         sync.Mutex
)

// kernelEventType defines a class of critical kernel events
type kernelEventType struct {
	Name     string
	TrKey    string
	Keywords []string
}

var kernelEventTypes = []kernelEventType{
	{
		Name:  "OOM",
		TrKey: "oom_alert",
		Keywords: []string{
			"out of memory",
			"oom-kill",
			"oom kill",
			"killed process",
			"memory cgroup out of memory",
			"oom_reaper",
		},
	},
	{
		Name:  "KernelPanic",
		TrKey: "kernel_panic",
		Keywords: []string{
			"kernel panic",
			"kernel bug",
			"bug: unable to handle",
			"oops:",
			"general protection fault",
			"rcu_sched self-detected stall",
		},
	},
	{
		Name:  "FSReadOnly",
		TrKey: "fs_readonly",
		Keywords: []string{
			"remounting filesystem read-only",
			"remount read-only",
			"ext4_abort",
			"abort (dev",
			"forcing read-only",
		},
	},
	{
		Name:  "IOError",
		TrKey: "io_error",
		Keywords: []string{
			"i/o error",
			"buffer i/o error",
			"blk_update_request: i/o error",
			"ata error",
			"medium error",
			"end_request: i/o error",
		},
	},
	{
		Name:  "HungTask",
		TrKey: "hung_task",
		Keywords: []string{
			"info: task .* blocked for more than",
			"hung_task_timeout",
		},
	},
}

// ═══════════════════════════════════════════════════════════════════
//  AUTONOMOUS MANAGER (automatic decisions)
// ═══════════════════════════════════════════════════════════════════

func autonomousManager(bot *tgbotapi.BotAPI) {
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
		slog.Info(fmt.Sprintf(tr("kw_started"), cfg.KernelWatchdog.CheckIntervalSecs))
	}
	if cfg.NetworkWatchdog.Enabled {
		slog.Info(fmt.Sprintf(tr("netwd_started"), cfg.NetworkWatchdog.CheckIntervalSecs))
	}
	if cfg.RaidWatchdog.Enabled {
		slog.Info(fmt.Sprintf(tr("raidwd_started"), cfg.RaidWatchdog.CheckIntervalSecs))
	}

	for {
		select {
		case <-ticker.C:
			statsMutex.RLock()
			s := statsCache
			ready := statsReady
			statsMutex.RUnlock()

			if !ready {
				continue
			}

			// Check stress for enabled resources only
			if cfg.StressTracking.Enabled {
				if cfg.Notifications.DiskIO.Enabled {
					checkResourceStress(bot, "HDD", s.DiskUtil, cfg.Notifications.DiskIO.WarningThreshold)
				}
				if cfg.Notifications.CPU.Enabled {
					checkResourceStress(bot, "CPU", s.CPU, cfg.Notifications.CPU.WarningThreshold)
				}
				if cfg.Notifications.RAM.Enabled {
					checkResourceStress(bot, "RAM", s.RAM, cfg.Notifications.RAM.WarningThreshold)
				}
				if cfg.Notifications.Swap.Enabled {
					checkResourceStress(bot, "Swap", s.Swap, cfg.Notifications.Swap.WarningThreshold)
				}
				if cfg.Notifications.DiskSSD.Enabled {
					checkResourceStress(bot, "SSD", s.VolSSD.Used, cfg.Notifications.DiskSSD.WarningThreshold)
				}
			}

			// Check temperature
			if cfg.Temperature.Enabled {
				checkTemperatureAlert(bot)
			}

			// Check critical RAM for auto-restart
			if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
				if s.RAM >= cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold {
					handleCriticalRAM(bot, s)
				}
			}

			// Clean restart counter (every hour)
			cleanRestartCounter()

			// Docker watchdog
			if cfg.Docker.Watchdog.Enabled {
				checkDockerHealth(bot)
			}

			// Check for unexpected container stops
			checkContainerStates(bot)

			// Check critical containers
			checkCriticalContainers(bot)

			// Weekly prune
			if cfg.Docker.WeeklyPrune.Enabled {
				checkWeeklyPrune(bot)
			}

		case <-diskTicker.C:
			recordDiskUsage()

		case <-trendTicker.C:
			recordTrendPoint()

		case <-kwTicker.C:
			if cfg.KernelWatchdog.Enabled {
				checkKernelEvents(bot)
			}

		case <-netTicker.C:
			if cfg.NetworkWatchdog.Enabled {
				checkNetworkHealth(bot)
			}

		case <-raidTicker.C:
			if cfg.RaidWatchdog.Enabled {
				checkRaidHealth(bot)
			}
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  KERNEL WATCHDOG — OOM, panic, I/O errors, read-only FS, hung tasks
//  ALWAYS notifies (ignores quiet hours) — these are critical events
// ═══════════════════════════════════════════════════════════════════

func checkKernelEvents(bot *tgbotapi.BotAPI) {
	lines := getKernelLogLines()
	if len(lines) == 0 {
		return
	}

	kwMutex.Lock()
	defer kwMutex.Unlock()

	if kwLastSignatures == nil {
		kwLastSignatures = make(map[string]string)
	}

	for _, evt := range kernelEventTypes {
		lastIdx := -1
		for i, line := range lines {
			low := strings.ToLower(line)
			for _, k := range evt.Keywords {
				if strings.Contains(low, k) {
					lastIdx = i
					break
				}
			}
		}

		if lastIdx == -1 {
			continue
		}

		lastLine := strings.TrimSpace(lines[lastIdx])
		if lastLine == "" {
			continue
		}

		// On first run, record the baseline without alerting
		if !kwInitialized {
			kwLastSignatures[evt.Name] = lastLine
			continue
		}

		// Skip if we already alerted for this exact line
		if prev, ok := kwLastSignatures[evt.Name]; ok && prev == lastLine {
			continue
		}
		kwLastSignatures[evt.Name] = lastLine

		// Build context (±3 lines around the event)
		start := lastIdx - 3
		if start < 0 {
			start = 0
		}
		end := lastIdx + 3
		if end >= len(lines) {
			end = len(lines) - 1
		}

		ctxText := strings.Join(lines[start:end+1], "\n")
		// Truncate if too long for Telegram
		if len(ctxText) > 3000 {
			ctxText = ctxText[:3000] + "\n..."
		}

		addReportEvent("critical", fmt.Sprintf("%s detected", evt.Name))
		slog.Warn("KernelWatchdog event detected", "event", evt.Name, "line", lastLine)

		// ALWAYS send — critical events ignore quiet hours
		msg := fmt.Sprintf(tr(evt.TrKey), ctxText)
		m := tgbotapi.NewMessage(AllowedUserID, msg)
		m.ParseMode = "Markdown"
		bot.Send(m)
	}

	if !kwInitialized {
		kwInitialized = true
	}
}

func getKernelLogLines() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try dmesg first (more reliable, no auth needed on most systems)
	cmd := exec.CommandContext(ctx, "dmesg", "--time-format", "reltime")
	out, err := cmd.Output()
	if err != nil {
		// Fallback: dmesg without format flag (older kernels)
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()
		cmd = exec.CommandContext(ctx2, "dmesg")
		out, err = cmd.Output()
	}
	if err != nil {
		// Fallback: journalctl kernel messages
		ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel3()
		cmd = exec.CommandContext(ctx3, "journalctl", "-k", "-n", "300", "--no-pager")
		out, _ = cmd.Output()
	}

	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil
	}

	return strings.Split(text, "\n")
}

// ═══════════════════════════════════════════════════════════════════
//  NETWORK WATCHDOG — gateway, internet reachability, DNS
// ═══════════════════════════════════════════════════════════════════

func checkNetworkHealth(bot *tgbotapi.BotAPI) {
	netMutex.Lock()
	defer netMutex.Unlock()

	// Defaults if config is missing
	targets := cfg.NetworkWatchdog.Targets
	if len(targets) == 0 {
		targets = []string{"1.1.1.1", "8.8.8.8"}
	}
	dnsHost := cfg.NetworkWatchdog.DNSHost
	if dnsHost == "" {
		dnsHost = "google.com"
	}
	threshold := cfg.NetworkWatchdog.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := time.Duration(cfg.NetworkWatchdog.CooldownMins) * time.Minute
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}

	pingOk := false
	var reasons []string

	if cfg.NetworkWatchdog.Gateway != "" {
		if pingHost(cfg.NetworkWatchdog.Gateway) {
			pingOk = true
		} else {
			reasons = append(reasons, fmt.Sprintf("Gateway %s unreachable", cfg.NetworkWatchdog.Gateway))
		}
	}

	for _, target := range targets {
		if pingHost(target) {
			pingOk = true
			break
		}
	}
	if !pingOk {
		reasons = append(reasons, "No ping targets reachable")
	}

	dnsOk := checkDNS(dnsHost)
	if !dnsOk {
		reasons = append(reasons, fmt.Sprintf("DNS lookup failed: %s", dnsHost))
	}

	// Healthy network
	if pingOk && dnsOk {
		netFailCount = 0
		if !netDownSince.IsZero() {
			if cfg.NetworkWatchdog.RecoveryNotify {
				msg := fmt.Sprintf(tr("net_recovered"), formatDuration(time.Since(netDownSince)))
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				if !isQuietHours() {
					bot.Send(m)
				}
			}
			netDownSince = time.Time{}
		}
		return
	}

	// DNS-only issue (ICMP ok but DNS fails)
	if pingOk && !dnsOk {
		if time.Since(netDNSAlertTime) >= cooldown {
			msg := fmt.Sprintf(tr("net_dns_fail"), dnsHost)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			netDNSAlertTime = time.Now()
			addReportEvent("warning", fmt.Sprintf("DNS lookup failed: %s", dnsHost))
		}
		return
	}

	// Potential network down
	if !pingOk && !dnsOk {
		netFailCount++
		if netFailCount < threshold {
			return
		}

		if netDownSince.IsZero() {
			netDownSince = time.Now()
		}

		if netDownAlertTime.IsZero() || time.Since(netDownAlertTime) >= cooldown {
			reasonText := strings.Join(reasons, " · ")
			msg := fmt.Sprintf(tr("net_down"), reasonText)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			netDownAlertTime = time.Now()
			addReportEvent("critical", "Network down")
		}
	}
}

func pingHost(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", host)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func checkDNS(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := net.DefaultResolver.LookupHost(ctx, host)
	return err == nil
}

// ═══════════════════════════════════════════════════════════════════
//  RAID WATCHDOG — mdadm (/proc/mdstat) + ZFS (zpool status)
// ═══════════════════════════════════════════════════════════════════

func checkRaidHealth(bot *tgbotapi.BotAPI) {
	raidMutex.Lock()
	defer raidMutex.Unlock()

	issues := getRaidIssues()
	if len(issues) == 0 {
		if !raidDownSince.IsZero() {
			if cfg.RaidWatchdog.RecoveryNotify {
				msg := fmt.Sprintf(tr("raid_recovered"), formatDuration(time.Since(raidDownSince)))
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				if !isQuietHours() {
					bot.Send(m)
				}
			}
			raidDownSince = time.Time{}
			raidLastSignature = ""
		}
		return
	}

	signature := strings.Join(issues, " | ")
	cooldown := time.Duration(cfg.RaidWatchdog.CooldownMins) * time.Minute
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}

	if raidDownSince.IsZero() {
		raidDownSince = time.Now()
	}

	if signature != raidLastSignature || time.Since(raidAlertTime) >= cooldown {
		msg := fmt.Sprintf(tr("raid_alert"), strings.Join(issues, "\n"))
		m := tgbotapi.NewMessage(AllowedUserID, msg)
		m.ParseMode = "Markdown"
		bot.Send(m)
		raidLastSignature = signature
		raidAlertTime = time.Now()
		addReportEvent("critical", "RAID issue detected")
	}
}

func getRaidIssues() []string {
	var issues []string

	// mdadm /proc/mdstat
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

	// ZFS: zpool status -x
	if _, err := exec.LookPath("zpool"); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "zpool", "status", "-x")
		out, err := cmd.Output()
		if err == nil {
			output := strings.TrimSpace(string(out))
			if output != "" && !strings.Contains(strings.ToLower(output), "all pools are healthy") {
				issues = append(issues, fmt.Sprintf("zpool: %s", output))
			}
		}
	}

	return issues
}

func checkDockerHealth(bot *tgbotapi.BotAPI) {
	containers := getContainerList()

	isHealthy := len(containers) > 0

	if isHealthy {
		if !dockerFailureStart.IsZero() {
			dockerFailureStart = time.Time{}
			slog.Info("Docker recovered/populated.")
		}
		return
	}

	if dockerFailureStart.IsZero() {
		dockerFailureStart = time.Now()
		slog.Warn("Docker: 0 containers or service down. Timer started.", "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)
		return
	}

	timeout := time.Duration(cfg.Docker.Watchdog.TimeoutMinutes) * time.Minute

	if time.Since(dockerFailureStart) > timeout {
		slog.Error("Docker down longer than timeout", "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)

		dockerFailureStart = time.Now()

		if !cfg.Docker.Watchdog.AutoRestartService {
			if !isQuietHours() {
				title := tr("wd_title")
				body := fmt.Sprintf(tr("wd_no_containers"), cfg.Docker.Watchdog.TimeoutMinutes)
				footer := tr("wd_disabled")
				bot.Send(tgbotapi.NewMessage(AllowedUserID, title+body+footer))
			}
			addReportEvent("warning", "Docker watchdog triggered (restart disabled)")
			return
		}

		if !isQuietHours() {
			title := tr("wd_title")
			body := fmt.Sprintf(tr("wd_no_containers"), cfg.Docker.Watchdog.TimeoutMinutes)
			footer := tr("wd_restarting")
			bot.Send(tgbotapi.NewMessage(AllowedUserID, title+body+footer))
		}

		addReportEvent("action", "Docker watchdog restart triggered")

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		var cmd *exec.Cmd
		if _, err := exec.LookPath("systemctl"); err == nil {
			cmd = exec.CommandContext(ctx, "systemctl", "restart", "docker")
		} else {
			cmd = exec.CommandContext(ctx, "service", "docker", "restart")
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			if !isQuietHours() {
				bot.Send(tgbotapi.NewMessage(AllowedUserID, fmt.Sprintf(tr("docker_restart_err"), err)))
			}
			slog.Error("Docker restart fail", "err", err, "output", string(out))
		} else {
			if !isQuietHours() {
				bot.Send(tgbotapi.NewMessage(AllowedUserID, tr("docker_restart_sent")))
			}
		}
	}
}

func checkWeeklyPrune(bot *tgbotapi.BotAPI) {
	if !dockerPruneEnabled {
		return
	}

	now := time.Now().In(location)

	targetDay := time.Sunday
	switch strings.ToLower(dockerPruneDay) {
	case "monday":
		targetDay = time.Monday
	case "tuesday":
		targetDay = time.Tuesday
	case "wednesday":
		targetDay = time.Wednesday
	case "thursday":
		targetDay = time.Thursday
	case "friday":
		targetDay = time.Friday
	case "saturday":
		targetDay = time.Saturday
	case "sunday":
		targetDay = time.Sunday
	}

	isTime := now.Weekday() == targetDay && now.Hour() == dockerPruneHour

	if isTime {
		if !pruneDoneToday {
			slog.Info("Docker: Running Weekly Prune...")
			pruneDoneToday = true

			go func() {
				cmd := exec.Command("docker", "system", "prune", "-a", "-f")
				out, err := cmd.CombinedOutput()

				var msg string
				if err != nil {
					msg = fmt.Sprintf("🧹 *Weekly Prune Error*\n\n`%v`", err)
				} else {
					output := string(out)
					lines := strings.Split(output, "\n")
					lastLine := ""
					for i := len(lines) - 1; i >= 0; i-- {
						if strings.TrimSpace(lines[i]) != "" {
							lastLine = lines[i]
							break
						}
					}
					msg = fmt.Sprintf("🧹 *Weekly Prune*\n\nUnused images removed.\n`%s`", lastLine)
					addReportEvent("info", "Weekly docker prune completed")
				}

				if !isQuietHours() {
					m := tgbotapi.NewMessage(AllowedUserID, msg)
					m.ParseMode = "Markdown"
					bot.Send(m)
				}
			}()
		}
	} else {
		if now.Hour() != cfg.Docker.WeeklyPrune.Hour {
			pruneDoneToday = false
		}
	}
}

// checkResourceStress tracks stress for a resource and notifies if necessary
func checkResourceStress(bot *tgbotapi.BotAPI, resource string, currentValue, threshold float64) {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	tracker := resourceStress[resource]
	if tracker == nil {
		tracker = &StressTracker{}
		resourceStress[resource] = tracker
	}

	isStressed := currentValue >= threshold
	stressDurationThreshold := time.Duration(cfg.StressTracking.DurationThresholdMinutes) * time.Minute

	if isStressed {
		if tracker.CurrentStart.IsZero() {
			tracker.CurrentStart = time.Now()
			tracker.StressCount++
			tracker.Notified = false
		}

		stressDuration := time.Since(tracker.CurrentStart)
		if stressDuration >= stressDurationThreshold && !tracker.Notified && !isQuietHours() {
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

			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)

			tracker.Notified = true
			addReportEvent("warning", fmt.Sprintf("%s high (%.0f%%) for %s", resource, currentValue, stressDuration.Round(time.Second)))
		}
	} else {
		if !tracker.CurrentStart.IsZero() {
			stressDuration := time.Since(tracker.CurrentStart)
			tracker.TotalStress += stressDuration

			if stressDuration > tracker.LongestStress {
				tracker.LongestStress = stressDuration
			}

			if tracker.Notified && !isQuietHours() {
				msg := fmt.Sprintf("✅ *%s back to normal* after `%s`", resource, stressDuration.Round(time.Second))
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
				addReportEvent("info", fmt.Sprintf("%s normalized after %s", resource, stressDuration.Round(time.Second)))
			}

			tracker.CurrentStart = time.Time{}
			tracker.Notified = false
		}
	}
}

// getStressSummary returns a summary of significant stress events
func getStressSummary() string {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	var parts []string

	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		tracker := resourceStress[res]
		if tracker == nil || tracker.StressCount == 0 {
			continue
		}

		if tracker.LongestStress < 5*time.Minute {
			continue
		}

		entry := fmt.Sprintf("%s %dx", res, tracker.StressCount)
		if tracker.LongestStress > 0 {
			entry += fmt.Sprintf(" `%s`", formatDuration(tracker.LongestStress))
		}
		parts = append(parts, entry)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// resetStressCounters resets stress counters for new report period
func resetStressCounters() {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	for _, tracker := range resourceStress {
		tracker.StressCount = 0
		tracker.LongestStress = 0
		tracker.TotalStress = 0
	}
}

// ═══════════════════════════════════════════════════════════════════
//  MONITOR ALERTS (only critical, no spam)
// ═══════════════════════════════════════════════════════════════════

var lastCriticalAlert time.Time

func monitorAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(IntervalMonitor)
	defer ticker.Stop()

	for range ticker.C {
		statsMutex.RLock()
		s := statsCache
		ready := statsReady
		statsMutex.RUnlock()

		if !ready {
			continue
		}

		var criticalAlerts []string

		// Disk almost full
		if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("💿 SSD critical: `%.1f%%`", s.VolSSD.Used))
		}
		if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🗄 HDD critical: `%.1f%%`", s.VolHDD.Used))
		}

		// Check SMART
		if cfg.Notifications.SMART.Enabled {
			for _, dev := range []string{"sda", "sdb"} {
				_, health := readDiskSMART(dev)
				if strings.Contains(strings.ToUpper(health), "FAIL") {
					criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 Disk %s FAILING — backup now!", dev))
				}
			}
		}

		// Critical CPU/RAM
		if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🧠 CPU critical: `%.1f%%`", s.CPU))
		}
		if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("💾 RAM critical: `%.1f%%`", s.RAM))
		}

		// Send critical alerts with configurable cooldown
		cooldown := time.Duration(cfg.Intervals.CriticalAlertCooldownMins) * time.Minute
		if len(criticalAlerts) > 0 && time.Since(lastCriticalAlert) >= cooldown && !isQuietHours() {
			msg := "🚨 *Critical*\n\n" + strings.Join(criticalAlerts, "\n")
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			lastCriticalAlert = time.Now()
		}

		// Always record critical events for report
		if len(criticalAlerts) > 0 {
			for _, alert := range criticalAlerts {
				addReportEvent("critical", alert)
			}
		}

		// Record warnings for the report
		if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.WarningThreshold && s.CPU < cfg.Notifications.CPU.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("CPU high: %.1f%%", s.CPU))
		}
		if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.WarningThreshold && s.RAM < cfg.Notifications.RAM.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("RAM high: %.1f%%", s.RAM))
		}
		if cfg.Notifications.Swap.Enabled && s.Swap >= cfg.Notifications.Swap.WarningThreshold {
			addReportEvent("warning", fmt.Sprintf("Swap high: %.1f%%", s.Swap))
		}
		if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.WarningThreshold && s.VolSSD.Used < cfg.Notifications.DiskSSD.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("SSD at %.1f%%", s.VolSSD.Used))
		}
		if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.WarningThreshold && s.VolHDD.Used < cfg.Notifications.DiskHDD.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("HDD at %.1f%%", s.VolHDD.Used))
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  BACKGROUND STATS COLLECTOR
// ═══════════════════════════════════════════════════════════════════

func statsCollector() {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	ticker := time.NewTicker(IntervalStats)
	defer ticker.Stop()

	for {
		c, _ := cpu.Percent(0, false)
		v, _ := mem.VirtualMemory()
		sw, _ := mem.SwapMemory()
		l, _ := load.Avg()
		h, _ := host.Info()
		dSSD, _ := disk.Usage(PathSSD)
		dHDD, _ := disk.Usage(PathHDD)

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

		newStats := Stats{
			CPU:        safeFloat(c, 0),
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

		statsMutex.Lock()
		statsCache = newStats
		statsReady = true
		statsMutex.Unlock()

		<-ticker.C
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
//  TEMPERATURE MONITORING
// ═══════════════════════════════════════════════════════════════════

func checkTemperatureAlert(bot *tgbotapi.BotAPI) {
	temp := readCPUTemp()
	if temp <= 0 {
		return
	}

	// Only alert once every 30 minutes
	if time.Since(lastTempAlert) < 30*time.Minute {
		return
	}

	if temp >= cfg.Temperature.CriticalThreshold {
		if !isQuietHours() {
			msg := fmt.Sprintf("🔥 *CPU Temperature Critical!*\n\n"+
				"Current: `%.1f°C`\n"+
				"Threshold: `%.0f°C`\n\n"+
				"_Consider checking cooling or reducing load_",
				temp, cfg.Temperature.CriticalThreshold)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		lastTempAlert = time.Now()
		addReportEvent("critical", fmt.Sprintf("CPU temp critical: %.1f°C", temp))
	} else if temp >= cfg.Temperature.WarningThreshold {
		if !isQuietHours() {
			msg := fmt.Sprintf("🌡 *CPU Temperature Warning*\n\n"+
				"Current: `%.1f°C`\n"+
				"Threshold: `%.0f°C`",
				temp, cfg.Temperature.WarningThreshold)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}
		lastTempAlert = time.Now()
		addReportEvent("warning", fmt.Sprintf("CPU temp high: %.1f°C", temp))
	}
}

// ═══════════════════════════════════════════════════════════════════
//  TREND TRACKING (for mini-graphs)
// ═══════════════════════════════════════════════════════════════════

func recordTrendPoint() {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return
	}

	now := time.Now()

	trendMutex.Lock()
	defer trendMutex.Unlock()

	// Add new points
	cpuTrend = append(cpuTrend, TrendPoint{Time: now, Value: s.CPU})
	ramTrend = append(ramTrend, TrendPoint{Time: now, Value: s.RAM})

	// Keep only last maxTrendPoints (6 hours at 5 min intervals = 72 points)
	if len(cpuTrend) > maxTrendPoints {
		cpuTrend = cpuTrend[len(cpuTrend)-maxTrendPoints:]
	}
	if len(ramTrend) > maxTrendPoints {
		ramTrend = ramTrend[len(ramTrend)-maxTrendPoints:]
	}
}

// getMiniGraph generates a tiny ASCII spark line (12 chars max)
func getMiniGraph(points []TrendPoint, maxPoints int) string {
	if len(points) == 0 {
		return ""
	}

	// Use last N points for the graph
	if len(points) > maxPoints {
		points = points[len(points)-maxPoints:]
	}

	chars := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	var result strings.Builder

	for _, p := range points {
		// Map 0-100% to character index 0-7
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

// getTrendSummary returns trend graphs for status display
func getTrendSummary() (cpuGraph, ramGraph string) {
	trendMutex.Lock()
	defer trendMutex.Unlock()

	// Get last 12 points (1 hour at 5 min intervals)
	cpuGraph = getMiniGraph(cpuTrend, 12)
	ramGraph = getMiniGraph(ramTrend, 12)
	return
}

// ═══════════════════════════════════════════════════════════════════
//  CRITICAL CONTAINERS MONITORING
// ═══════════════════════════════════════════════════════════════════

var lastCriticalContainerAlert = make(map[string]time.Time)
var criticalContainerMutex sync.Mutex

func checkCriticalContainers(bot *tgbotapi.BotAPI) {
	if len(cfg.CriticalContainers) == 0 {
		return
	}

	containers := getCachedContainerList()
	containerMap := make(map[string]bool)
	for _, c := range containers {
		containerMap[c.Name] = c.Running
	}

	criticalContainerMutex.Lock()
	defer criticalContainerMutex.Unlock()

	for _, name := range cfg.CriticalContainers {
		running, exists := containerMap[name]

		if !exists || !running {
			// Only alert once every 10 minutes per container
			if lastAlert, ok := lastCriticalContainerAlert[name]; ok {
				if time.Since(lastAlert) < 10*time.Minute {
					continue
				}
			}

			if !isQuietHours() {
				status := tr("status_not_running")
				if !exists {
					status = tr("status_not_found")
				}
				msg := fmt.Sprintf(tr("crit_cont_alert"), name, status)
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
			}

			lastCriticalContainerAlert[name] = time.Now()
			addReportEvent("critical", fmt.Sprintf("Critical container %s down", name))
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  DOCKER CACHE (reduce overhead)
// ═══════════════════════════════════════════════════════════════════

func getCachedContainerList() []ContainerInfo {
	dockerCacheMutex.RLock()
	ttl := time.Duration(cfg.Cache.DockerTTLSeconds) * time.Second
	if time.Since(dockerCache.LastUpdate) < ttl && len(dockerCache.Containers) > 0 {
		result := dockerCache.Containers
		dockerCacheMutex.RUnlock()
		return result
	}
	dockerCacheMutex.RUnlock()

	// Need to refresh
	containers := getContainerList()

	dockerCacheMutex.Lock()
	dockerCache.Containers = containers
	dockerCache.LastUpdate = time.Now()
	dockerCacheMutex.Unlock()

	return containers
}
