package main

var currentLanguage = "en"

func init() {
	ensureTranslationCoverage()
}

func ensureTranslationCoverage() {
	en, ok := translations["en"]
	if !ok {
		return
	}
	for lang, langMap := range translations {
		if lang == "en" {
			continue
		}
		for key, value := range en {
			if _, exists := langMap[key]; !exists {
				langMap[key] = value
			}
		}
	}
}

// tr returns the translated string for the given key
func tr(key string) string {
	if currentLanguage == "" {
		currentLanguage = "en"
	}
	t, ok := translations[currentLanguage]
	if !ok {
		t = translations["en"]
	}
	if v, ok := t[key]; ok {
		return v
	}
	if tEn, ok := translations["en"]; ok {
		if v, ok := tEn[key]; ok {
			return v
		}
	}
	return key
}
