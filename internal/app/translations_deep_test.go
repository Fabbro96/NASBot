package app

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// reFormatVerb matches one Go formatting verb, flags, width and precision
// included: `%s`, `%d`, `%02d`, `%2.0f`, `%.1f`, `%v`. Capture groups are
// 1 = width, 2 = precision, 3 = verb.
var reFormatVerb = regexp.MustCompile(`%[+\-# 0]*(\*|\d+)?(\.\*|\.\d+)?([a-zA-Z%])`)

// reCodeSpan matches a Markdown code span. Its content is literal, so a `_` or a
// `*` inside it is not markup.
var reCodeSpan = regexp.MustCompile("`[^`]*`")

// stripCodeSpans removes Markdown code spans so the marker and verb scanners
// stop seeing literal text as markup.
//
// This is the right rule for markers: health_no_url contains "ping_url" inside a
// code span and counting it would report a third unpaired `_`. It is only half
// right for verbs, because a value that really reaches fmt is expanded before
// Telegram ever sees it: mon_cpu_crit is "🧠 CPU critical: `%.1f%%`" and that
// verb is live. That is why formattingVerbs takes a `raw` flag and
// TestTranslationsPlaceholderParity sets it from the call site. The only case
// where the distinction matters is top_header, "`PID   CPU%  MEM%  COMMAND`",
// which is written with strings.Builder and never passed to fmt.
func stripCodeSpans(s string) string {
	return reCodeSpan.ReplaceAllString(s, "")
}

// formattingVerbs returns the ordered list of formatting verbs in s, one entry
// per verb, as the single letter fmt actually dispatches on.
//
// Order matters and is the point of the check: comparing only len() let
// `sysinfo_cores` change `%d` into `%s` (fmt.Sprintf would then print
// %!s(float64=8)) and let two swapped `%d` swap "physical" and "logical"
// cores, both of which passed the old count-only test. Type matters for the
// same reason, and listing them fixes the second one. Flag, width and
// precision are deliberately dropped: `%2.0f` and `%.1f` are the same verb,
// and translations legitimately re-space a number for their locale.
//
// The old countFormattingVerbs is gone: two different regexes could not agree
// about what a verb is, and the weaker one (it did not even know `%.1f`, the
// most frequent format in the dictionary) was the one that ran.
//
// `raw` disables the code-span stripping. Use it for values that really reach
// fmt, where a verb inside a Markdown code span is still live: mon_cpu_crit is
// "🧠 CPU critical: `%.1f%%`".
func formattingVerbs(s string, raw bool) []string {
	if !raw {
		s = stripCodeSpans(s)
	}
	verbs := make([]string, 0, 4)
	for _, m := range reFormatVerb.FindAllStringSubmatch(s, -1) {
		// Groups: 1 = width, 2 = precision, 3 = verb.
		if m[3] == "%" {
			// "%%" is a literal percent sign, not a verb.
			continue
		}
		verbs = append(verbs, m[3])
	}
	return verbs
}

// TestTranslationsPlaceholderParity is the single, strict placeholder check for
// the whole dictionary: for every key, the ordered list of formatting verbs must
// be identical in all six languages.
//
// Keys that really reach fmt.Sprintf are scanned raw, because a verb inside a
// code span still gets interpreted. Keys that only reach a plain WriteString
// are scanned with code spans stripped, because top_header's "`CPU%  MEM%`" is
// a table header and not the verb "% M".
func TestTranslationsPlaceholderParity(t *testing.T) {
	raw := rawTranslations(t)
	en := raw["en"]
	sites := scanCallSites(t)

	mismatches := 0
	for _, lang := range localizationLangs {
		if lang == "en" {
			continue
		}
		dict := raw[lang]
		for key, enText := range en {
			langText, ok := dict[key]
			if !ok {
				// Reported by TestTranslations_TotalCoverage.
				continue
			}

			isFormat := sites[key].isFormat
			enVerbs := formattingVerbs(enText, isFormat)
			langVerbs := formattingVerbs(langText, isFormat)

			if !equalStrings(enVerbs, langVerbs) {
				t.Errorf("[%s] placeholder sequence mismatch for %q (format string: %v):\n"+
					"  EN (%d): %q -> %v\n"+
					"  %s (%d): %q -> %v",
					lang, key, isFormat, len(enVerbs), enText, enVerbs,
					strings.ToUpper(lang), len(langVerbs), langText, langVerbs)
				mismatches++
			}
		}
		t.Logf("[%s] placeholder mismatches so far: %d (over %d EN keys)", lang, mismatches, len(en))
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDeepTranslationAudit checks the direction that can silently ship a raw
// key to a Telegram message: a key passed to Tr/tr/Translate that the
// dictionary does not define in every language.
func TestDeepTranslationAudit(t *testing.T) {
	raw := rawTranslations(t)

	sites := scanCallSites(t)

	used := make([]string, 0, len(sites))
	for key, cs := range sites {
		if cs.used {
			used = append(used, key)
		}
	}
	sort.Strings(used)

	missingAny := 0
	for _, key := range used {
		for _, lang := range localizationLangs {
			if _, ok := raw[lang][key]; !ok {
				t.Errorf("KEY USED IN CODE BUT MISSING IN translations[%q]: %q", lang, key)
				missingAny++
			}
		}
	}
	t.Logf("Total unique keys used at Tr/tr/Translate call sites: %d, missing entries: %d",
		len(used), missingAny)

	// Guard against a vacuous audit. An earlier revision of the scanner put the
	// call-site branch behind a "is this node a BasicLit" early return, so no
	// key was ever marked used and this test reported zero missing entries while
	// checking nothing at all. Keys that are certain to be reached must be
	// found, and the total must be plausible.
	for _, key := range []string{"yes", "no", "cpu_fmt", "help_intro", "temp_disk_healthy", "net_title"} {
		if !sites[key].used {
			t.Errorf("scanner did not find key %q at any Tr/tr/Translate call site; "+
				"the call-site detection is broken, not the dictionary", key)
		}
	}
	if len(used) < 100 {
		t.Errorf("only %d keys found at Tr/tr/Translate call sites, which cannot be right "+
			"for a dictionary of %d keys: the scanner is broken", len(used), len(raw["en"]))
	}
}

// TestTranslationsCallSiteScanner sanity checks the format-string detection,
// which decides whether a key is scanned for verbs with Markdown code spans
// stripped or not.
func TestTranslationsCallSiteScanner(t *testing.T) {
	sites := scanCallSites(t)

	expect := map[string]bool{
		"mon_cpu_crit":  true,  // fmt.Sprintf(ctx.Tr("mon_cpu_crit"), s.CPU)
		"cpu_fmt":       true,  // fmt.Sprintf(tr("cpu_fmt"), bar, pct)
		"sysinfo_cores": true,  // fmt.Sprintf(tr("sysinfo_cores"), a, b)
		"help_intro":    false, // b.WriteString(tr("help_intro"))
		"top_header":    false, // b.WriteString(tr("top_header"))
	}
	for key, want := range expect {
		if got := sites[key].isFormat; got != want {
			t.Errorf("key %q: isFormat = %v, want %v", key, got, want)
		}
	}
}

// TestTranslationsNoRawKeyInHelpText is a regression guard for the class of bug
// that an unpaired Markdown marker produces: Telegram rejects the whole
// message, sendMarkdown falls back to plain text, and one bad key takes the
// formatting of the entire /help output with it.
func TestTranslationsNoRawKeyInHelpText(t *testing.T) {
	raw := rawTranslations(t)

	// Keys concatenated into getHelpText, in order. help_reports used to open
	// an underscore and never close it, which is what this guards.
	helpKeys := []string{
		"help_intro", "help_mon", "help_docker", "help_net", "help_settings",
		"help_reports", "help_every_days", "help_quiet",
	}
	for _, key := range helpKeys {
		for _, lang := range localizationLangs {
			text, ok := raw[lang][key]
			if !ok {
				t.Errorf("[%s] /help key %q not defined", lang, key)
				continue
			}
			for _, marker := range []string{"_", "*"} {
				if n := strings.Count(stripCodeSpans(text), marker); n%2 != 0 {
					t.Errorf("[%s] /help key %q has %d unpaired %q markers: %q",
						lang, key, n, marker, text)
				}
			}
		}
	}
}

// TestTranslationsFormatVerbScanners documents what formattingVerbs must catch.
// It is the proof that the unified scanner sees `%.1f` (which
// countFormattingVerbs did not) and that it respects order.
func TestTranslationsFormatVerbScanners(t *testing.T) {
	cases := []struct {
		name string
		in   string
		raw  bool
		want []string
	}{
		{name: "percent one", in: "CPU at %.1f%%", want: []string{"f"}},
		{name: "width and precision", in: "CPU %2.0f%", want: []string{"f"}},
		{name: "padded int", in: "at %02d:%02d", want: []string{"d", "d"}},
		{name: "s and d", in: "name %s has %d cores", want: []string{"s", "d"}},
		{name: "no verb", in: "plain text", want: nil},
		// Not a format string: a table header. "CPU%" reads as "% M".
		{name: "code span is literal", in: "`PID   CPU%  MEM%  COMMAND`", want: nil},
		// Same text, but it really is a format string: the verb is live and
		// dropping it would print the raw %.1f to the user.
		{name: "verb inside code span of a format string", in: "CPU critical: `%.1f%%`", raw: true, want: []string{"f"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formattingVerbs(tc.in, tc.raw)
			if !equalStrings(got, tc.want) {
				t.Errorf("formattingVerbs(%q, raw=%v) = %v, want %v", tc.in, tc.raw, got, tc.want)
			}
		})
	}

	// Order is part of the contract: the same two verbs in a different order give a
	// different list, and TestTranslationsPlaceholderParity compares those
	// lists, so "name %s has %d" cannot become "name %d has %s" in one language.
	//
	// Swapping two *identical* verbs is deliberately not checked: fmt would
	// render both without complaint, so no format-string inspection can see it.
	// That is a translation-reading bug, not a formatting one.
	if equalStrings(formattingVerbs("%s has %d cores", false), formattingVerbs("%d has %s cores", false)) {
		t.Errorf("formattingVerbs cannot distinguish verb order")
	}

	// Type is part of the contract too.
	if equalStrings(formattingVerbs("%d", false), formattingVerbs("%s", false)) {
		t.Errorf("formattingVerbs cannot distinguish verb type")
	}
}
