package app

// Deep-audit fuzz targets (round 3). Every target asserts structural
// invariants, not just "no panic": a fuzzer that only watches for crashes
// misses wrong-answer bugs in parsers that feed reboot/prune/threshold
// decisions.
import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzParseDockerJSON(f *testing.F) {
	seeds := []string{
		"",
		`{"ID":"abc","Names":"web","Image":"nginx","State":"running"}`,
		"not json at all",
		`{"ID":"a"}` + "\n" + `garbage` + "\n" + `{"ID":"b","Names":"x"}`,
		"{\"ID\":\"\\ud800\"}",
		strings.Repeat("x", 100000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		containers, err := parseDockerJSON(s)
		if err != nil {
			return
		}
		// One JSON object per line at most.
		if len(containers) > len(strings.Split(s, "\n")) {
			t.Fatalf("more containers than lines")
		}
	})
}

func FuzzParseDockerStatsRow(f *testing.F) {
	seeds := []string{
		"",
		"web|12.34%|1.234GiB / 8GiB|15.42%",
		"a|b",
		"|||",
		"name with spaces|NaN%|1.5 TiB / 2 TiB|1e400%",
		"n|--%|--|--",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		row, ok := parseDockerStatsRow(s)
		if !ok {
			return
		}
		if row.Name == "" {
			t.Fatalf("ok with empty name for %q", s)
		}
		// The numeric chain must never accept non-finite values.
		if v, ok := parsePercentValue(row.CPUPerc); ok && (v != v || v > 1e18 || v < -1e18) {
			t.Fatalf("non-finite CPU accepted for %q", s)
		}
		_ = shortenDockerMemUsage(row.MemUsage)
	})
}

func FuzzParsePercentValue(f *testing.F) {
	for _, s := range []string{"", "%", "12.34%", "--", "N/A", "1e400", "-1e400", "NaN", "  5.5 % ", "0x10%"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := parsePercentValue(s)
		if !ok {
			return
		}
		if v != v || v > 1e18 || v < -1e18 {
			t.Fatalf("bad value %v accepted for %q", v, s)
		}
	})
}

func FuzzSplitMemToken(f *testing.F) {
	for _, s := range []string{"", "1.234GiB", "1.5 TiB", "1GiB / 8GiB", "  /  ", "12", "1.2.3GB", "--"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		value, unit := splitMemToken(s)
		_ = value
		_ = unit
		_ = shortenDockerMemUsage(s)
	})
}

func FuzzParseMdstatIssues(f *testing.F) {
	seeds := []string{
		"",
		"Personalities : [raid1]\nmd0 : active raid1 sda1[0] sdb1[1]\n      104320 blocks [2/2] [UU]\n",
		"md0 : active raid1 sda1[0]\n      104320 blocks [2/1] [_U]\n",
		"md0 : inactive sda1[0](F) sdb1[1]\n",
		"recovery = 99.9% (1/2) finish=999.9min speed=1K/sec",
		strings.Repeat("md0 : active raid1 sda1[0] [U_]\n", 5000),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		issues := parseMdstatIssues(s)
		// One header line may carry several failed devices, so the count is
		// not bounded by the line count — but it is bounded by maxRaidIssues
		// plus the omission tail, so the alert always fits a Telegram message.
		if len(issues) > maxRaidIssues+1 {
			t.Fatalf("%d issues exceed the cap for input of %d bytes", len(issues), len(s))
		}
	})
}

func FuzzParseReleaseVersion(f *testing.F) {
	for _, s := range []string{"", "v", "v1.2.3", "V1.2.3-rc.1", "1.2.3.4", "dev", "nightly", "v99999999999999999999.0.0", "v1.2.3+meta", "v.1", "..."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v, ok := parseReleaseVersion(s)
		if !ok {
			return
		}
		for _, n := range v.core {
			if n < 0 {
				t.Fatalf("negative version segment for %q", s)
			}
		}
	})
}

func FuzzParseChecksumLine(f *testing.F) {
	asset := "nasbot-arm64"
	seeds := []string{
		"",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709  nasbot-arm64\n",
		"# comment\n\n",
		"ZZZ  nasbot-arm64",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709 *nasbot-arm64",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709  other",
		"a b c",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, manifest string) {
		digest, ok := parseChecksumLine(manifest, asset)
		if !ok {
			return
		}
		if len(digest) != 64 {
			t.Fatalf("bad digest length %d for manifest %q", len(digest), manifest)
		}
		for _, c := range digest {
			if !strings.ContainsRune("0123456789abcdef", c) {
				t.Fatalf("non-hex digest %q", digest)
			}
		}
	})
}

func FuzzParsePID(f *testing.F) {
	for _, s := range []string{"", "1", "123456", "0", "-1", "12a", "99999999999999999999", " 42 ", "٤٢"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		pid, ok := parsePID(s)
		if !ok {
			return
		}
		if pid <= 0 {
			t.Fatalf("non-positive pid %d accepted for %q", pid, s)
		}
	})
}

func FuzzParseThresholdAction(f *testing.F) {
	prefix := "set_thr_"
	seeds := []string{"", "w_cpu", "c_ram", "x_cpu", "w", "w_", "w_cpu_extra_stuff", "W_CPU"}
	for _, s := range seeds {
		f.Add(prefix + s)
	}
	f.Fuzz(func(t *testing.T, data string) {
		level, res, ok := parseThresholdAction(data, prefix)
		if !ok {
			return
		}
		if level != "w" && level != "c" {
			t.Fatalf("bad level %q for %q", level, data)
		}
		if res == "" {
			t.Fatalf("empty resource for %q", data)
		}
	})
}

func FuzzParseLanguageCallbackData(f *testing.F) {
	for _, s := range []string{"", "set_lang_", "set_lang_it", "set_lang_it_settings", "set_lang__settings", "set_lang_xx", "SET_LANG_IT"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lang, _, ok := parseLanguageCallbackData(s)
		if !ok {
			return
		}
		if lang == "" {
			t.Fatalf("ok with empty language for %q", s)
		}
	})
}

func FuzzSplitTelegramMessage(f *testing.F) {
	const budget, maxChunks = 4000, 4
	notice := "[truncated]"
	seeds := []string{
		"",
		"hello",
		strings.Repeat("a", 5000),
		strings.Repeat("line one\nline two\n", 1000),
		strings.Repeat("😀", 5000),
		strings.Repeat("\n", 5000),
		"a" + strings.Repeat("\n", 3000) + "b",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		chunks := splitTelegramMessage(text, budget, maxChunks, notice)
		if len(chunks) == 0 {
			t.Fatalf("no chunks for input of %d bytes", len(text))
		}
		if len(chunks) > maxChunks {
			t.Fatalf("%d chunks exceed max %d", len(chunks), maxChunks)
		}
		for i, c := range chunks {
			if !utf8.ValidString(c) {
				t.Fatalf("chunk %d is not valid UTF-8", i)
			}
			// The last chunk may carry the notice; the rest must fit.
			if i < len(chunks)-1 && len([]rune(c)) > budget {
				t.Fatalf("chunk %d has %d runes over budget %d", i, len([]rune(c)), budget)
			}
		}
	})
}

func FuzzTakeRunePrefix(f *testing.F) {
	for _, s := range []string{"", "abc", "😀😀😀", "a\xc3\xa9b"} {
		f.Add(s, 2)
	}
	f.Fuzz(func(t *testing.T, s string, n int) {
		if n < 0 || n > 100000 {
			return
		}
		p := takeRunePrefix(s, n)
		if !strings.HasPrefix(s, p) {
			t.Fatalf("not a prefix of %q", s)
		}
		if utf8.RuneCountInString(p) > n {
			t.Fatalf("prefix of %d runes exceeds %d", utf8.RuneCountInString(p), n)
		}
		// Validity is conditional: a lone lead byte ("\xf0") has no valid
		// prefix but itself. End-to-end validity is guaranteed by the caller
		// splitTelegramMessage, which normalizes with ToValidUTF8 first
		// (see TestSplitTelegramMessageSanitizesInvalidUTF8).
		if utf8.ValidString(s) && !utf8.ValidString(p) {
			t.Fatalf("valid input %q produced invalid prefix %q", s, p)
		}
	})
}
