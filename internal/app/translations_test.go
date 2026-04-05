package app

import (
	"strings"
	"testing"
)

func TestTranslationsCompleteAndValid(t *testing.T) {
	// ensureTranslationCoverage populates the map at startup
	ensureTranslationCoverage()

	enMap, ok := translations["en"]
	if !ok {
		t.Fatal("English translations map missing")
	}

	for lang, langMap := range translations {
		// 1. Ensure fallback worked: every key in EN must exist in this language
		for enKey := range enMap {
			if _, exists := langMap[enKey]; !exists {
				t.Errorf("Language '%s' is missing key '%s'", lang, enKey)
			}
		}

		// 2. Format string validation: ensure % verbs match
		for key, enText := range enMap {
			langText := langMap[key]

			enVerbs := countFormattingVerbs(enText)
			langVerbs := countFormattingVerbs(langText)

			if enVerbs != langVerbs {
				t.Errorf("Translation format mismatch for language '%s', key '%s'. "+
					"EN requires %d verbs, but translation has %d. Text: '%s'",
					lang, key, enVerbs, langVerbs, langText)
			}
		}
	}
}

func countFormattingVerbs(s string) int {
	return strings.Count(s, "%s") + strings.Count(s, "%v") + strings.Count(s, "%.0f") + strings.Count(s, "%d") + strings.Count(s, "%2.0f") + strings.Count(s, "%02d")
}

func TestTranslateByLanguage(t *testing.T) {
	ensureTranslationCoverage()

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
