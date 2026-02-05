package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// formatUptime formats uptime in a readable format
func formatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	mins := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// formatBytes formats bytes in a readable format
func formatBytes(bytes uint64) string {
	gb := float64(bytes) / 1024 / 1024 / 1024
	if gb >= 1000 {
		return fmt.Sprintf("%.0fT", gb/1024)
	}
	return fmt.Sprintf("%.0fG", gb)
}

// formatRAM formats RAM in a readable format
func formatRAM(mb uint64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%dM", mb)
}

// formatDuration formats a duration readably
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// formatPeriod formats seconds into a human readable period
func formatPeriod(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%d seconds", seconds)
	}
	if seconds < 3600 {
		mins := seconds / 60
		if mins == 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	hours := seconds / 3600
	if hours == 1 {
		return "1 hour"
	}
	return fmt.Sprintf("%d hours", hours)
}

// truncate truncates a string to max length
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "~"
}

// safeFloat safely gets a float from an array
func safeFloat(arr []float64, def float64) float64 {
	if len(arr) > 0 {
		return arr[0]
	}
	return def
}

// boolToEmoji converts a bool to an emoji
func boolToEmoji(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

// makeProgressBar creates a 10-step visual progress bar
func makeProgressBar(percent float64) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	filled := int((percent + 5) / 10)
	if filled > 10 {
		filled = 10
	}

	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

// readCPUTemp reads CPU temperature from thermal zone
func readCPUTemp() float64 {
	raw, err := exec.Command("cat", "/sys/class/thermal/thermal_zone0/temp").Output()
	if err != nil {
		return 0
	}
	val, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return float64(val) / 1000.0
}

// readDiskSMART reads disk SMART data
func readDiskSMART(device string) (temp int, health string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "smartctl", "-A", "/dev/"+device)
	out, _ := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Temperature_Celsius") || strings.Contains(line, "Temperature_Internal") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				temp, _ = strconv.Atoi(fields[9])
			}
		}
	}

	cmd = exec.CommandContext(ctx, "smartctl", "-H", "/dev/"+device)
	out, _ = cmd.Output()
	health = "OK"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "PASSED") {
			health = "PASSED"
		} else if strings.Contains(line, "FAILED") {
			health = "FAILED!"
		}
	}

	return temp, health
}

// parseUptime parses Docker container uptime
func parseUptime(status string) string {
	if !strings.Contains(status, "Up") {
		return "stopped"
	}
	parts := strings.Fields(status)
	if len(parts) >= 2 {
		result := parts[1]
		if len(parts) >= 3 {
			result += " " + parts[2]
		}
		return result
	}
	return "running"
}
