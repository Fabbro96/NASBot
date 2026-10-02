package app

import pmodel "nasbot/pkg/model"

// Translate è una variabile di funzione dichiarata in pkg/model con un
// default che restituisce la chiave. pkg/model e pkg/commands non importano
// internal/app (sarebbe un ciclo), quindi il bootstrap va fatto da qui: questo
// init() è l'unico punto in cui il resolver reale entra in pkg/model.
func init() {
	pmodel.Translate = translateByLanguage
}

// translateByLanguage è l'unico resolver del dizionario per chiave + lingua.
//
// Ordine di risoluzione, da non cambiare:
//
//	lingua vuota   -> "en"
//	lingua ignota  -> dizionario "en"
//	chiave assente -> dizionario "en"
//	chiave assente ovunque -> la chiave stessa
//
// L'ultimo caso è l'unico che può stampare una chiave grezza in un messaggio
// Telegram: è anche l'unico difetto che TestDeepTranslationAudit può vedere,
// perché il dizionario in memoria non viene più completato a runtime (vedi
// translations_runtime.go: non c'è più ensureTranslationCoverage).
func translateByLanguage(lang, key string) string {
	if lang == "" {
		lang = "en"
	}
	if v, ok := translations[lang][key]; ok {
		return v
	}
	if v, ok := translations["en"][key]; ok {
		return v
	}
	return key
}
