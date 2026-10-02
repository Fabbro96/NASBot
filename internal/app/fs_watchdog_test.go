package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"nasbot/pkg/model"
)

// ═══════════════════════════════════════════════════════════════════
//  WIRING - the watchdog has to be inside the bot, not beside it
// ═══════════════════════════════════════════════════════════════════

// TestFSWatchdogLaneIsRegisteredInTheManager pins the defect this lane exists to
// fix: 885 lines of filesystem watchdog behind a build tag that nothing built,
// so the disk-space monitoring the user expects never ran in any distribution.
//
// The cheap way to make it dead code again is to drop the entry from
// watchdogsFor: nothing else would notice, the package would still compile and
// the tests would still pass. So the entry itself is asserted, by name.
func TestFSWatchdogLaneIsRegisteredInTheManager(t *testing.T) {
	lanes := watchdogsFor(InitApp(&Config{}))
	for _, wd := range lanes {
		if wd.name != "fs-space" {
			continue
		}
		if wd.run == nil || wd.interval == nil {
			t.Fatal("the fs-space lane has no run or no interval")
		}
		return
	}
	names := make([]string, 0, len(lanes))
	for _, wd := range lanes {
		names = append(names, wd.name)
	}
	t.Fatalf("no fs-space lane in the autonomous manager, lanes are: %v", names)
}

// TestFSWatchdogLaneCadence pins the cadence: it comes from
// fs_watchdog.check_interval_minutes, and an unusable value falls back to the
// documented default instead of spinning or stopping.
func TestFSWatchdogLaneCadence(t *testing.T) {
	cases := []struct {
		mins int
		want time.Duration
	}{
		{30, 30 * time.Minute},
		{5, 5 * time.Minute},
		{1, time.Minute},
		{1440, 24 * time.Hour},
		{0, fsWatchdogDefaultInterval},
		{-7, fsWatchdogDefaultInterval},
	}

	lane := fsWatchdogLaneFor(t, InitApp(&Config{}))
	for _, tc := range cases {
		got := lane.interval(&Config{FSWatchdog: FSWatchdogConfig{CheckIntervalMins: tc.mins}})
		if got != tc.want {
			t.Errorf("interval(check_interval_minutes=%d) = %s, want %s", tc.mins, got, tc.want)
		}
	}
}

// TestFSWatchdogLaneFirstCheckIsEarly pins the one thing the lane asks for that
// the manager does not do by default: measure once after a minute instead of
// after a whole 30 minute interval, so a disk that was already full at boot is
// seen at boot.
func TestFSWatchdogLaneFirstCheckIsEarly(t *testing.T) {
	lane := fsWatchdogLaneFor(t, InitApp(&Config{}))
	if lane.initialDelay == nil {
		t.Fatal("the fs-space lane has no initialDelay, the first check waits a full interval")
	}
	if got := lane.initialDelay(&Config{}); got != fsWatchdogFirstCheckDelay {
		t.Errorf("initialDelay = %s, want %s", got, fsWatchdogFirstCheckDelay)
	}
	if !(fsWatchdogFirstCheckDelay < fsWatchdogDefaultInterval) {
		t.Errorf("the first check (%s) is not earlier than the interval (%s): the delay buys nothing",
			fsWatchdogFirstCheckDelay, fsWatchdogDefaultInterval)
	}
}

// TestFSWatchdogLaneRespectsEnabledFlag runs the lane both ways and looks at the
// only thing that changes: whether a check happened at all. With enabled=false
// nothing must touch the filesystem, so the timestamp of the last light check
// stays where it was.
func TestFSWatchdogLaneRespectsEnabledFlag(t *testing.T) {
	base := func(enabled bool) *Config {
		return &Config{
			Paths: model.PathsConfig{SSD: t.TempDir()},
			FSWatchdog: model.FSWatchdogConfig{
				Enabled:           enabled,
				CheckIntervalMins: 30,
				WarningThreshold:  99.9,
				CriticalThreshold: 100,
				DeepScanPaths:     []string{t.TempDir()},
			},
		}
	}

	w := GetFSWatchdog()
	// The singleton is package-wide: put its timestamps back the way they were
	// so this test does not depend on, or disturb, the order it runs in.
	beforeLight, beforeDeep := w.lastCheckTimes()
	t.Cleanup(func() {
		w.mu.Lock()
		w.lastLightCheck, w.lastDeepScan = beforeLight, beforeDeep
		w.mu.Unlock()
	})

	lane := fsWatchdogLaneFor(t, InitApp(base(false)))

	// A cancelled runCtx: the light check still runs, but anything the lane
	// would hand to a background goroutine (the deep scan) bails out at once,
	// so the test never leaves a scan walking the filesystem.
	stopped, cancel := context.WithCancel(context.Background())
	cancel()

	lane.run(InitApp(base(false)), nil, stopped)
	if light, _ := w.lastCheckTimes(); !light.Equal(beforeLight) {
		t.Errorf("the disabled lane still checked: last light check moved from %s to %s", beforeLight, light)
	}

	lane.run(InitApp(base(true)), nil, stopped)
	if light, _ := w.lastCheckTimes(); light.Equal(beforeLight) {
		t.Error("the enabled lane did not check: the filesystem watchdog is dead code again")
	}
}

// ═══════════════════════════════════════════════════════════════════
//  CONFIGURATION
// ═══════════════════════════════════════════════════════════════════

// TestFSWatchdogConfigReadsThePublishedSnapshot proves the config comes from the
// bot's snapshot and not from the package-level mirror: publishing a new one
// changes what the watchdog sees, with no restart and no watchdog publish.
func TestFSWatchdogConfigReadsThePublishedSnapshot(t *testing.T) {
	first := &Config{FSWatchdog: model.FSWatchdogConfig{WarningThreshold: 70, DeepScanPaths: []string{"/a"}}}
	ctx := InitApp(first)

	if got := fsWatchdogConfig(ctx).WarningThreshold; got != 70 {
		t.Fatalf("WarningThreshold = %v, want 70", got)
	}

	second := &Config{FSWatchdog: model.FSWatchdogConfig{WarningThreshold: 91, DeepScanPaths: []string{"/b"}}}
	ctx.SetConfig(second)

	if got := fsWatchdogConfig(ctx).WarningThreshold; got != 91 {
		t.Errorf("after a reload WarningThreshold = %v, want 91: the watchdog reads a stale config", got)
	}
	if got := fsWatchdogConfig(ctx).DeepScanPaths; len(got) != 1 || got[0] != "/b" {
		t.Errorf("DeepScanPaths = %v, want [/b]", got)
	}
}

// TestFSWatchdogConfigCopiesTheMutableSlices guards the two slices: a reload
// replaces the Config, and a check in progress must not see them change under
// its feet.
func TestFSWatchdogConfigCopiesTheMutableSlices(t *testing.T) {
	conf := &Config{FSWatchdog: model.FSWatchdogConfig{
		DeepScanPaths:   []string{"/data"},
		ExcludePatterns: []string{"/proc"},
	}}
	snapshot := fsWatchdogConfigFrom(conf)

	conf.FSWatchdog.DeepScanPaths[0] = "/changed"
	conf.FSWatchdog.ExcludePatterns[0] = "/changed"

	if snapshot.DeepScanPaths[0] != "/data" || snapshot.ExcludePatterns[0] != "/proc" {
		t.Errorf("the snapshot shares the slices with the live config: %v %v",
			snapshot.DeepScanPaths, snapshot.ExcludePatterns)
	}
}

func TestMonitoredPaths(t *testing.T) {
	conf := &Config{
		Paths: model.PathsConfig{SSD: "/Volume1"},
		Notifications: model.NotificationsConfig{
			SecondaryDisks: map[string]model.ResourceConfig{
				"/mnt/usb": {},
				// Same volume as Paths.SSD: monitoring it twice would double
				// the messages and burn the cooldown twice.
				"/Volume1": {},
			},
		},
	}

	got := monitoredPaths(conf)
	want := map[string]int{"/": 1, "/Volume1": 1, "/mnt/usb": 1}
	if len(got) != len(want) {
		t.Fatalf("monitoredPaths = %v, want %d distinct paths", got, len(want))
	}
	for _, p := range got {
		if want[p] == 0 {
			t.Errorf("unexpected path %q", p)
			continue
		}
		want[p]--
	}
	for p, n := range want {
		if n != 0 {
			t.Errorf("path %q appears %d times too many", p, n)
		}
	}

	// No SSD configured: "/" still has to be watched, and an empty SSD must not
	// become a path of its own.
	if got := monitoredPaths(&Config{}); len(got) != 1 || got[0] != "/" {
		t.Errorf("monitoredPaths(&Config{}) = %v, want [/]", got)
	}
	if got := monitoredPaths(nil); got != nil {
		t.Errorf("monitoredPaths(nil) = %v, want nil", got)
	}
}

// TestGetDiskInfoTextFollowsTheUserLanguage covers the manual report: same six
// keys, but rendered with the language of the settings.
func TestGetDiskInfoTextFollowsTheUserLanguage(t *testing.T) {
	ctx := InitApp(&Config{Paths: model.PathsConfig{SSD: t.TempDir()}})
	ctx.Settings.SetLanguage("it")

	text := GetDiskInfoText(ctx)
	if !strings.HasPrefix(text, translations["it"]["fswd_disk_status_title"]) {
		t.Errorf("GetDiskInfoText does not start with the italian title.\n got: %q\nwant prefix: %q",
			text[:min(len(text), 40)], translations["it"]["fswd_disk_status_title"])
	}
	if strings.Contains(text, "%!") {
		t.Errorf("the report carries an unexpanded verb: %q", text)
	}
}

// ═══════════════════════════════════════════════════════════════════
//  MESSAGES
// ═══════════════════════════════════════════════════════════════════

// TestFSWatchdogMessagesSurviveEveryLanguage is the invariant the switch to
// ctx.Tr had to preserve.
//
// Every fswd_* key is rendered through fmt in all six languages, with the arity
// the call site really uses. fmt reports a mismatch as %!(EXTRA ...) or %!d(...)
// *in the text that reaches the chat*, so this is the check that keeps a
// mistranslation from shipping a broken alert. The English-only shim could not
// fail this way: it never saw another language at all.
func TestFSWatchdogMessagesSurviveEveryLanguage(t *testing.T) {
	const (
		path   = "/Volume1"
		pct    = 87.5
		freeGB = 12.3
	)

	// The arity each key really gets at its call site: the two space alerts are
	// formatted, the four titles are appended as they are.
	withArgs := map[string]bool{
		"fswd_space_warn": true,
		"fswd_space_crit": true,
	}

	for lang, dict := range translations {
		for _, key := range []string{
			"fswd_space_warn",
			"fswd_space_crit",
			"fswd_deepscan_title",
			"fswd_largest_dirs",
			"fswd_largest_files",
			"fswd_disk_status_title",
		} {
			tmpl, ok := dict[key]
			if !ok {
				t.Errorf("%s: key %q is missing", lang, key)
				continue
			}

			var out string
			if withArgs[key] {
				out = fmt.Sprintf(tmpl, path, pct, freeGB)
				// The numbers must really be in there: a template that kept
				// the verb but not the value shows up as an empty gap.
				if !strings.Contains(out, path) || !strings.Contains(out, "87.5") || !strings.Contains(out, "12.3") {
					t.Errorf("%s/%s: the arguments did not land in the text:\n%s", lang, key, out)
				}
			} else {
				out = fmt.Sprintf(tmpl)
			}

			if strings.Contains(out, "%!") {
				t.Errorf("%s/%s: broken format string, this text would reach the chat:\n%s", lang, key, out)
			}
		}
	}
}

// TestFSWatchdogMessagesUseTheUserLanguage, not English: the six call sites read
// the language from the settings. Before the watchdog joined the bot they went
// through the context-free shim, which resolves to "en" no matter what.
func TestFSWatchdogMessagesUseTheUserLanguage(t *testing.T) {
	for _, lang := range []string{"en", "it", "de"} {
		ctx := InitApp(&Config{})
		ctx.Settings.SetLanguage(lang)

		if got := ctx.Tr("fswd_space_warn"); got != translations[lang]["fswd_space_warn"] {
			t.Errorf("Tr(fswd_space_warn) with language %q did not follow the settings", lang)
		}
		if got := fmt.Sprintf(ctx.Tr("fswd_space_crit"), "/v", 91.0, 1.5); got == translations["en"]["fswd_space_crit"] && lang != "en" {
			t.Errorf("the critical alert is still english with language %q:\n%s", lang, got)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  THROTTLE
// ═══════════════════════════════════════════════════════════════════

// TestCriticalAlertCooldownKeepsItsFloor pins the judgement that the critical
// branch repeats a recursive scan of the whole volume: intervals.
// critical_alert_cooldown_minutes is scaled by fsCriticalCooldownFactor and
// floored at fsCriticalCooldownFloor, so the default 30 min becomes 6h instead
// of 48 scans a day, and lowering the resource threshold cannot bring it back
// to minutes.
func TestCriticalAlertCooldownKeepsItsFloor(t *testing.T) {
	cases := []struct {
		baseMins int
		want     time.Duration
	}{
		{0, 6 * time.Hour},  // unset: the 30 min default, scaled
		{-5, 6 * time.Hour}, // nonsense value
		{1, 6 * time.Hour},  // floored
		{30, 6 * time.Hour},
		{60, 12 * time.Hour},
		{120, 24 * time.Hour},
	}
	for _, tc := range cases {
		if got := criticalAlertCooldown(tc.baseMins); got != tc.want {
			t.Errorf("criticalAlertCooldown(%d) = %s, want %s", tc.baseMins, got, tc.want)
		}
	}

	if fsCriticalCooldownFloor < time.Hour {
		t.Errorf("the floor is %s: too low for a full recursive scan", fsCriticalCooldownFloor)
	}
}

// ═══════════════════════════════════════════════════════════════════
//  PATH TRUNCATION
// ═══════════════════════════════════════════════════════════════════

func TestTruncatePath(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		maxLen int
		want   string
	}{
		{"short", "/a/b", 10, "/a/b"},
		{"twoParts", "/ab", 4, "/..."},
		{"long", "/a/b/c/d/e", 12, "...c/d/e"},
		{"maxZero", "/a/b", 0, ""},
		{"emptyPath", "", 5, ""},
		{"maxThreeAbs", "/abcdef", 3, "/.."},
		{"maxFourRel", "abcdef", 4, "...."},
		{"maxOneRel", "abcdef", 1, "."},
		{"maxTwoRel", "abcdef", 2, ".."},
		{"rootOne", "/", 1, "/"},
		{"rootTwo", "/", 2, "/."},
		{"rootFour", "/", 4, "/..."},
		{"exactLen", "/a/b/c", 6, "/a/b/c"},
		{"deepLong", "/a/b/c/d/e/f", 10, "...d/e/f"},
		{"deepTight", "/a/b/c/d/e/f", 8, "...d/e/f"},
		{"deepTighter", "/a/b/c/d/e/f", 7, "...d..."},

		// Non-ASCII. Cutting at maxLen bytes instead of maxLen runes splits a
		// multi-byte character, and Telegram then rejects the whole report
		// because one filename made the text invalid.
		{"utf8Long", "/media/dati/archivio/vecchio/foto", 12, "...archiv..."},
		{"utf8CJK", "/档案/文档/图片/2024/夏天", 10, "...图片/2..."},
		{"utf8Accents", "/données/photos/vacances/été", 14, "...photos/v..."},
		// The ellipsis eats the budget here: maxLen-3 leaves nothing of the
		// path. Ugly but valid, and unreachable in practice (the report asks
		// for 35). Pinned so a rewrite cannot silently change it.
		{"utf8Tiny", "/Ω/δ/λ/ξ", 6, "......"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncatePath(tc.path, tc.maxLen); got != tc.want {
				t.Fatalf("truncatePath(%q,%d) = %q, want %q", tc.path, tc.maxLen, got, tc.want)
			}
		})
	}
}

// TestTruncatePathNeverSplitsARune states the invariant directly, so a future
// rewrite of the ASCII cases above cannot pass by luck.
func TestTruncatePathNeverSplitsARune(t *testing.T) {
	paths := []string{
		"/档案/文档/图片/2024/夏天",
		"/données/photos/vacances/été",
		"/Ω/δ/λ/ξ",
		"/медиа/архив/фото",
		"/Volume1/日本語のフォルダ/長い名前",
	}
	for _, maxLen := range []int{0, 1, 4, 8, 12, 20, 35} {
		for _, p := range paths {
			got := truncatePath(p, maxLen)
			if !utf8.ValidString(got) {
				t.Errorf("truncatePath(%q,%d) = %q, which is not valid UTF-8", p, maxLen, got)
			}
			if n := utf8.RuneCountInString(got); maxLen > 4 && n > maxLen {
				t.Errorf("truncatePath(%q,%d) = %q: %d runes, over the budget", p, maxLen, got, n)
			}
		}
	}
}

func fsWatchdogLaneFor(t *testing.T, ctx *AppContext) watchdog {
	t.Helper()
	for _, wd := range watchdogsFor(ctx) {
		if wd.name == "fs-space" {
			return wd
		}
	}
	t.Fatal("no fs-space lane in the autonomous manager")
	return watchdog{}
}

// ═══════════════════════════════════════════════════════════════════
//  COST OF THE CRITICAL BRANCH
// ═══════════════════════════════════════════════════════════════════

// BenchmarkDeepScan measures the cost the critical cooldown is reasoning about.
//
// The critical branch does not just send a message: it walks every file under
// deep_scan_paths, one lstat per file, to build the top-N list. That is what
// fsCriticalCooldownFactor and fsCriticalCooldownFloor are there to throttle, so
// the number that justifies them has to come from a measurement rather than from
// taste.
//
// Run it with:
//
//	go test -run XXX -bench DeepScan -benchtime 1x ./internal/app/
//
// b.N files per directory and dirs directories, so the walk is wide like a
// media volume and not a handful of files in a deep tree. Files are empty: the
// scan reads no content, only metadata, which is exactly why the cost shows up
// as directory entries and not as bytes on the platters.
func BenchmarkDeepScan(b *testing.B) {
	const (
		dirs     = 200
		filesPer = 100
		// Sparse files: the scan only reads metadata, so a logical size with no
		// data blocks behind it keeps the tree cheap to create and leaves the
		// measurement about directory walking, which is what the scan does.
		fileBytes = 64 << 10
	)

	root := b.TempDir()
	for d := 0; d < dirs; d++ {
		sub := fmt.Sprintf("%s/dir%03d", root, d)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			b.Fatalf("mkdir %s: %v", sub, err)
		}
		for f := 0; f < filesPer; f++ {
			name := fmt.Sprintf("%s/file%03d", sub, f)
			fh, err := os.Create(name)
			if err != nil {
				b.Fatalf("create %s: %v", name, err)
			}
			if err := fh.Truncate(fileBytes); err != nil {
				b.Fatalf("truncate %s: %v", name, err)
			}
			if err := fh.Close(); err != nil {
				b.Fatalf("close %s: %v", name, err)
			}
		}
	}

	ctx := InitApp(&Config{FSWatchdog: model.FSWatchdogConfig{
		TopNFiles:       10,
		ExcludePatterns: []string{"/proc", "/sys"},
	}})

	w := GetFSWatchdog()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := w.DeepScan(ctx, []string{root})
		if res == nil {
			b.Fatal("DeepScan returned nil: a previous run is still marked as scanning")
		}
		if len(res.DirUsages) == 0 || res.DirUsages[0].Files != filesPer {
			b.Fatalf("the scan walked %d top directories holding %d files, want %d in each of %d directories",
				len(res.DirUsages), filesCount(res), filesPer, dirs)
		}
	}
	b.StopTimer()

	// Files per second is the number that transfers to a real volume: the scan
	// is one lstat per file, so the cost scales with the file count and not with
	// the bytes stored.
	if elapsed := b.Elapsed(); elapsed > 0 {
		files := float64(dirs * filesPer * b.N)
		b.ReportMetric(files/elapsed.Seconds(), "files/s")
		b.ReportMetric(elapsed.Seconds()/float64(b.N), "s/scan")
	}
}

// filesCount totals the per-directory file counts of a scan result.
func filesCount(res *DeepScanResult) int {
	n := 0
	for _, d := range res.DirUsages {
		n += d.Files
	}
	return n
}
