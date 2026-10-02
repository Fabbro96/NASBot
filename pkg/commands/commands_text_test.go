package commands

import (
	"fmt"
	"strings"
	"testing"

	"nasbot/pkg/model"
)

// enFormatTemplates holds the "en" dictionary value of every key that reaches
// trf() from the generators under test, copied verbatim from
// internal/app/translations.go.
//
// The values must be the real templates, not a placeholder: trf refuses to render
// a template whose verb count does not match the number of arguments, so a stub
// returning "[key]" turned every formatted line into
// "⚠️ [fmt:cpu_fmt] template has 0 verb(s) for 2 argument(s)". The old tests
// survived that only by accident: "0G" was matching inside the
// "%!(EXTRA float64=50, string=0G)" tail of a broken Sprintf, so they asserted
// on fmt's diagnostic dump rather than on the rendered message.
//
// pkg/commands cannot import internal/app (that would be an import cycle: the
// app package builds this package's registries), so the templates are duplicated
// here on purpose. TestTextGeneratorsRejectFormatMismatch keeps the duplication
// honest: it fails the moment production and this table disagree.
var enFormatTemplates = map[string]string{
	// /status
	"status_title": "🖥 *NAS* at %s",
	"cpu_fmt":      "🧠 CPU  %s %2.0f%%",
	"ram_fmt":      "💾 RAM  %s %2.0f%%",
	"swap_fmt":     "🔄 Swap %s %2.0f%%",
	"ssd_fmt":      "💿 SSD %2.0f%% · %s free",
	"disk_sec_fmt": "🗄 %s %2.0f%% · %s free",
	"disk_io_fmt":  "📡 Disk I/O at %.0f%%",
	"disk_rw_fmt":  " (R %.0f / W %.0f MB/s)",
	"uptime_fmt":   "_⏱ Running for %s_",
	// /ping
	"ping_pong":       "%s *Pong!*",
	"ping_uptime":     "🤖 Bot uptime: `%s`",
	"ping_collecting": "🖥 Collecting stats: %v",
	"ping_last_check": "📡 Last check: `%s`",
	// /help
	"help_every_days": "_(every %d days)_",
	"help_quiet":      "_🌙 Quiet: %02d:%02d — %02d:%02d_",
	// /logsearch, /diskpred and /net, exercised by commands_contract_test.go.
	"diskpred_datapoints":    "_Data points: %d/12_",
	"logsearch_title":        "🔍 *Log Search*: `%s` in `%s`\n\n",
	"logsearch_found_fmt":    "Found %d matches (showing last %d):\n\n",
	"logsearch_no_matches":   "🔍 No matches for `%s` in `%s` logs",
	"net_public_unavailable": "🌐 Public IP: unavailable (%v)",
	// /cmd, exercised by cmd_shell_test.go.
	"cmd_parse_error": "🚫 Cannot read the command line: %s",
	"cmd_not_allowed": "🚫 `%s` is not in the allowlist.\n\nAllowed here: %s",
	// /backup. Not reached by the generators below: cmd_backup_test.go asserts on
	// their rendered text, which turns into the mismatch warning the day the verbs
	// drift, so the same guard applies there.
	"backup_create_err":           "❌ Error creating backup: %v",
	"backup_send_err":             "❌ Error sending backup: %v",
	"backup_other_target_refused": "❌ Refused: only User ID %d may change the backup destination, not %d.",
}

// textGeneratorFormatKeys are the keys TestTextGeneratorsRejectFormatMismatch
// requires to be exercised by getStatusText, GetPingText and GetHelpText.
var textGeneratorFormatKeys = []string{
	"status_title", "cpu_fmt", "ram_fmt", "swap_fmt", "ssd_fmt", "disk_sec_fmt",
	"disk_io_fmt", "disk_rw_fmt", "uptime_fmt",
	"ping_pong", "ping_uptime", "ping_collecting", "ping_last_check",
	"help_every_days", "help_quiet",
}

// fmtMismatchMarker is what trf substitutes for a template whose verbs do not
// match its arguments. Its presence in a generated message means the dictionary
// and the call site disagree, i.e. the user would read the warning instead of
// the status.
const fmtMismatchMarker = "⚠️ [fmt:"

// installEnTranslator installs a resolver built from enFormatTemplates and
// restores the previous one when the test ends.
//
// model.Translate is a package-level variable shared by every test in the
// process: without t.Cleanup a test that installed a stub leaks it into all the
// tests that run after it, and the failure surfaces somewhere else entirely.
func installEnTranslator(t *testing.T) {
	t.Helper()

	prev := model.Translate
	model.Translate = func(_ string, key string) string {
		if tmpl, ok := enFormatTemplates[key]; ok {
			return tmpl
		}
		// Keys that only go through a plain tr() carry no verb and are asserted
		// by name, so echoing the key is the useful answer for them.
		return "[" + key + "]"
	}
	t.Cleanup(func() { model.Translate = prev })
}

func setupTestContext(t *testing.T) *model.AppContext {
	t.Helper()

	cfg := &model.Config{
		Paths: model.PathsConfig{
			SSD: "/",
		},
	}
	ctx := model.InitApp(cfg)

	installEnTranslator(t)

	return ctx
}

// assertNoFormatMismatch fails when a generated message carries trf's
// placeholder-for-a-mismatch warning.
func assertNoFormatMismatch(t *testing.T, what, text string) {
	t.Helper()

	if idx := strings.Index(text, fmtMismatchMarker); idx >= 0 {
		end := strings.IndexByte(text[idx:], '\n')
		if end < 0 {
			end = len(text)
		} else {
			end += idx
		}
		t.Errorf("%s rendered a template/argument mismatch instead of the message:\n%s",
			what, text[idx:end])
	}
}

func TestGetStatusText(t *testing.T) {
	ctx := setupTestContext(t)

	// Fill with some dummy stats to avoid loading defaults inside tests
	ctx.Stats.Set(model.Stats{
		CPU:    42.5,
		RAM:    60.0,
		Swap:   10.0,
		VolSSD: model.VolumeStats{Used: 50.0},
		SecondaryVols: map[string]model.VolumeStats{
			"/mnt/data": {Used: 30.0},
		},
		Uptime: 123456,
	})

	text := GetStatusText(ctx)

	// The header is a format string, so it is asserted on its rendered shape:
	// the clock is the only variable in it.
	if !strings.Contains(text, "🖥 *NAS* at ") {
		t.Errorf("Expected the rendered status title, got text: \n%s", text)
	}
	if !strings.Contains(text, "42") { // CPU
		t.Errorf("Expected CPU usage, got text: \n%s", text)
	}
	if !strings.Contains(text, "0G") { // SSD free space as formatted
		t.Errorf("Expected SSD free space, got text: \n%s", text)
	}
	if strings.Contains(text, `\n`) {
		t.Errorf("Status text contains literal '\\n': %s", text)
	}
	if strings.Contains(text, "\n\n\n") {
		t.Errorf("Status text contains redundant triple newlines: %q", text)
	}
	assertNoFormatMismatch(t, "GetStatusText", text)
}

// TestGetStatusTextRendersEverySection pins the four composed sections
// separately. "42" and "0G" alone cannot tell a complete message from a title
// line: the percentages, the formatted free space and the uptime each come from a
// different trf call, so a section that silently stopped being built would leave
// every other assertion green.
func TestGetStatusTextRendersEverySection(t *testing.T) {
	ctx := setupTestContext(t)
	ctx.Stats.Set(model.Stats{
		CPU:    42.5,
		RAM:    60.0,
		Swap:   10.0,
		VolSSD: model.VolumeStats{Used: 50.0, Free: 3 * (1 << 30)},
		SecondaryVols: map[string]model.VolumeStats{
			"/mnt/data": {Used: 30.0, Free: 2 * (1 << 30)},
		},
		Uptime: 123456,
	})

	text := GetStatusText(ctx)

	// Swap above swapVisiblePct must produce the third compute line.
	for _, want := range []string{
		"🖥 *NAS* at ", // header, from status_title
		"🧠 CPU  ",     // compute
		"💾 RAM  ",     // compute
		"🔄 Swap ",     // compute, only above the visibility threshold
		"💿 SSD 50% · 3G free",
		"🗄 data 30% · 2G free",
		"_⏱ Running for 1d10h_", // 123456s = 1 day 10 h
	} {
		if !strings.Contains(text, want) {
			t.Errorf("status text is missing %q, got:\n%s", want, text)
		}
	}
	assertNoFormatMismatch(t, "GetStatusText", text)
}

// TestGetStatusTextWithoutStats is the cold-start branch: with no sample yet the
// generator must answer the "loading" string and nothing else, not a message
// whose CPU and RAM are zero.
func TestGetStatusTextWithoutStats(t *testing.T) {
	ctx := setupTestContext(t)

	text := GetStatusText(ctx)

	if text != "[loading]" {
		t.Fatalf("expected the loading placeholder, got %q", text)
	}
}

func TestGetHelpText(t *testing.T) {
	appCtx := setupTestContext(t)

	text := GetHelpText(appCtx)

	if !strings.Contains(text, "[help_intro]") {
		t.Errorf("Expected help intro, got: %s", text)
	}
	if !strings.Contains(text, "/docker") {
		t.Errorf("Expected docker command in help, got: %s", text)
	}
	if !strings.Contains(text, "[cmd_status_desc]") {
		t.Errorf("Expected status command description key, got: %s", text)
	}
	if !strings.Contains(text, "[cmd_docker_desc]") {
		t.Errorf("Expected docker command description key, got: %s", text)
	}
	if !strings.Contains(text, "[cmd_settings_desc]") {
		t.Errorf("Expected settings command description key, got: %s", text)
	}
	assertNoFormatMismatch(t, "GetHelpText", text)
}

// TestGetHelpTextShowsScheduleAndQuietHours covers the two conditional footer
// lines. Both are format strings, so this is where a template gaining or losing
// a verb would show up first.
func TestGetHelpTextShowsScheduleAndQuietHours(t *testing.T) {
	ctx := setupTestContext(t)
	ctx.Settings.SetReportsSettings(true, 3, []model.TimePoint{{Hour: 8, Minute: 5}})
	ctx.Settings.SetQuietHours(model.QuietSettings{
		Enabled: true,
		Start:   model.TimePoint{Hour: 23, Minute: 30},
		End:     model.TimePoint{Hour: 7, Minute: 15},
	})

	text := GetHelpText(ctx)

	for _, want := range []string{
		"[help_reports]",
		"08:05 ",
		"_(every 3 days)_",
		"_🌙 Quiet: 23:30 — 07:15_",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("help text is missing %q, got:\n%s", want, text)
		}
	}
	assertNoFormatMismatch(t, "GetHelpText", text)
}

func TestGetPingText(t *testing.T) {
	ctx := setupTestContext(t)
	ctx.Stats.Set(model.Stats{CPU: 1})

	text := GetPingText(ctx)

	if !strings.Contains(text, "✅ *Pong!*") {
		t.Errorf("Expected ping_pong rendered with the ready glyph, got: %s", text)
	}
	if !strings.Contains(text, "[ping_ok]") {
		t.Errorf("Expected the ready status line, got: %s", text)
	}
	if strings.Contains(text, "[ping_pong]") {
		t.Errorf("ping_pong went through a plain tr() instead of trf(): %s", text)
	}
	assertNoFormatMismatch(t, "GetPingText", text)
}

// TestGetPingTextNotReady covers the other branch: with no sample yet the status
// is the warning glyph and the "stats not ready" line.
func TestGetPingTextNotReady(t *testing.T) {
	ctx := setupTestContext(t)

	text := GetPingText(ctx)

	if !strings.Contains(text, "⚠️") {
		t.Errorf("Expected the not-ready glyph, got: %s", text)
	}
	if !strings.Contains(text, "[ping_not_ready]") {
		t.Errorf("Expected ping_not_ready, got: %s", text)
	}
	assertNoFormatMismatch(t, "GetPingText", text)
}

// TestTextGeneratorsRejectFormatMismatch is the reason enFormatTemplates is
// allowed to be a copy.
//
// It re-implements trf's decision with fmt and compares it against the rendered
// messages: for every generator, no template in the test dictionary may disagree
// with the number of arguments production passes. A production edit that adds an
// argument, renames a key or changes a verb count fails here with the offending
// key named, instead of silently degrading into a warning the user reads.
func TestTextGeneratorsRejectFormatMismatch(t *testing.T) {
	seen := map[string]int{}

	trfProbe := func(key string, args ...any) {
		tmpl, ok := enFormatTemplates[key]
		if !ok {
			t.Errorf("trf(\"%s\", …) is used by a generator but the key is not in "+
				"enFormatTemplates: add the real \"en\" template from "+
				"internal/app/translations.go", key)
			return
		}
		seen[key] = len(args)
		if got := countVerbs(tmpl); got != len(args) {
			t.Errorf("template %q has %d verb(s) but production passes %d argument(s): %q",
				key, got, len(args), tmpl)
		}
		// fmt is the real judge of the verb *types* too: a %s in the table where
		// production passes a float64 renders as %!s(float64=…) and still passes
		// a count-only check.
		if rendered := fmt.Sprintf(tmpl, args...); strings.Contains(rendered, "%!") {
			t.Errorf("template %q does not match the argument types: %q", key, rendered)
		}
	}

	ctx := setupTestContext(t)
	ctx.Stats.Set(model.Stats{
		CPU:           42.5,
		RAM:           60.0,
		Swap:          10.0,
		VolSSD:        model.VolumeStats{Used: 50.0, Free: 1 << 30},
		Uptime:        123456,
		DiskUtil:      90, // above diskIOVisiblePct, so the I/O lines are built too
		ReadMBs:       4,
		WriteMBs:      5,
		SecondaryVols: map[string]model.VolumeStats{"/mnt/data": {Used: 30.0}},
	})

	// The argument lists below are the ones getStatusText and getPingText pass,
	// written out independently of the generators so a change in the generator
	// shows up as a template/argument disagreement.
	trfProbe("status_title", "12:34")
	trfProbe("cpu_fmt", "█████░░░░░", 42.5)
	trfProbe("ram_fmt", "██████░░░░", 60.0)
	trfProbe("swap_fmt", "█░░░░░░░░░░", 10.0)
	trfProbe("ssd_fmt", 50.0, "1G")
	trfProbe("disk_sec_fmt", "data", 30.0, "1G")
	trfProbe("disk_io_fmt", 90.0)
	trfProbe("disk_rw_fmt", 4.0, 5.0)
	trfProbe("uptime_fmt", "1d10h")
	trfProbe("ping_pong", "✅")
	trfProbe("ping_uptime", "10m")
	trfProbe("ping_collecting", true)
	trfProbe("ping_last_check", "12:34:56")
	trfProbe("help_every_days", 3)
	trfProbe("help_quiet", 23, 30, 7, 15)

	// The generators must actually exercise them: a template nobody reaches is a
	// copy that can rot without anything noticing. The report schedule and the
	// quiet window have to be on for the two /help format keys to be built.
	GetStatusText(ctx)
	GetPingText(ctx)
	ctx.Settings.SetReportsSettings(true, 3, []model.TimePoint{{Hour: 8, Minute: 5}})
	ctx.Settings.SetQuietHours(model.QuietSettings{
		Enabled: true,
		Start:   model.TimePoint{Hour: 23, Minute: 30},
		End:     model.TimePoint{Hour: 7, Minute: 15},
	})
	GetHelpText(ctx)

	for _, key := range textGeneratorFormatKeys {
		if _, ok := seen[key]; !ok {
			t.Errorf("textGeneratorFormatKeys lists %q but the probe never reached it", key)
		}
	}
	if len(seen) != len(textGeneratorFormatKeys) {
		t.Errorf("the probe covered %d keys, textGeneratorFormatKeys lists %d: "+
			"the two lists have drifted apart", len(seen), len(textGeneratorFormatKeys))
	}
}
