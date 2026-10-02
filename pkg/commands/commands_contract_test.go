package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"nasbot/pkg/model"
)

// This file covers the pure and near-pure helpers the message generators and the
// log commands sit on: the verb counter that decides whether a template is
// rendered or replaced by a warning, the Markdown escaper that keeps Telegram
// from rejecting a whole message, the shell-injection guard on /logsearch, and the
// ordering that makes two consecutive messages list the disks the same way.

// TestCountVerbs pins the rule trf uses to decide a template is safe to render.
//
// This is the whole safety mechanism of the message layer: getCountVerbs != number
// of arguments means the user reads "⚠️ [fmt:cpu_fmt] template has 0 verb(s) …"
// instead of their status. An off-by-one here is a visible defect in every
// formatted line of the bot.
func TestCountVerbs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"no verb", "plain text", 0},
		{"empty", "", 0},
		{"single %s", "CPU: %s", 1},
		{"single %d", "at %d cores", 1},
		{"width and precision", "CPU %2.0f%%", 1},
		{"explicit precision", "CPU %.1f%%", 1},
		{"padded int", "at %02d:%02d", 2},
		{"two verbs", "%s has %d cores", 2},
		{"three verbs", "%s %2.0f%% · %s free", 3},
		// "%%" is a literal percent sign, not a verb.
		{"literal percent", "100%% done", 0},
		{"literal percent between verbs", "%s%% of %s", 2},
		{"doubled percent before a verb", "%%%s", 1},
		{"flags", "%-5s %+d %#x", 3},
		{"zero padded width", "%08.3f", 1},
		// A trailing '%' with nothing after it is not a verb: fmt would complain
		// about it too, but trf must not count it or every real template would
		// look like a mismatch.
		{"trailing percent", "done %", 0},
		{"percent at end after a verb", "%s done %", 1},
		// top_header is a Markdown table header that is written with a
		// strings.Builder and never reaches fmt. countVerbs sees "%  M" and "% `"
		// as two verbs; the reason that is harmless is the call-site scanner, not
		// this counter. The row documents the boundary rather than hiding it.
		{"markdown code span", "`PID   CPU%  MEM%  COMMAND`", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countVerbs(tc.in); got != tc.want {
				t.Errorf("countVerbs(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestTrfRefusesAMismatchInsteadOfPrintingFmtDiagnostics is the documented reason
// trf exists: fmt would append "%!(EXTRA float64=50, string=0G)" to the message,
// which leaks internals *and* makes assertions on the rendered text pass for the
// wrong reason.
func TestTrfRefusesAMismatchInsteadOfPrintingFmtDiagnostics(t *testing.T) {
	tr := func(key string) string { return "[" + key + "]" }

	got := trf(tr, "cpu_fmt", "bar", 42.5)
	if strings.Contains(got, "%!") {
		t.Errorf("trf let an fmt diagnostic through: %q", got)
	}
	if !strings.Contains(got, "template has 0 verb(s) for 2 argument(s)") {
		t.Errorf("the mismatch was not reported: %q", got)
	}
	if !strings.Contains(got, "cpu_fmt") {
		t.Errorf("the message must name the key: %q", got)
	}
}

// TestTrfRendersWhenTheCountsAgree: the control for the case above. A version that
// always returned the warning would satisfy every mismatch test.
func TestTrfRendersWhenTheCountsAgree(t *testing.T) {
	got := trf(func(key string) string { return "CPU %s %.0f%%" }, "cpu_fmt", "█████░░░░░", 42.5)
	if got != "CPU █████░░░░░ 42%" {
		t.Errorf("trf = %q", got)
	}
	if strings.Contains(got, "⚠️") {
		t.Error("a matching template must not produce a warning")
	}
}

// TestTrfCountsExtraArgumentsToo: the check is symmetric. Passing three arguments
// to a two-verb template is just as wrong as passing one.
func TestTrfCountsExtraArgumentsToo(t *testing.T) {
	tr := func(string) string { return "%s %d" }
	got := trf(tr, "k", "a", 1, 2)
	if !strings.Contains(got, "2 verb(s) for 3 argument(s)") {
		t.Errorf("trf = %q, want a count mismatch", got)
	}
}

// TestEscapeMarkdown covers the characters that break a Telegram message.
//
// The documented failure: a mount like /mnt/data_archive produces an unpaired `_`,
// Telegram answers 400, and the whole /status message is re-sent as plain text
// with its asterisks and backticks visible.
func TestEscapeMarkdown(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"data_archive", "data\\_archive"},
		{"*bold*", "\\*bold\\*"},
		{"`code`", "\\`code\\`"},
		{"[link]", "\\[link]"},
		{"a_b*c`d[e]", "a\\_b\\*c\\`d\\[e]"},
		{"nothing to escape", "nothing to escape"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := escapeMarkdown(tc.in); got != tc.want {
			t.Errorf("escapeMarkdown(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestDiskDisplayNameIsSafeToInline is the end-to-end version: the name that ends
// up inline in a Telegram message.
func TestDiskDisplayNameIsSafeToInline(t *testing.T) {
	cases := map[string]string{
		"/mnt/data":       "data",
		"/mnt/data_1":     "data\\_1",
		"/":               "root",
		"/mnt/my-disk":    "my-disk",
		"/mnt/a*b":        "a\\*b",
		"/srv/photo[1]":   "photo\\[1]",
		"/mnt/trailing/":  "trailing",
		"/mnt/UPPER":      "UPPER",
		"/mnt/with space": "with space",
	}
	for mount, want := range cases {
		if got := diskDisplayName(mount); got != want {
			t.Errorf("diskDisplayName(%q) = %q, want %q", mount, got, want)
		}
	}
}

// TestDiskDisplayNameNeverLeavesAnUnpairedMarker: the property, over a set of
// hostile names. An odd count of "_" or "*" is what makes Telegram reject the
// message, so the count is the assertion rather than the exact spelling.
func TestDiskDisplayNameNeverLeavesAnUnpairedMarker(t *testing.T) {
	mounts := []string{
		"/mnt/data", "/mnt/data_", "/mnt/_data", "/mnt/a_b_c", "/mnt/*", "/mnt/**",
		"/mnt/`x`", "/mnt/[x]", "/mnt/(_)", "/mnt/[_]*`",
	}
	for _, m := range mounts {
		got := diskDisplayName(m)
		// Escaped markers are literal text, so only the *unescaped* ones are
		// markup and those must pair up. This is the exact condition Telegram
		// rejects a message on.
		for _, marker := range []string{"_", "*"} {
			if n := countUnescaped(got, marker); n%2 != 0 {
				t.Errorf("diskDisplayName(%q) = %q has %d unescaped %q, which is an "+
					"unpaired markdown marker", m, got, n, marker)
			}
		}
		for _, literal := range []string{"`", "["} {
			if n := countUnescaped(got, literal); n != 0 {
				t.Errorf("diskDisplayName(%q) = %q left %d unescaped %q", m, got, n, literal)
			}
		}
	}
}

// countUnescaped counts the occurrences of marker that are not preceded by a
// backslash, i.e. the ones Telegram still reads as markup.
func countUnescaped(s string, marker string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] != marker[0] {
			continue
		}
		escaped := false
		for b := i - 1; b >= 0 && s[b] == '\\'; b-- {
			escaped = !escaped
		}
		if !escaped {
			n++
		}
	}
	return n
}

func TestMountShortName(t *testing.T) {
	cases := map[string]string{
		"/mnt/data":      "data",
		"/mnt/data/":     "data",
		"/":              "root",
		"//":             "root",
		"":               "root",
		"/mnt/deep/path": "path",
		"/Volume1":       "Volume1",
	}
	for mount, want := range cases {
		if got := mountShortName(mount); got != want {
			t.Errorf("mountShortName(%q) = %q, want %q", mount, got, want)
		}
	}
}

// TestVolumeMountsIsDeterministic is the ordering contract: /status and /quick
// both list the disks, and Go randomises map iteration, so without sorting two
// consecutive messages list them in a different order and the user reads it as a
// change of state.
func TestVolumeMountsIsDeterministic(t *testing.T) {
	vols := map[string]VolumeStats{
		"/mnt/zeta":  {},
		"/mnt/alpha": {},
		"/mnt/beta":  {},
		"/srv/data":  {},
	}

	// Ordered by *display* name, not by path: /srv/data shows as "data".
	want := []string{"/mnt/alpha", "/mnt/beta", "/srv/data", "/mnt/zeta"}
	for i := 0; i < 50; i++ {
		got := volumeMounts(vols)
		if len(got) != len(want) {
			t.Fatalf("volumeMounts = %v", got)
		}
		for j := range want {
			if got[j] != want[j] {
				t.Fatalf("run %d: volumeMounts = %v, want %v (map order leaked through)", i, got, want)
			}
		}
	}

	// An empty and a nil map must both answer an empty slice, not nil: the
	// callers range over it and index into the result.
	if got := volumeMounts(nil); len(got) != 0 {
		t.Errorf("volumeMounts(nil) = %v", got)
	}
	if got := configMounts(nil); len(got) != 0 {
		t.Errorf("configMounts(nil) = %v", got)
	}
}

// TestSortedMountsBreaksTiesByFullPath: two mounts can share a short name
// (/mnt/data and /srv/data). The tie-break has to be the full path, or the two
// swap places between messages.
func TestSortedMountsBreaksTiesByFullPath(t *testing.T) {
	got := sortedMounts([]string{"/srv/data", "/mnt/data", "/mnt/alpha"})
	want := []string{"/mnt/alpha", "/mnt/data", "/srv/data"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sortedMounts = %v, want %v", got, want)
		}
	}

	// The input must not be reordered in place: the caller owns its slice.
	input := []string{"/srv/data", "/mnt/alpha"}
	sortedMounts(input)
	if input[0] != "/srv/data" {
		t.Errorf("sortedMounts reordered its argument: %v", input)
	}
}

func TestConfigMountsIsSorted(t *testing.T) {
	cfgs := map[string]ResourceConfig{
		"/mnt/zeta":  {},
		"/mnt/alpha": {Enabled: true},
	}
	want := []string{"/mnt/alpha", "/mnt/zeta"}
	got := configMounts(cfgs)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("configMounts = %v, want %v", got, want)
		}
	}
}

// TestCpuTempStatusThresholds: the icon ladder the user reads at a glance.
func TestCpuTempStatusThresholds(t *testing.T) {
	ctx := newTestAppContext()
	installEnTranslator(t)

	cases := []struct {
		temp float64
		icon string
	}{
		{-1, iconUnknown},
		{0, iconUnknown}, // no reading must never be a green tick
		{0.1, "✅"},
		{cpuWarmC, "✅"}, // the boundary belongs to the cooler tier
		{cpuWarmC + 0.1, "🟡"},
		{cpuHotC, "🟡"},
		{cpuHotC + 0.1, "🔥"},
		{120, "🔥"},
	}
	for _, tc := range cases {
		icon, status := cpuTempStatus(ctx, tc.temp)
		if icon != tc.icon {
			t.Errorf("cpuTempStatus(%.1f) icon = %q, want %q", tc.temp, icon, tc.icon)
		}
		if status == "" {
			t.Errorf("cpuTempStatus(%.1f) returned no status text", tc.temp)
		}
	}
}

// TestDiskTempStatusRefusesToShowUnknownAsHealthy is the documented case:
// readDiskSMART answers (-1, "UNKNOWN") when smartctl is missing from sudoers or
// the disk has no SMART support, and that has to render as "no data" rather than
// as a green tick on a disk nobody is watching.
func TestDiskTempStatusRefusesToShowUnknownAsHealthy(t *testing.T) {
	ctx := newTestAppContext()
	installEnTranslator(t)

	cases := []struct {
		name   string
		temp   int
		health string
		icon   string
	}{
		{"missing reading", -1, "OK", iconUnknown},
		{"unknown health", 35, "UNKNOWN", iconUnknown},
		{"unknown health in lower case", 35, "unknown", iconUnknown},
		{"unknown health padded", 35, "  UNKNOWN  ", iconUnknown},
		{"healthy", 35, "PASSED", "✅"},
		{"failing", 35, "FAILED!", "🚨"},
		{"failing at high temperature", 60, "FAILED!", "🚨"},
		{"warm", diskWarmC + 1, "OK", "🟡"},
		{"at the warm boundary", diskWarmC, "OK", "✅"},
		{"very hot but passing", 70, "OK", "🟡"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			icon, status := diskTempStatus(ctx, tc.temp, tc.health)
			if icon != tc.icon {
				t.Errorf("diskTempStatus(%d, %q) icon = %q, want %q", tc.temp, tc.health, icon, tc.icon)
			}
			if status == "" {
				t.Error("no status text")
			}
		})
	}
}

// TestFailureBeatsWarm: a failing disk is 🚨 whatever its temperature, because the
// icon is what decides whether the user opens the NAS.
func TestFailureBeatsWarm(t *testing.T) {
	ctx := newTestAppContext()
	icon, _ := diskTempStatus(ctx, 80, "FAILING")
	if icon != "🚨" {
		t.Errorf("a failing disk at 80C reports %q, want 🚨", icon)
	}
}

func TestRuneSliceAndTail(t *testing.T) {
	const s = "🚀🚨📊🖥💿"

	if got := runeSlice(s, 3); got != "🚀🚨📊" {
		t.Errorf("runeSlice = %q", got)
	}
	if got := runeSlice(s, 10); got != s {
		t.Errorf("runeSlice beyond the length = %q, want the input", got)
	}
	if got := runeSlice(s, 0); got != "" {
		t.Errorf("runeSlice(0) = %q", got)
	}
	if got := runeSlice(s, -1); got != "" {
		t.Errorf("runeSlice(-1) = %q", got)
	}

	// The tail keeps the newest data, which is the whole point: format.Truncate
	// drops the head instead.
	if got := runeTail(s, 3); got != "📊🖥💿" {
		t.Errorf("runeTail = %q", got)
	}
	if got := runeTail(s, 10); got != s {
		t.Errorf("runeTail beyond the length = %q", got)
	}
	if got := runeTail(s, 0); got != "" {
		t.Errorf("runeTail(0) = %q", got)
	}
}

// TestRuneHelpersNeverSplitARune: a byte slice would produce U+FFFD, and Telegram
// rejects a message containing it.
func TestRuneHelpersNeverSplitARune(t *testing.T) {
	const s = "città italianà"
	for _, got := range []string{runeSlice(s, 4), runeTail(s, 4)} {
		if strings.ContainsRune(got, '�') {
			t.Errorf("a rune was split: %q", got)
		}
	}
}

func TestFormatTotalMB(t *testing.T) {
	cases := map[float64]string{
		0:        "0M",
		-1:       "0M", // a counter that went backwards must not print a negative
		512:      "512M",
		1024:     "1.0G",
		1024 * 5: "5.0G",
	}
	for in, want := range cases {
		if got := formatTotalMB(in); got != want {
			t.Errorf("formatTotalMB(%v) = %q, want %q", in, got, want)
		}
	}
}

// timeAt and timeDurationOf build the timestamps the prediction tests need without
// pinning the wall clock: the regression only looks at the differences.
func timeAt(nanos int64) time.Time { return time.Unix(0, 0).Add(time.Duration(nanos)) }

func timeDurationOf(days int) time.Duration { return time.Duration(days) * 24 * time.Hour }

// TestCollectDiskSamplesIgnoresDisksMissingFromTheHistory is the documented
// phantom-rate bug: a disk mounted less than the length of the history window has
// no entry in the oldest points, and treating the missing zero as a real
// measurement produced a fill rate out of thin air.
func TestCollectDiskSamplesIgnoresDisksMissingFromTheHistory(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := timeAt(-48 * 3600)

	history := []DiskUsagePoint{
		{Time: start, SSDFree: 100 * giB, SecondaryFree: nil}, // disk not mounted yet
		{Time: start.Add(24 * 3600e9), SSDFree: 90 * giB, SecondaryFree: map[string]uint64{"/mnt/data": 50 * giB}},
		{Time: start.Add(48 * 3600e9), SSDFree: 80 * giB, SecondaryFree: map[string]uint64{"/mnt/data": 45 * giB}},
	}

	samples, err := collectDiskSamples(history, "/mnt/data")
	if err != nil {
		t.Fatalf("collectDiskSamples: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("got %d samples, want the 2 where the disk was present: %+v", len(samples), samples)
	}
	// The origin is the first *history* point, so the mount lands at day 1. The
	// part that matters is that the day-0 zero is gone: keeping it would have
	// turned "the disk was not mounted" into a 50 GiB drop in one day.
	if samples[0].days != 1 || samples[0].freeGB != 50 {
		t.Errorf("first sample = %+v, want day 1 at 50 GiB", samples[0])
	}
	if samples[1].days != 2 || samples[1].freeGB != 45 {
		t.Errorf("second sample = %+v, want day 2 at 45 GiB", samples[1])
	}
}

// TestCollectDiskSamplesRejectsShortHistories: one point cannot produce a slope.
func TestCollectDiskSamplesRejectsShortHistories(t *testing.T) {
	if _, err := collectDiskSamples(nil, "SSD"); err == nil {
		t.Error("an empty history must be refused")
	}
	if _, err := collectDiskSamples([]DiskUsagePoint{{}}, "SSD"); err == nil {
		t.Error("a single point must be refused")
	}
	// A disk with no presence at all is refused too, not reported as stable.
	history := []DiskUsagePoint{
		{Time: timeAt(-3600e9), SecondaryFree: map[string]uint64{"/other": 1}},
		{Time: timeAt(0), SecondaryFree: map[string]uint64{"/other": 2}},
	}
	if _, err := collectDiskSamples(history, "/mnt/data"); err == nil {
		t.Error("a disk absent from the whole history must be refused")
	}
}

// TestCollectDiskSamplesIgnoresOutOfOrderPoints: a clock that jumped backwards
// would invert the slope and report a disk that is emptying as one that is
// filling.
func TestCollectDiskSamplesIgnoresOutOfOrderPoints(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := timeAt(-3600e9)

	history := []DiskUsagePoint{
		{Time: start, SSDFree: 100 * giB},
		{Time: start.Add(-7200e9), SSDFree: 1}, // before the origin
		{Time: start.Add(3600e9), SSDFree: 99 * giB},
	}

	samples, err := collectDiskSamples(history, "SSD")
	if err != nil {
		t.Fatalf("collectDiskSamples: %v", err)
	}
	for _, s := range samples {
		if s.days < 0 {
			t.Errorf("a negative day survived: %+v", s)
		}
	}
	if len(samples) != 2 {
		t.Errorf("got %d samples, want the 2 in-order ones", len(samples))
	}
}

// TestPredictDiskFullStateDistinguishesTheThreeOutcomes: "not filling up" and
// "no data" are different things and a consumer must not confuse them.
func TestPredictDiskFullStateDistinguishesTheThreeOutcomes(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := timeAt(-10 * 24 * 3600e9)

	cases := []struct {
		name  string
		build func() []DiskUsagePoint
		state predState
	}{
		{
			name: "filling up",
			build: func() []DiskUsagePoint {
				var h []DiskUsagePoint
				for d := 0; d <= 10; d++ {
					h = append(h, DiskUsagePoint{
						Time:    start.Add(time.Duration(d) * 24 * 3600e9),
						SSDFree: uint64(100-d*2) * giB,
					})
				}
				return h
			},
			state: predFilling,
		},
		{
			name: "growing, so not filling",
			build: func() []DiskUsagePoint {
				var h []DiskUsagePoint
				for d := 0; d <= 10; d++ {
					h = append(h, DiskUsagePoint{
						Time:    start.Add(time.Duration(d) * 24 * 3600e9),
						SSDFree: uint64(100+d) * giB,
					})
				}
				return h
			},
			state: predNotFilling,
		},
		{
			// The documented noise floor: the least-squares slope of a perfectly
			// constant series is not exactly zero in floating point, and a slope
			// of -1e-13 GB/day would be reported as "more than a year until full".
			name: "perfectly constant",
			build: func() []DiskUsagePoint {
				var h []DiskUsagePoint
				for d := 0; d <= 10; d++ {
					h = append(h, DiskUsagePoint{
						Time:    start.Add(timeDurationOf(d)),
						SSDFree: 100 * giB,
					})
				}
				return h
			},
			state: predNotFilling,
		},
		{
			name: "history too short",
			build: func() []DiskUsagePoint {
				return []DiskUsagePoint{{Time: start, SSDFree: 100 * giB}}
			},
			state: predNoData,
		},
		{
			// Two points in the same instant: no span, so no slope.
			name: "no elapsed time",
			build: func() []DiskUsagePoint {
				return []DiskUsagePoint{
					{Time: start, SSDFree: 100 * giB},
					{Time: start, SSDFree: 99 * giB},
				}
			},
			state: predNoData,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, state := predictDiskFullState(tc.build(), "SSD")
			if state != tc.state {
				t.Errorf("state = %v, want %v", state, tc.state)
			}
			pred := predictDiskFull(tc.build(), "SSD")
			if tc.state == predFilling {
				if pred.DaysUntilFull <= 0 || pred.GBPerDay <= 0 {
					t.Errorf("a filling disk must report a positive estimate: %+v", pred)
				}
			} else if pred.DaysUntilFull != -1 {
				t.Errorf("a non-filling disk must report -1, got %.2f", pred.DaysUntilFull)
			}
		})
	}
}

// TestPredictDiskFullUsesEverySample pins the documented reason for the
// least-squares fit: with only the last two points, one big backup in the final
// point flips the prediction from "over a year" to "3 days".
func TestPredictDiskFullUsesEverySample(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	start := timeAt(-20 * 24 * 3600e9)

	var h []DiskUsagePoint
	for d := 0; d <= 19; d++ {
		h = append(h, DiskUsagePoint{
			Time:    start.Add(timeDurationOf(d)),
			SSDFree: uint64(200-d) * giB, // 1 GiB/day, steady
		})
	}
	// One huge final write: 60 GiB in a single point.
	last := &h[len(h)-1]
	last.SSDFree -= 60 * giB

	pred := predictDiskFull(h, "SSD")
	if pred.GBPerDay <= 0 {
		t.Fatalf("GBPerDay = %v", pred.GBPerDay)
	}
	// The regression over 20 days of a 60 GiB drop is about 4 GiB/day, not 60.
	if pred.GBPerDay > 10 {
		t.Errorf("GBPerDay = %.2f: a single outlier point dominated the regression", pred.GBPerDay)
	}
}

// TestRecordDiskUsageCapsTheHistory: a long-running bot grows DiskHistory without
// bound, and every /diskpred copies the whole slice under the state lock.
func TestRecordDiskUsageCapsTheHistory(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Stats.Set(Stats{VolSSD: VolumeStats{Used: 1, Free: 2}})

	// Fill past the cap with recognisable points.
	ctx.State.Mu.Lock()
	for i := 0; i < 2100; i++ {
		ctx.State.DiskHistory = append(ctx.State.DiskHistory, DiskUsagePoint{SSDFree: uint64(i)})
	}
	ctx.State.Mu.Unlock()

	// recordDiskUsage appends one point and then trims one when the history is
	// over the cap, so the length holds steady instead of shrinking: a history
	// that reached the cap once stays there. What matters is that it stops
	// growing, because every /diskpred copies the whole slice under a lock.
	for i := 0; i < 50; i++ {
		recordDiskUsage(ctx)
	}

	ctx.State.Mu.Lock()
	n := len(ctx.State.DiskHistory)
	newest := ctx.State.DiskHistory[n-1]
	ctx.State.Mu.Unlock()

	if n != 2100 {
		t.Errorf("history holds %d points after 50 recordings, want it steady at 2100", n)
	}

	// From below the cap the ceiling is exact, which is the case a normally
	// behaving bot is in. Note that a history already *above* the cap stays above
	// it: recordDiskUsage trims only as many points as it appended, so the limit
	// stops further growth rather than clamping. See the report.
	ctx.State.Mu.Lock()
	ctx.State.DiskHistory = make([]DiskUsagePoint, 2016)
	ctx.State.Mu.Unlock()
	recordDiskUsage(ctx)
	ctx.State.Mu.Lock()
	atCap := len(ctx.State.DiskHistory)
	ctx.State.Mu.Unlock()
	if atCap != 2016 {
		t.Errorf("a history at the cap grew to %d points", atCap)
	}
	// The newest point must be the one just recorded, not the one trimmed.
	if newest.SSDUsed != 1 || newest.SSDFree != 2 {
		t.Errorf("the newest point = %+v, want the sample just taken", newest)
	}
}

// TestRecordDiskUsageIgnoresAnUnreadyContext: before the first sample there is
// nothing to record, and a zero point would drag the regression to "full".
func TestRecordDiskUsageIgnoresAnUnreadyContext(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Stats = &model.ThreadSafeStats{}

	recordDiskUsage(ctx)

	if n := len(ctx.State.DiskHistory); n != 0 {
		t.Errorf("recorded %d points before the stats were ready", n)
	}
}

// TestGetDiskPredictionTextReportsCollectingUntilItHasHistory: below the
// threshold the generator must say so and report the count, instead of predicting
// from two points.
func TestGetDiskPredictionTextReportsCollectingUntilItHasHistory(t *testing.T) {
	ctx := newTestAppContext()
	installEnTranslator(t)

	text := getDiskPredictionText(ctx)
	if !strings.Contains(text, "[diskpred_collecting]") {
		t.Errorf("with no history the generator must report it is collecting:\n%s", text)
	}
	if !strings.Contains(text, "Data points: 0/12") {
		t.Errorf("the point count must be filled in:\n%s", text)
	}

	// Eleven points is still below the threshold.
	ctx.State.DiskHistory = make([]DiskUsagePoint, 11)
	text = getDiskPredictionText(ctx)
	if strings.Contains(text, "SSD") {
		t.Errorf("with 11 points nothing must be predicted yet:\n%s", text)
	}
	if !strings.Contains(text, "Data points: 11/12") {
		t.Errorf("the point count must be filled in:\n%s", text)
	}
}

// TestGetLogSearchTextRefusesShellMetacharacters is the injection guard.
//
// The container name reaches `docker logs … <name>` through the runner, so a name
// carrying a shell metacharacter could append a command. The guard refuses those
// names *before* any command runs, which is the property worth pinning.
func TestGetLogSearchTextRefusesShellMetacharacters(t *testing.T) {
	installEnTranslator(t)

	var ran bool
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	// The container is the first whitespace-delimited token, so these are names a
	// user could type with no token boundary in between.
	hostile := []string{
		"plex;",
		"plex&&",
		"plex|cat",
		"plex`id`",
		"plex$(id)",
		`plex"`,
		"plex'",
		`plex\`,
		"plex$HOME",
		";reboot",
		"a;b",
	}
	for _, name := range hostile {
		ran = false
		got := getLogSearchText(newTestAppContext(), name+" error")

		if !strings.Contains(got, "[logsearch_invalid_container]") {
			t.Errorf("%q was not refused, got:\n%s", name, got)
		}
		if ran {
			t.Errorf("%q reached the command runner", name)
		}
	}
}

// TestGetLogSearchTextRequiresBothArguments is the usage contract: with only a
// container name there is nothing to search for.
func TestGetLogSearchTextRequiresBothArguments(t *testing.T) {
	installEnTranslator(t)

	for _, args := range []string{"", "   ", "plex", "  plex  "} {
		got := getLogSearchText(newTestAppContext(), args)
		if args == "  plex  " {
			// A padded single word is still a single word.
			if !strings.Contains(got, "[logsearch_usage]") {
				t.Errorf("args=%q must produce the usage message, got:\n%s", args, got)
			}
			continue
		}
		if !strings.Contains(got, "[logsearch_usage]") {
			t.Errorf("args=%q must produce the usage message, got:\n%s", args, got)
		}
	}
}

// TestGetLogSearchTextFiltersCaseInsensitivelyAndCapsTheMatches: the useful part
// of /logsearch. The match count has to be the *total*, with the displayed ones
// capped, or the user is told "found 12" and shown 10 with no explanation.
func TestGetLogSearchTextFiltersCaseInsensitivelyAndCapsTheMatches(t *testing.T) {
	installEnTranslator(t)

	var b strings.Builder
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, "line %d: Error something happened\n", i)
	}
	b.WriteString("line 15: all fine\n")
	canned := b.String()

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "docker" {
				t.Errorf("ran %q, want docker", name)
			}
			return []byte(canned), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLogSearchText(newTestAppContext(), "plex ERROR")

	if strings.Contains(got, "all fine") {
		t.Errorf("a non-matching line leaked into the output:\n%s", got)
	}
	if !strings.Contains(got, "Found 15 matches (showing last 10)") {
		t.Errorf("the count must be the total with the shown ones capped:\n%s", got)
	}
	if n := strings.Count(got, "something happened"); n != 10 {
		t.Errorf("expected the last 10 matches, got %d:\n%s", n, got)
	}
	if !strings.HasPrefix(got, "🔍 *Log Search*") {
		t.Errorf("the header must name the keyword and the container:\n%s", got)
	}
}

// TestGetLogSearchTextReportsNoMatches: an empty result is a sentence, not an
// empty code block.
func TestGetLogSearchTextReportsNoMatches(t *testing.T) {
	installEnTranslator(t)

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("nothing interesting here\n"), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLogSearchText(newTestAppContext(), "plex error")
	if !strings.Contains(got, "No matches for") {
		t.Errorf("expected the no-matches line, got:\n%s", got)
	}
	if strings.Contains(got, "```") {
		t.Errorf("an empty code block was emitted:\n%s", got)
	}
}

// TestGetLogSearchTextSurfacesACommandFailure: a Docker error must be reported as
// an error, not as "no matches", or the user is told their container has no
// errors when in fact it could not be read.
func TestGetLogSearchTextSurfacesACommandFailure(t *testing.T) {
	installEnTranslator(t)

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("docker: no such container")
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLogSearchText(newTestAppContext(), "plex error")
	if !strings.Contains(got, "no such container") {
		t.Errorf("the command error must be surfaced, got:\n%s", got)
	}
	if strings.Contains(got, "No matches") {
		t.Errorf("a failed command was reported as no matches:\n%s", got)
	}
}

// TestGetRecentLogsFallsBackToJournalctl is the documented bug: dmesg blocks until
// its deadline when the kernel ring buffer is full, and the fallback then had ~0ms
// left, so /logs reported "no logs" even though journalctl would have worked.
func TestGetRecentLogsFallsBackToJournalctl(t *testing.T) {
	var order []string
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			order = append(order, name)
			switch name {
			case "dmesg":
				// Deadline exceeded, with no output at all.
				return nil, context.DeadlineExceeded
			case "journalctl":
				return []byte("Sep 02 00:00:00 nasbot started\n"), nil
			}
			return nil, fmt.Errorf("unexpected command %q", name)
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	out, err := getRecentLogs(newTestAppContext())
	if err != nil {
		t.Fatalf("getRecentLogs: %v", err)
	}
	if !strings.Contains(out, "nasbot started") {
		t.Errorf("the journalctl output was not used: %q", out)
	}
	if len(order) != 2 || order[0] != "dmesg" || order[1] != "journalctl" {
		t.Errorf("commands run = %v, want dmesg then journalctl", order)
	}
}

// TestGetRecentLogsPrefersDmesg: the fallback must not run when dmesg answered.
func TestGetRecentLogsPrefersDmesg(t *testing.T) {
	var ran []string
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			ran = append(ran, name)
			if name == "dmesg" {
				return []byte("kernel line\n"), nil
			}
			return []byte("journal line\n"), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	out, err := getRecentLogs(newTestAppContext())
	if err != nil {
		t.Fatalf("getRecentLogs: %v", err)
	}
	if !strings.Contains(out, "kernel line") {
		t.Errorf("out = %q", out)
	}
	if len(ran) != 1 {
		t.Errorf("commands run = %v, want dmesg only", ran)
	}
}

// TestGetRecentLogsReportsBothFailures: with neither source available the error
// must name both, or the operator cannot tell which command to run by hand.
func TestGetRecentLogsReportsBothFailures(t *testing.T) {
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			return nil, fmt.Errorf("%s: not permitted", name)
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	_, err := getRecentLogs(newTestAppContext())
	if err == nil {
		t.Fatal("expected an error when both sources fail")
	}
	if !strings.Contains(err.Error(), "dmesg") || !strings.Contains(err.Error(), "journalctl") {
		t.Errorf("the error must name both commands, got %v", err)
	}
}

// TestGetRecentLogsKeepsTheNewestLinesAndIsRuneSafe: a byte slice would split a
// non-ASCII kernel timestamp into U+FFFD and Telegram would reject the message.
func TestGetRecentLogsKeepsTheNewestLinesAndIsRuneSafe(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxLogLines+50; i++ {
		fmt.Fprintf(&b, "kernel line %d àèìòù\n", i)
	}
	canned := b.String()

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte(canned), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	out, err := getRecentLogs(newTestAppContext())
	if err != nil {
		t.Fatalf("getRecentLogs: %v", err)
	}
	if strings.ContainsRune(out, '�') {
		t.Errorf("a rune was split:\n%s", out)
	}
	if n := strings.Count(out, "\n") + 1; n > maxLogLines {
		t.Errorf("kept %d lines, want at most %d", n, maxLogLines)
	}
	// The newest line must be present and the oldest dropped.
	if !strings.Contains(out, fmt.Sprintf("kernel line %d", maxLogLines+49)) {
		t.Error("the newest line was dropped")
	}
	if strings.Contains(out, "kernel line 0 ") {
		t.Error("the oldest line was kept: /logs must show the newest window")
	}
}

// TestGetLogsTextWrapsTheOutput: /logs must not dump a raw table into the chat.
func TestGetLogsTextWrapsTheOutput(t *testing.T) {
	installEnTranslator(t)

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("kernel line\n"), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLogsText(newTestAppContext())
	if !strings.HasPrefix(got, "[logs_title]") {
		t.Errorf("the title must come first:\n%s", got)
	}
	if !strings.Contains(got, "```") {
		t.Errorf("the output must be fenced:\n%s", got)
	}
}

// TestGetLogsTextReportsNoLogsRatherThanAnEmptyBlock.
func TestGetLogsTextReportsNoLogsRatherThanAnEmptyBlock(t *testing.T) {
	installEnTranslator(t)

	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("not permitted")
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLogsText(newTestAppContext())
	if strings.Contains(got, "```") {
		t.Errorf("an empty code block was emitted:\n%s", got)
	}
	if !strings.Contains(got, "[logs_title]") || !strings.Contains(got, "No logs") {
		t.Errorf("expected the no-logs message, got:\n%s", got)
	}
}

// TestGetLocalIPUsesTheHostnameOutput is the fast path: the first address reported
// by `hostname -I`.
func TestGetLocalIPUsesTheHostnameOutput(t *testing.T) {
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(_ context.Context, name string, _ ...string) ([]byte, error) {
			if name != "hostname" {
				t.Errorf("ran %q, want hostname", name)
			}
			return []byte("192.168.1.50 172.17.0.1\n"), nil
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	if got := getLocalIP(context.Background()); got != "192.168.1.50" {
		t.Errorf("getLocalIP = %q", got)
	}
}

// TestGetLocalIPFallsBackToTheInterfaces: when `hostname -I` is missing or prints
// nothing, the net interfaces are read instead. Both answer "n/a" as the last
// resort, and never an empty string.
func TestGetLocalIPFallsBackToTheInterfaces(t *testing.T) {
	BindRuntime(RuntimeDeps{
		RunCommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("hostname: not found")
		},
	})
	t.Cleanup(func() { BindRuntime(RuntimeDeps{}) })

	got := getLocalIP(context.Background())
	if got == "" {
		t.Error("getLocalIP returned an empty string; the caller would render a blank")
	}
	if got == "n/a" {
		return // an isolated CI container legitimately has no non-loopback address
	}
	if !strings.Contains(got, ".") {
		t.Errorf("getLocalIP = %q, want an address", got)
	}
}
