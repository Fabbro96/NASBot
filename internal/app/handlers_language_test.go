package app

import "testing"

func TestParseLanguageCallbackData(t *testing.T) {
	// Every supported language, in both callback shapes:
	// "set_lang_<code>" and "set_lang_<code>_settings".
	//
	// This used to be a hand-written table covering en, es, de, zh and pt, so
	// `set_lang_it` and `set_lang_uk_settings` — that is, Italian and Ukrainian
	// — were never exercised at all. Generating the matrix means a regression
	// that broke either of them is caught.
	for _, lang := range localizationLangs {
		for _, tc := range []struct {
			data         string
			wantSettings bool
		}{
			{data: "set_lang_" + lang, wantSettings: false},
			{data: "set_lang_" + lang + "_settings", wantSettings: true},
		} {
			t.Run(tc.data, func(t *testing.T) {
				gotLang, fromSettings, ok := parseLanguageCallbackData(tc.data)
				if !ok {
					t.Fatalf("parseLanguageCallbackData(%q) rejected a supported language", tc.data)
				}
				if gotLang != lang {
					t.Errorf("parseLanguageCallbackData(%q) language = %q, want %q", tc.data, gotLang, lang)
				}
				if fromSettings != tc.wantSettings {
					t.Errorf("parseLanguageCallbackData(%q) fromSettings = %v, want %v",
						tc.data, fromSettings, tc.wantSettings)
				}
			})
		}
	}
}

// TestParseLanguageCallbackDataRejects keeps the negative cases explicit: they
// are the reason the positive matrix above means anything.
func TestParseLanguageCallbackDataRejects(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		// pt is not a supported language: English, Italian, Spanish, German,
		// Chinese and Ukrainian are.
		{name: "unsupported language", data: "set_lang_pt"},
		{name: "unsupported language with settings suffix", data: "set_lang_pt_settings"},
		{name: "empty", data: ""},
		{name: "empty language", data: "set_lang_"},
		{name: "unknown prefix", data: "settings_change_lang"},
		{name: "unknown language", data: "set_lang_xx"},
		{name: "junk suffix", data: "set_lang_en_bogus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lang, fromSettings, ok := parseLanguageCallbackData(tt.data)
			if ok {
				t.Errorf("parseLanguageCallbackData(%q) accepted a callback it should reject (lang=%q, settings=%v)",
					tt.data, lang, fromSettings)
			}
		})
	}
}

// TestEverySupportedLanguageHasCallbackData ties the parser test to the
// dictionary: if a language is added to the dictionary and to
// localizationLangs, it must also be routable from the inline keyboard.
func TestEverySupportedLanguageHasCallbackData(t *testing.T) {
	for _, lang := range localizationLangs {
		if got, _, ok := parseLanguageCallbackData("set_lang_" + lang); !ok || got != lang {
			t.Errorf("language %q cannot be selected from a callback: (lang=%q, ok=%v)", lang, got, ok)
		}
	}
}
