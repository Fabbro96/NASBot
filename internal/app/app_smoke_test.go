package app

import (
	"strings"
	"testing"
	"time"
)

// fmtMismatchMarker is what pkg/commands' trf substitutes for a template whose
// verbs do not match the arguments it was given. Seeing it in a generated
// message means the dictionary and the call site disagree and the user reads the
// warning instead of the report.
const fmtMismatchMarker = "⚠️ [fmt:"

// assertNoFormatMismatch fails when a generated message carries that warning.
//
// This is the invariant the old smoke test could not check: it asserted only
// that getStatusText contained the substring "NAS", which the title line alone
// provides, so every other section could stop being built and the test stayed
// green. Each section is built by a separate trf call, so one mismatch anywhere
// in the message is visible here.
func assertNoFormatMismatch(t *testing.T, what, text string) {
	t.Helper()

	idx := strings.Index(text, fmtMismatchMarker)
	if idx < 0 {
		return
	}
	end := strings.IndexByte(text[idx:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += idx
	}
	t.Errorf("%s rendered a template/argument mismatch instead of the message:\n%s",
		what, text[idx:end])
}

// newTextGeneratorContext builds the context the text generators need: stats
// marked ready, a populated trend history and a container cache inside its TTL.
func newTextGeneratorContext() *AppContext {
	giB := uint64(1024 * 1024 * 1024)
	return &AppContext{
		Stats: &ThreadSafeStats{Data: Stats{
			CPU:    12,
			RAM:    34,
			Swap:   0,
			Uptime: 3600,
			VolSSD: VolumeStats{Used: 10, Free: 50 * giB},
			SecondaryVols: map[string]VolumeStats{
				"/mnt/data": {Used: 20, Free: 100 * giB},
			},
		}, Ready: true},
		State: &RuntimeState{
			TimeLocation: time.UTC,
			DiskHistory: []DiskUsagePoint{
				{Time: time.Now().Add(-2 * time.Hour), SSDFree: 100 * giB, SecondaryFree: map[string]uint64{"/mnt/data": 200 * giB}},
				{Time: time.Now(), SSDFree: 90 * giB, SecondaryFree: map[string]uint64{"/mnt/data": 190 * giB}},
			},
		},
		Settings: &UserSettings{Language: "en"},
		Bot:      &BotContext{StartTime: time.Now().Add(-1 * time.Hour)},
		Monitor: &MonitorState{
			CPUTrend: []TrendPoint{},
			RAMTrend: []TrendPoint{},
		},
		Docker: &DockerManager{
			Cache: DockerCache{Containers: []ContainerInfo{{Name: "x", Running: true}}, LastUpdate: time.Now()},
		},
		Config: &Config{Cache: CacheConfig{DockerTTLSeconds: 60}},
	}
}

// TestTextGeneratorsSmoke asserts one rendered fragment per section instead of
// one substring for the whole message.
//
// What it covers: with the real dictionary installed, every generator must render
// its formatted lines. A section that silently stopped being composed, a template
// that lost a verb, or an argument that was dropped all leave a specific fragment
// missing while the title-only check the old test used stayed green.
func TestTextGeneratorsSmoke(t *testing.T) {
	ctx := newTextGeneratorContext()

	status := getStatusText(ctx)
	for _, want := range []string{
		"*NAS* at ",        // header
		"CPU",              // compute
		"RAM",              // compute
		"SSD",              // storage
		"data",             // secondary disk, from the short mount name
		"Running for 1h0m", // uptime footer, 3600s
		"🐳",                // container summary
	} {
		if !strings.Contains(status, want) {
			t.Errorf("getStatusText is missing %q, got:\n%s", want, status)
		}
	}
	if strings.Contains(status, `\n`) {
		t.Errorf("getStatusText contains a literal backslash-n: %q", status)
	}
	assertNoFormatMismatch(t, "getStatusText", status)

	quick := getQuickText(ctx)
	// The quick line is one sentence: health emoji, CPU, RAM, SSD, the secondary
	// disk, the container count and the watchdog semaphores. Asserting only that
	// it is non-empty (as the old smoke test did) accepts a bare "⏳".
	for _, want := range []string{
		"✅",        // health, everything under the thresholds
		"CPU 12%",  //
		"RAM 34%",  //
		"SSD 10%",  //
		"data 20%", // secondary volume, short name
		"🐳",        // container count
		"WD K🟢 N🟢", // watchdog semaphores, both healthy
	} {
		if !strings.Contains(quick, want) {
			t.Errorf("getQuickText is missing %q, got:\n%s", want, quick)
		}
	}

	pred := getDiskPredictionText(ctx)
	// Two samples: below the 12-point threshold, so the generator must say it is
	// still collecting and report how many points it has. The old assertion
	// ("Disk Space") only proved the title was rendered.
	if !strings.Contains(pred, "*Disk Space Prediction*") {
		t.Errorf("getDiskPredictionText is missing its title, got:\n%s", pred)
	}
	if !strings.Contains(pred, "need at least 1 hour") {
		t.Errorf("getDiskPredictionText must report that it is still collecting, got:\n%s", pred)
	}
	if !strings.Contains(pred, "Data points: 2/12") {
		t.Errorf("getDiskPredictionText must report the point count with the number "+
			"filled in, got:\n%s", pred)
	}
	assertNoFormatMismatch(t, "getDiskPredictionText", pred)
}

// TestTextGeneratorsColdStart covers the branch every generator takes before the
// first sample arrives: they must not invent a status out of zero values. The
// quick line answers the hourglass and the disk prediction says it is collecting,
// while /status shows the loading placeholder.
func TestTextGeneratorsColdStart(t *testing.T) {
	ctx := newTextGeneratorContext()
	ctx.Stats = &ThreadSafeStats{} // nothing collected yet
	ctx.State.DiskHistory = nil

	if got := getQuickText(ctx); strings.TrimSpace(got) != "⏳" {
		t.Errorf("getQuickText without stats = %q, want the hourglass alone", got)
	}

	pred := getDiskPredictionText(ctx)
	if !strings.Contains(pred, "need at least 1 hour") {
		t.Errorf("getDiskPredictionText without history = %q", pred)
	}
	if !strings.Contains(pred, "Data points: 0/12") {
		t.Errorf("getDiskPredictionText must report zero points, got:\n%s", pred)
	}
}

// TestTextGeneratorsAreLanguageDriven: the same context rendered in Italian must
// differ from the English one and must still be free of format mismatches.
//
// The failure this catches is silent and expensive: a key missing from one
// language leaves the raw key in a Telegram message, and a template whose verbs
// drifted from the call site replaces a whole report line with a warning.
func TestTextGeneratorsAreLanguageDriven(t *testing.T) {
	ctx := newTextGeneratorContext()

	english := getStatusText(ctx)

	ctx.Settings.SetLanguage("it")
	italian := getStatusText(ctx)

	if english == italian {
		t.Fatalf("getStatusText returned the English text for an Italian context:\n%s", english)
	}
	if !strings.Contains(italian, "*NAS* alle ") {
		t.Errorf("expected the Italian header in:\n%s", italian)
	}
	assertNoFormatMismatch(t, "getStatusText (it)", italian)

	// The language must not change which values are reported, only how they are
	// worded: a template resolved to the wrong language would still print numbers,
	// and a lost argument would drop one.
	for _, want := range []string{"12%", "34%", "10%", "1h0m"} {
		if !strings.Contains(italian, want) {
			t.Errorf("the Italian status lost the value %q:\n%s", want, italian)
		}
	}
}
