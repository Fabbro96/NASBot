package app

// tr risolve una chiave di traduzione senza contesto utente.
//
// Stato: nessun chiamante in produzione. Le sei occorrenze che esistevano in
// fs_watchdog.go (fswd_space_warn, fswd_space_crit, fswd_deepscan_title,
// fswd_largest_dirs, fswd_largest_files, fswd_disk_status_title) sono passate a
// ctx.Tr quando il filesystem watchdog è diventato una lane del bot, e da lì la
// lingua esce dai settings invece di cadere su "en".
//
// Restano solo due riferimenti, entrambi in translations_test.go:
// TestTrShimFallsBackToEnglish e la riga dentro
// TestTranslateByLanguageDoesNotMutate. Quel file non è di chi ha fatto il
// cambio, quindi questa funzione non è stata cancellata: toglierla romperebbe
// la compilazione dei test. La mossa che chiude la faccenda è cancellare,
// insieme, translations_runtime.go e quei due riferimenti.
//
// Finché resta, non usarla: translateByLanguage("", key) restituisce sempre
// l'inglese, quindi un nuovo caller qui produrrebbe un messaggio nella lingua
// sbagliata. Con un *AppContext a portata di mano la risposta è ctx.Tr(key).
func tr(key string) string {
	return translateByLanguage("", key)
}
