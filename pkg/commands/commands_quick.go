package commands

import (
	"fmt"
	"strings"
)

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
		if temp > cpuHotC {
			tempIcon = "🔥"
		}
		tempStr = fmt.Sprintf(" %s%.0f°", tempIcon, temp)
	}

	// Build compact line with optional trends
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s ", quickHealthEmoji(ctx, s)))

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

	// Disks (sorted, so two messages list them in the same order)
	b.WriteString(fmt.Sprintf(" · SSD %.0f%%", s.VolSSD.Used))
	for _, m := range volumeMounts(s.SecondaryVols) {
		shortName := runeSlice(diskDisplayName(m), quickMountNameMax)
		b.WriteString(fmt.Sprintf(" · %s %.0f%%", shortName, s.SecondaryVols[m].Used))
	}

	// Docker
	b.WriteString(fmt.Sprintf(" · 🐳%d", running))

	// Watchdog semaphores
	ctx.Monitor.Mu.Lock()
	netDegraded := ctx.Monitor.NetConsecutiveDegraded > 0 || ctx.Monitor.NetFailCount > 0
	kwErrors := ctx.Monitor.KwConsecutiveCheckErrors > 0
	ctx.Monitor.Mu.Unlock()

	netSem := "🟢"
	if netDegraded {
		netSem = "🟡"
	}
	kwSem := "🟢"
	if kwErrors {
		kwSem = "🔴"
	}
	b.WriteString(fmt.Sprintf(" · WD K%s N%s", kwSem, netSem))

	// Temp
	b.WriteString(tempStr)

	return b.String()
}

// quickHealthEmoji summarises the worst resource usage. Each resource is
// compared against its own configured thresholds (the same numbers the alerts
// use), falling back to 90/95 when the config leaves them unset.
func quickHealthEmoji(ctx *AppContext, s Stats) string {
	c := cfg(ctx)
	if c == nil {
		return "✅"
	}

	const (
		ok   = 0
		warn = 1
		crit = 2
	)
	level := ok
	check := func(used, warnT, critT float64) {
		switch {
		case used > threshold(critT, defaultHealthCritPc):
			if level < crit {
				level = crit
			}
		case used > threshold(warnT, defaultHealthWarnPc):
			if level < warn {
				level = warn
			}
		}
	}

	check(s.CPU, c.Notifications.CPU.WarningThreshold, c.Notifications.CPU.CriticalThreshold)
	check(s.RAM, c.Notifications.RAM.WarningThreshold, c.Notifications.RAM.CriticalThreshold)
	check(s.VolSSD.Used, c.Notifications.DiskSSD.WarningThreshold, c.Notifications.DiskSSD.CriticalThreshold)
	for _, m := range volumeMounts(s.SecondaryVols) {
		rc := c.Notifications.SecondaryDisks[m]
		check(s.SecondaryVols[m].Used, rc.WarningThreshold, rc.CriticalThreshold)
	}

	switch level {
	case crit:
		return "🚨"
	case warn:
		return "⚠️"
	default:
		return "✅"
	}
}

// threshold returns v when it is configured, def otherwise.
func threshold(v, def float64) float64 {
	if v > 0 {
		return v
	}
	return def
}

func GetQuickText(ctx *AppContext) string { return getQuickText(ctx) }
