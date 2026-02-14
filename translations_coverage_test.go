package main

import "testing"

func TestTranslations_TotalCoverage(t *testing.T) {
	ensureTranslationCoverage()
	en := translations["en"]
	if len(en) == 0 {
		t.Fatalf("english translations missing")
	}

	for lang, dict := range translations {
		if lang == "en" {
			continue
		}
		for key := range en {
			if _, ok := dict[key]; !ok {
				t.Fatalf("language %s missing key %s", lang, key)
			}
		}
	}
}
