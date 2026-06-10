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
	if s.CPU > 95 || s.RAM > 95 || s.VolSSD.Used > 95 {
		healthEmoji = "🚨"
	} else {
		for _, v := range s.SecondaryVols {
			if v.Used > 95 {
				healthEmoji = "🚨"
				break
			}
		}
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
	b.WriteString(fmt.Sprintf(" · SSD %.0f%%", s.VolSSD.Used))
	for m, v := range s.SecondaryVols {
		shortName := mountShortName(m)
		// Truncate for ultra-compact one-liner display
		if len(shortName) > 5 {
			shortName = shortName[:5]
		}
		b.WriteString(fmt.Sprintf(" · %s %.0f%%", shortName, v.Used))
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

func GetQuickText(ctx *AppContext) string { return getQuickText(ctx) }
