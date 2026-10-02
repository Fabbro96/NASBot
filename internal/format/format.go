package format

import (
	"fmt"
	"math"
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
//
// Contract:
//   - 0                 -> "0G" (kept: callers and tests rely on a G-suffixed zero,
//     and a zero-byte volume is easier to read as "0G" than as "0B")
//   - >= 1000 GiB       -> "%.0fT"
//   - >= 1 GiB          -> "%.0fG"
//   - >= 1 MiB          -> "%.0fM"
//   - >= 1 KiB          -> "%.0fK"
//   - < 1 KiB           -> "0K"
func FormatBytes(bytes uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
		tib = 1 << 40
	)

	if bytes == 0 {
		return "0G"
	}
	switch {
	case bytes >= 1000*gib:
		return fmt.Sprintf("%.0fT", float64(bytes)/float64(tib))
	case bytes >= gib:
		return fmt.Sprintf("%.0fG", float64(bytes)/float64(gib))
	case bytes >= mib:
		return fmt.Sprintf("%.0fM", float64(bytes)/float64(mib))
	case bytes >= kib:
		return fmt.Sprintf("%.0fK", float64(bytes)/float64(kib))
	}
	return "0K"
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

// PeriodTranslator resolves a translation key for the active language.
// It has the same shape as model.Translate / (*AppContext).Tr, so callers can
// pass those directly.
type PeriodTranslator func(key string) string

// periodUnitKeys maps a unit to its singular/plural translation keys.
//
// The keys are the ones the dictionary actually defines in all six languages:
// the singular templates ("fmt_period_minute", "fmt_period_hour",
// "fmt_period_day") are pure strings with no verb, the plural ones carry a %d.
// The seconds unit has no singular in the dictionary ("fmt_period_seconds" is
// the plural), so "fmt_period_second" resolves through the English fallback
// below until the i18n lane adds it.
type periodUnitKeys struct {
	one   string
	other string
}

var (
	periodSeconds = periodUnitKeys{"fmt_period_second", "fmt_period_seconds"}
	periodMinutes = periodUnitKeys{"fmt_period_minute", "fmt_period_minutes"}
	periodHours   = periodUnitKeys{"fmt_period_hour", "fmt_period_hours"}
	periodDays    = periodUnitKeys{"fmt_period_day", "fmt_period_days"}
)

// englishPeriodFallbacks keeps FormatPeriodL usable and stable even when the
// resolver is nil or answers with the key itself (that is what model.Translate
// does for a missing key): without it a period would render as an empty string
// instead of as English.
var englishPeriodFallbacks = map[string]string{
	periodSeconds.one:   "%d second",
	periodSeconds.other: "%d seconds",
	periodMinutes.one:   "1 minute",
	periodMinutes.other: "%d minutes",
	periodHours.one:     "1 hour",
	periodHours.other:   "%d hours",
	periodDays.one:      "1 day",
	periodDays.other:    "%d days",
}

func resolvePeriodKey(tr PeriodTranslator, key string) string {
	if tr != nil {
		if s := tr(key); s != "" && s != key {
			return s
		}
	}
	return englishPeriodFallbacks[key]
}

// periodUnit renders count with the singular or plural template of unit.
//
// The count is only passed to fmt when the resolved template actually carries a
// verb. The singular keys are plain strings in every language ("1 minute"), and
// handing them an argument appends a "%!(EXTRA int=1)" diagnostic to the user's
// message.
func periodUnit(tr PeriodTranslator, unit periodUnitKeys, count int) string {
	key := unit.other
	if count == 1 {
		key = unit.one
	}
	template := resolvePeriodKey(tr, key)
	if !strings.ContainsRune(template, '%') {
		return template
	}
	return fmt.Sprintf(template, count)
}

// FormatPeriodL formats seconds into a human readable period using localized
// unit names. Pass a resolver (e.g. ctx.Tr) so the text follows the UI language;
// pass nil to get the English fallback.
//
// Thresholds: < 60s seconds, < 1h minutes, < 24h hours, otherwise days.
func FormatPeriodL(seconds int, tr PeriodTranslator) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return periodUnit(tr, periodSeconds, seconds)
	case seconds < 3600:
		return periodUnit(tr, periodMinutes, seconds/60)
	case seconds < 86400:
		return periodUnit(tr, periodHours, seconds/3600)
	default:
		return periodUnit(tr, periodDays, seconds/86400)
	}
}

// FormatPeriod formats seconds into a human readable period using the English
// fallback text only.
//
// It has no production caller left: the two healthchecks messages that used it
// now pass ctx.Tr to FormatPeriodL, because an English "1 minute" inside an
// Italian alert is exactly the bug. It is kept for two reasons — format_test.go
// pins its behaviour, and it is the honest spelling of "no translator
// available" for a caller outside a running bot (a CLI, a diagnostic dump).
func FormatPeriod(seconds int) string {
	return FormatPeriodL(seconds, nil)
}

// Truncate truncates a string to max length in runes safely.
func Truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "~"
	}
	return string(r[:max-1]) + "~"
}

// BoolToEmoji converts a bool to an emoji.
func BoolToEmoji(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

// MakeProgressBar creates a 10-step visual progress bar.
//
// Non-finite input is treated as 0%: gopsutil can report NaN for a CPU total of
// 0 and +Inf/-Inf for bogus /proc samples. int(NaN) is undefined behaviour in Go
// and returns INT64_MIN on amd64, which used to make strings.Repeat panic on a
// negative count and take the whole process down.
func MakeProgressBar(percent float64) string {
	if math.IsNaN(percent) {
		percent = 0
	}
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
	if filled < 0 {
		filled = 0
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
