package format

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The functions in this file decide what every status line in the bot reads like.
// They are pure, tiny, and their output is a user-facing contract ("0G", "1d10h",
// "10m30s", a ten-cell bar), so the tests below are table-driven over the
// boundaries rather than over a couple of happy values: an off-by-one at 86400s or
// at the 1000GiB rollover prints a wrong number that nobody notices until it is
// wrong in production.

// TestFormatUptimeContract covers both branches and the day rollover.
func TestFormatUptimeContract(t *testing.T) {
	const (
		minute = uint64(60)
		hour   = 60 * minute
		day    = 24 * hour
	)
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0h0m"},
		{59, "0h0m"},
		{minute - 1, "0h0m"},
		{minute, "0h1m"},
		{hour - 1, "0h59m"},
		{hour, "1h0m"},
		{hour + 30*minute, "1h30m"},
		{day - 1, "23h59m"},
		{day, "1d0h"}, // the rollover: days>0 switches to the day form
		{day + hour, "1d1h"},
		{2*day + 5*hour + 59*minute, "2d5h"},
		{999*day + 23*hour, "999d23h"},
	}
	for _, tc := range cases {
		if got := FormatUptime(tc.in); got != tc.want {
			t.Errorf("FormatUptime(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatBytesContract pins the unit ladder, including the two values the
// source comments call out: 0 is "0G" and not "0B", and the T threshold is 1000
// GiB rather than 1024.
func TestFormatBytesContract(t *testing.T) {
	const (
		kib = uint64(1) << 10
		mib = uint64(1) << 20
		gib = uint64(1) << 30
		tib = uint64(1) << 40
	)
	cases := []struct {
		name string
		in   uint64
		want string
	}{
		{"zero keeps the G suffix", 0, "0G"},
		{"one byte", 1, "0K"},
		{"just under a KiB", kib - 1, "0K"},
		{"exactly one KiB", kib, "1K"},
		{"999 KiB", 999 * kib, "999K"},
		{"just under a MiB rounds up to 1024K", mib - 1, "1024K"},
		{"exactly one MiB", mib, "1M"},
		{"1023 MiB", 1023 * mib, "1023M"},
		{"just under a GiB rounds up to 1024M", gib - 1, "1024M"},
		{"exactly one GiB", gib, "1G"},
		{"the largest GiB tier rounds up to 1000G", 1000*gib - 1, "1000G"},
		// The T threshold is decimal, not binary: this is the documented contract.
		{"1000 GiB is one TiB", 1000 * gib, "1T"},
		{"1001 GiB", 1001 * gib, "1T"},
		{"1024 GiB", 1024 * gib, "1T"},
		{"2048 GiB", 2048 * gib, "2T"},
		{"one TiB", tib, "1T"},
		{"1023 TiB", 1023 * tib, "1023T"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatBytes(tc.in); got != tc.want {
				t.Errorf("FormatBytes(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFormatBytesIsMonotonicInSize guards the ladder as a whole: a caller reads
// "50G free" next to "100G total", so the printed number must never go down as the
// volume grows, and the ladder must actually reach TiB.
func TestFormatBytesIsMonotonicInSize(t *testing.T) {
	// 0 is deliberately absent: it is documented to print "0G", not "0K".
	progression := []uint64{
		1 << 10, // 1K
		1 << 20, // 1M
		1 << 30, // 1G
		1 << 40, // 1T
	}
	units := []string{"K", "M", "G", "T"}

	prev := -1.0
	for i, bytes := range progression {
		got := FormatBytes(bytes)
		if len(got) < 2 {
			t.Fatalf("FormatBytes(%d) = %q, too short to carry a unit", bytes, got)
		}
		if unit := got[len(got)-1:]; unit != units[i] {
			t.Fatalf("FormatBytes(%d) = %q, want unit %q: the ladder skips a tier", bytes, got, units[i])
		}
		value, err := strconv.ParseFloat(got[:len(got)-1], 64)
		if err != nil {
			t.Fatalf("FormatBytes(%d) = %q, unparsable: %v", bytes, got, err)
		}
		if value < prev {
			t.Errorf("FormatBytes went backwards at %d: %q after %v", bytes, got, prev)
		}
		prev = value
	}
}

// TestFormatRAMContract: the switch to GiB is at 1024 MiB, not at 1000.
func TestFormatRAMContract(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0M"},
		{1, "1M"},
		{512, "512M"},
		{1023, "1023M"},
		{1024, "1.0G"},
		{1025, "1.0G"},
		{1536, "1.5G"},
		{2048, "2.0G"},
		{10240, "10.0G"},
		{10752, "10.5G"}, // 10.5 GiB is not an integer number of MiB
	}
	for _, tc := range cases {
		if got := FormatRAM(tc.in); got != tc.want {
			t.Errorf("FormatRAM(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatDurationContract covers the three tiers and the sub-minute formatting
// rules (a minute boundary drops the seconds, an hour boundary keeps the minutes).
func TestFormatDurationContract(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"one second", time.Second, "1s"},
		{"under a minute", 30 * time.Second, "30s"},
		{"59 seconds", 59 * time.Second, "59s"},
		// 59.6s rounds up to 60s, which is no longer under a minute.
		{"rounds up into the minute tier", 59600 * time.Millisecond, "1m"},
		{"exactly a minute", time.Minute, "1m"},
		{"a minute and a half", 90 * time.Second, "1m30s"},
		{"two minutes", 2 * time.Minute, "2m"},
		{"one minute fifty-nine", 119 * time.Second, "1m59s"},
		{"just under an hour", 59*time.Minute + 59*time.Second, "59m59s"},
		{"exactly an hour", time.Hour, "1h0m"},
		{"an hour and a minute", time.Hour + time.Minute, "1h1m"},
		{"two hours", 2 * time.Hour, "2h0m"},
		{"days are not a tier", 49 * time.Hour, "49h0m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatDuration(tc.in); got != tc.want {
				t.Errorf("FormatDuration(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestFormatDurationIsRoundedToSeconds: a raw nanosecond count would print
// "1m0.000000123s"; every consumer assumes whole seconds.
func TestFormatDurationIsRoundedToSeconds(t *testing.T) {
	for _, in := range []time.Duration{
		time.Second + 400*time.Millisecond,
		2*time.Second + 600*time.Millisecond,
	} {
		got := FormatDuration(in)
		if strings.ContainsAny(got, ".") {
			t.Errorf("FormatDuration(%v) = %q carries a fraction", in, got)
		}
	}
}

// TestBoolToEmojiContract: this is the only place a healthy/unhealthy state turns
// into a glyph, and it is read at a glance in Telegram.
func TestBoolToEmojiContract(t *testing.T) {
	if got := BoolToEmoji(true); got != "✅" {
		t.Errorf("BoolToEmoji(true) = %q, want ✅", got)
	}
	if got := BoolToEmoji(false); got != "❌" {
		t.Errorf("BoolToEmoji(false) = %q, want ❌", got)
	}
}

// TestMakeProgressBarContract: ten cells, rounded to the nearest cell, and
// negative input clipped instead of repeating a negative number of runes.
func TestMakeProgressBarContract(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "░░░░░░░░░░"},
		{4.9, "░░░░░░░░░░"},
		{5, "█░░░░░░░░░"}, // rounds to one cell
		{49, "█████░░░░░"},
		{50, "█████░░░░░"},
		{55, "██████░░░░"},
		{94, "█████████░"},
		{95, "██████████"}, // rounds to a full bar
		{100, "██████████"},
	}
	for _, tc := range cases {
		if got := MakeProgressBar(tc.in); got != tc.want {
			t.Errorf("MakeProgressBar(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMakeProgressBarClipsOutOfRangeInput is the regression guard for the crash:
// gopsutil can report NaN for a CPU total of 0 and ±Inf for a bogus /proc sample,
// and int(NaN) is undefined behaviour in Go (INT64_MIN on amd64), which used to
// make strings.Repeat panic on a negative count and take the process down.
//
// Every value here must produce exactly ten cells. The length is the assertion:
// a panic fails the test outright, and a short bar fails the count.
func TestMakeProgressBarClipsOutOfRangeInput(t *testing.T) {
	inputs := map[string]float64{
		"negative":         -42.5,
		"large negative":   -1e9,
		"over one hundred": 100.5,
		"way over":         1e9,
		"positive zero":    0,
		"NaN":              math.NaN(),
		"positive Inf":     math.Inf(1),
		"negative Inf":     math.Inf(-1),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			got := MakeProgressBar(in)
			if n := len([]rune(got)); n != 10 {
				t.Fatalf("MakeProgressBar(%v) = %q has %d cells, want 10", in, got, n)
			}
			if strings.Trim(got, "█░") != "" {
				t.Errorf("MakeProgressBar(%v) = %q contains a cell outside █ and ░", in, got)
			}
		})
	}
}

// TestMakeProgressBarNaNIsEmpty: NaN means "not measured", so the honest reading
// is an empty bar, not a full one.
func TestMakeProgressBarNaNIsEmpty(t *testing.T) {
	if got := MakeProgressBar(math.NaN()); got != strings.Repeat("░", 10) {
		t.Errorf("MakeProgressBar(NaN) = %q, want an empty bar", got)
	}
	if got := MakeProgressBar(math.Inf(1)); got != strings.Repeat("█", 10) {
		t.Errorf("MakeProgressBar(+Inf) = %q, want a full bar", got)
	}
}

// fixedTranslator resolves a fixed table and echoes the key when it is absent,
// which is what model.Translate does for a missing key.
type fixedTranslator map[string]string

func (f fixedTranslator) tr(key string) string {
	if v, ok := f[key]; ok {
		return v
	}
	return key
}

// TestFormatPeriodLPicksTheUnitTier: the four thresholds, at and around the
// boundary. An off-by-one puts "59 minutes" where the user expects "1 hour".
func TestFormatPeriodLPicksTheUnitTier(t *testing.T) {
	tr := fixedTranslator{
		"fmt_period_second":  "1 sec",
		"fmt_period_minute":  "1 min",
		"fmt_period_hour":    "1 h",
		"fmt_period_day":     "1 d",
		"fmt_period_seconds": "%d sec",
		"fmt_period_minutes": "%d min",
		"fmt_period_hours":   "%d h",
		"fmt_period_days":    "%d d",
	}
	cases := []struct {
		in   int
		want string
	}{
		{0, "0 sec"},
		{1, "1 sec"},
		{59, "59 sec"},
		{60, "1 min"},
		{119, "1 min"},
		{120, "2 min"},
		{3599, "59 min"},
		{3600, "1 h"},
		{7199, "1 h"},
		{7200, "2 h"},
		{86399, "23 h"},
		{86400, "1 d"},
		{86400 * 3, "3 d"},
	}
	for _, tc := range cases {
		if got := FormatPeriodL(tc.in, tr.tr); got != tc.want {
			t.Errorf("FormatPeriodL(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormatPeriodLClampsNegativeSeconds: a clock that jumped backwards must not
// produce "-3 minutes" in an alert message.
func TestFormatPeriodLClampsNegativeSeconds(t *testing.T) {
	for _, in := range []int{-1, -3600, -1 << 30} {
		if got := FormatPeriodL(in, nil); got != "0 seconds" {
			t.Errorf("FormatPeriodL(%d) = %q, want %q", in, got, "0 seconds")
		}
	}
}

// TestFormatPeriodLUsesSingularForOne: "1 minutes" in a Telegram alert is the kind
// of small wrongness that makes a bot look unfinished.
func TestFormatPeriodLUsesSingularForOne(t *testing.T) {
	tr := fixedTranslator{
		"fmt_period_second":  "1 second",
		"fmt_period_minute":  "1 minute",
		"fmt_period_hour":    "1 hour",
		"fmt_period_day":     "1 day",
		"fmt_period_minutes": "%d minutes",
		"fmt_period_hours":   "%d hours",
		"fmt_period_days":    "%d days",
		"fmt_period_seconds": "%d seconds",
	}
	cases := []struct {
		in   int
		want string
	}{
		{1, "1 second"},
		{59, "59 seconds"},
		{60, "1 minute"},
		{120, "2 minutes"},
		{3600, "1 hour"},
		{7200, "2 hours"},
		{86400, "1 day"},
		{86400 * 2, "2 days"},
	}
	for _, tc := range cases {
		if got := FormatPeriodL(tc.in, tr.tr); got != tc.want {
			t.Errorf("FormatPeriodL(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestPeriodUnitDoesNotPassAnArgumentToAVerblessTemplate is the bug the helper
// exists for: handing an argument to a string with no verb appends
// "%!(EXTRA int=1)" to the user's message.
func TestPeriodUnitDoesNotPassAnArgumentToAVerblessTemplate(t *testing.T) {
	tr := fixedTranslator{
		// Singular templates carry no verb in every language; this one
		// deliberately does not either, and the dictionary's plural does.
		"fmt_period_day":     "un giorno",
		"fmt_period_days":    "%d giorni",
		"fmt_period_minute":  "un minuto",
		"fmt_period_minutes": "%d minuti",
		"fmt_period_hour":    "un'ora",
		"fmt_period_hours":   "%d ore",
	}

	for _, in := range []int{60, 120, 86400, 86400 * 2} {
		got := FormatPeriodL(in, tr.tr)
		if strings.Contains(got, "%!") {
			t.Errorf("FormatPeriodL(%d) = %q leaks an fmt diagnostic", in, got)
		}
	}
	if got := FormatPeriodL(86400, tr.tr); got != "un giorno" {
		t.Errorf("singular = %q, want %q", got, "un giorno")
	}
	if got := FormatPeriodL(86400*2, tr.tr); got != "2 giorni" {
		t.Errorf("plural = %q, want %q", got, "2 giorni")
	}
}

// TestFormatPeriodLFallsBackWithoutAResolver: a nil translator is the standalone
// watchdog and the English-only callers. It must answer English, not an empty
// string.
func TestFormatPeriodLFallsBackWithoutAResolver(t *testing.T) {
	cases := map[int]string{
		1:         "1 second",
		45:        "45 seconds",
		120:       "2 minutes",
		7200:      "2 hours",
		86400:     "1 day",
		86400 * 5: "5 days",
	}
	for in, want := range cases {
		if got := FormatPeriodL(in, nil); got != want {
			t.Errorf("FormatPeriodL(%d, nil) = %q, want %q", in, got, want)
		}
	}
}

// TestFormatPeriodLFallsBackOnAnUselessResolver: a resolver that echoes the key is
// what model.Translate does for a key the dictionary does not define. resolvePeriodKey
// has to recognise that and answer English, or the alert would read "3600 h".
func TestFormatPeriodLFallsBackOnAnUselessResolver(t *testing.T) {
	useless := func(key string) string { return key }
	for _, in := range []int{45, 120, 7200, 86400} {
		if got := FormatPeriodL(in, useless); !strings.Contains(got, " ") || strings.Contains(got, "fmt_period") {
			t.Errorf("FormatPeriodL(%d) = %q, want the English fallback", in, got)
		}
	}
}

// TestResolvePeriodKeyPrefersTheResolver: the whole point of the translator
// parameter is that the alert follows the UI language.
func TestResolvePeriodKeyPrefersTheResolver(t *testing.T) {
	tr := fixedTranslator{"fmt_period_hours": "%d heures"}
	if got := resolvePeriodKey(tr.tr, "fmt_period_hours"); got != "%d heures" {
		t.Errorf("resolvePeriodKey = %q, want the dictionary entry", got)
	}
	if got := resolvePeriodKey(nil, "fmt_period_hours"); got != "%d hours" {
		t.Errorf("resolvePeriodKey(nil) = %q, want the English fallback", got)
	}
	if got := resolvePeriodKey(func(string) string { return "" }, "fmt_period_hours"); got != "%d hours" {
		t.Errorf("resolvePeriodKey with an empty answer = %q, want the English fallback", got)
	}
	if got := resolvePeriodKey(tr.tr, "fmt_period_unknown_unit"); got != "" {
		t.Errorf("resolvePeriodKey on an unknown key = %q, want empty", got)
	}
}
