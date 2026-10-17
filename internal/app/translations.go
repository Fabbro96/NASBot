package app

// translations contains all translatable strings, one dictionary per language
// (see translations_<lang>.go).
var translations = map[string]map[string]string{
	"en": translationsEN,
	"it": translationsIT,
	"es": translationsES,
	"de": translationsDE,
	"zh": translationsZH,
	"uk": translationsUK,
}
