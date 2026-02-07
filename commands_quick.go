package main

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
