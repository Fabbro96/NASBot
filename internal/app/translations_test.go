package app

import (
	"strings"
	"testing"
)

// TestTranslationsCompleteAndValid validates the raw dictionary: every language
// must carry every English key, and no value may contain a Go escape that was
// meant to be interpreted.
//
// It reads the source file, not the in-memory map, and it never calls
// ensureTranslationCoverage() — that function used to run from an init() and
// fill every hole with the English value before this test could look, which is
// why deleting `docker_restart_all_fail` and `help_every_days` from the `it`
// block used to leave the suite green.
func TestTranslationsCompleteAndValid(t *testing.T) {
	raw := rawTranslations(t)

	enMap, ok := raw["en"]
	if !ok {
		t.Fatal("English translations map missing")
	}

	for _, lang := range localizationLangs {
		langMap, ok := raw[lang]
		if !ok {
			t.Errorf("Language '%s' is missing entirely", lang)
			continue
		}

		// 1. Every key in EN must exist in this language.
		for enKey := range enMap {
			if _, exists := langMap[enKey]; !exists {
				t.Errorf("Language '%s' is missing key '%s'", lang, enKey)
			}
		}

		// 2. No double-escaped newlines or quotes.
		for key, langText := range langMap {
			if strings.Contains(langText, `\n`) {
				t.Errorf("Language '%s', key '%s' contains literal '\\n' instead of actual newline: %q", lang, key, langText)
			}
			if strings.Contains(langText, `\"`) {
				t.Errorf("Language '%s', key '%s' contains literal '\\\"' instead of actual quote: %q", lang, key, langText)
			}
		}
	}
}

// TestTranslationsMarkerPairsBalanced fails when a key has an odd number of
// `_`, `*` or backtick markers.
//
// This is the check that was missing when help_reports opened an underscore and
// never closed it in any of the six languages: with Telegram's legacy Markdown
// parse mode the API rejects the message, sendMarkdown silently falls back to
// plain text, and the formatting of the entire /help output is lost. With quiet
// hours enabled the dangling `_` also pairs up with the one in help_quiet and
// italicises half the message.
//
// Markers inside a Markdown code span are literal, so they are counted on the
// raw string for backticks and on the code-span-stripped string for `_` and `*`
// (health_no_url contains "ping_url" inside a code span; counting it as markup
// would be a false positive).
func TestTranslationsMarkerPairsBalanced(t *testing.T) {
	raw := rawTranslations(t)

	for _, lang := range localizationLangs {
		dict, ok := raw[lang]
		if !ok {
			t.Errorf("language %q is missing entirely", lang)
			continue
		}
		for key, text := range dict {
			stripped := stripCodeSpans(text)
			for _, marker := range []string{"_", "*"} {
				if n := strings.Count(stripped, marker); n%2 != 0 {
					t.Errorf("[%s] key %q has %d unpaired %q markers: %q",
						lang, key, n, marker, text)
				}
			}
			if n := strings.Count(text, "`"); n%2 != 0 {
				t.Errorf("[%s] key %q has %d unpaired backticks: %q", lang, key, n, text)
			}
		}
	}
}

func TestTranslateByLanguage(t *testing.T) {
	testCases := []struct {
		desc     string
		lang     string
		key      string
		expected string
	}{
		{
			desc:     "existing key en",
			lang:     "en",
			key:      "yes",
			expected: "✅ Yes",
		},
		{
			desc:     "existing key it",
			lang:     "it",
			key:      "yes",
			expected: "✅ Sì",
		},
		{
			desc:     "existing key uk",
			lang:     "uk",
			key:      "yes",
			expected: rawTranslations(t)["uk"]["yes"],
		},
		{
			desc:     "empty language falls back to en",
			lang:     "",
			key:      "yes",
			expected: "✅ Yes",
		},
		{
			desc:     "missing language fallback to en",
			lang:     "xx",
			key:      "yes",
			expected: "✅ Yes",
		},
		{
			desc:     "missing key fallback to missing string",
			lang:     "en",
			key:      "nonexistent_key_xyz",
			expected: "nonexistent_key_xyz",
		},
	}

	for _, tC := range testCases {
		t.Run(tC.desc, func(t *testing.T) {
			got := translateByLanguage(tC.lang, tC.key)
			if got != tC.expected {
				t.Errorf("translateByLanguage(%q, %q) = %q; want %q", tC.lang, tC.key, got, tC.expected)
			}
		})
	}
}

// TestTranslateByLanguageDoesNotMutate pins the reason the runtime filler was
// deleted: resolving an unknown language must not write anything into the
// dictionaries, otherwise a hole in `it` would be papered over at first use and
// the source-of-truth tests would no longer describe the running program.
func TestTranslateByLanguageDoesNotMutate(t *testing.T) {
	before := make(map[string]int, len(translations))
	for lang, dict := range translations {
		before[lang] = len(dict)
	}

	translateByLanguage("pt", "this_key_does_not_exist")
	translateByLanguage("", "this_key_does_not_exist")
	tr("this_key_does_not_exist")

	for lang, dict := range translations {
		if len(dict) != before[lang] {
			t.Errorf("dictionary %q grew from %d to %d keys during a lookup", lang, before[lang], len(dict))
		}
	}
}

// TestTrShimFallsBackToEnglish documents the standing defect of tr(): the
// standalone watchdog process has no AppContext and therefore no user language,
// so the six call sites in fs_watchdog.go always render in English. The shim
// exists only so that tree keeps compiling while those call sites move to
// ctx.Tr; when the last one goes, this test and the function go together.
func TestTranslationTrShimFallsBackToEnglish(t *testing.T) {
	raw := rawTranslations(t)

	if got, want := tr("yes"), raw["en"]["yes"]; got != want {
		t.Errorf("tr(\"yes\") = %q, want the english value %q", got, want)
	}
	if got, want := tr("nonexistent_key_xyz"), "nonexistent_key_xyz"; got != want {
		t.Errorf("tr(\"nonexistent_key_xyz\") = %q, want %q", got, want)
	}
}
