package format

import (
	"fmt"
	"strings"
	"time"
	"unicode"
)

// FormatUptime formats uptime in a readable format.
func FormatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	mins := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

// FormatBytes formats bytes in a readable format.
func FormatBytes(bytes uint64) string {
	gb := float64(bytes) / 1024 / 1024 / 1024
	if gb >= 1000 {
		return fmt.Sprintf("%.0fT", gb/1024)
	}
	return fmt.Sprintf("%.0fG", gb)
}

// FormatRAM formats RAM in a readable format.
func FormatRAM(mb uint64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%dM", mb)
}

// FormatDuration formats a duration readably.
func FormatDuration(d time.Duration) string {
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

// FormatPeriod formats seconds into a human readable period.
func FormatPeriod(seconds int) string {
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

// Truncate truncates a string to max length.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "~"
}

// SafeFloat safely gets a float from an array.
func SafeFloat(arr []float64, def float64) float64 {
	if len(arr) > 0 {
		return arr[0]
	}
	return def
}

// BoolToEmoji converts a bool to an emoji.
func BoolToEmoji(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

// MakeProgressBar creates a 10-step visual progress bar.
func MakeProgressBar(percent float64) string {
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

// TitleCaseWord capitalizes the first letter of a word (ASCII-safe, rune-aware).
func TitleCaseWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	lower := strings.ToLower(s)
	r := []rune(lower)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
