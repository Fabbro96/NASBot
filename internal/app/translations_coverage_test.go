package app

import "testing"

func TestTranslations_TotalCoverage(t *testing.T) {
	ensureTranslationCoverage()
	en := translations["en"]
	if len(en) == 0 {
		t.Fatalf("english translations missing")
	}

	totalKeys := len(en)
	allPassed := true

	for lang, dict := range translations {
		if lang == "en" {
			continue
		}

		missingKeys := 0
		for key := range en {
			if _, ok := dict[key]; !ok {
				missingKeys++
				t.Logf("Language '%s' missing key: %s", lang, key)
			}
		}

		presentKeys := totalKeys - missingKeys
		percentage := float64(presentKeys) / float64(totalKeys) * 100.0

		t.Logf("Coverage for %s: %.1f%% (%d/%d keys)", lang, percentage, presentKeys, totalKeys)

		if percentage < 90.0 {
			t.Errorf("Coverage for %s is below 90%%", lang)
			allPassed = false
		}
	}

	if !allPassed {
		t.Fatalf("Translation coverage check failed. Some languages are missing too many keys.")
	}
}
