package main

import "testing"

func TestParseLanguageCallbackData(t *testing.T) {
	tests := []struct {
		data         string
		wantLang     string
		wantSettings bool
		wantOK       bool
	}{
		{data: "set_lang_en", wantLang: "en", wantSettings: false, wantOK: true},
		{data: "set_lang_es", wantLang: "es", wantSettings: false, wantOK: true},
		{data: "set_lang_de_settings", wantLang: "de", wantSettings: true, wantOK: true},
		{data: "set_lang_zh_settings", wantLang: "zh", wantSettings: true, wantOK: true},
		{data: "set_lang_pt", wantOK: false},
		{data: "settings_change_lang", wantOK: false},
	}

	for _, tt := range tests {
		lang, fromSettings, ok := parseLanguageCallbackData(tt.data)
		if ok != tt.wantOK || lang != tt.wantLang || fromSettings != tt.wantSettings {
			t.Fatalf("parseLanguageCallbackData(%q) = (%q,%v,%v), want (%q,%v,%v)",
				tt.data, lang, fromSettings, ok, tt.wantLang, tt.wantSettings, tt.wantOK)
		}
	}
}
