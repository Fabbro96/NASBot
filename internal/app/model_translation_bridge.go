package app

import pmodel "nasbot/pkg/model"

func init() {
	pmodel.Translate = translateByLanguage
}

func translateByLanguage(lang, key string) string {
	if lang == "" {
		lang = "en"
	}
	t, ok := translations[lang]
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
