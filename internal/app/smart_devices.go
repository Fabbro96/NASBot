package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// smartSysfsRoot è la directory di sysfs da cui rilevare i dischi.
//
// È una variabile di package e non una costante deliberatamente: i test la
// puntano a un tmpdir (con t.Cleanup per rimetterla a posto), perché i dischi
// della macchina che esegue i test non devono mai finire dentro una fixture.
var smartSysfsRoot = "/sys/block"

// smartBootSyncAtLoad abilita la sync di boot dentro loadConfig.
//
// È false quando il codice gira sotto `go test`, perché runtime_main.go
// chiama loadConfig() dall'init() anche nel binario di test: lì il
// config.json della root del repo (o quello di NASBOT_CONFIG) verrebbe letto
// con /sys/block reale e riscritto con i dischi della macchina che esegue i
// test, un file che nessuno ha chiesto di toccare. testing.Testing() è
// deciso dal linker (imposta "1" su testing.testBinary) ed è quindi già
// valido quando l'init() parte, non solo dentro i singoli test.
//
// I test che esercitano proprio la sync di boot la riaccendono con
// enableBootSyncForTest (smart_devices_test.go).
var smartBootSyncAtLoad = !testing.Testing()

// smartExcludedPrefixes sono i nomi che mai indicano un disco su cui abbia
// senso girare smartctl: loop e ram/zram sono pseudo-dischi, dm- è device
// mapper, sr è il cdrom, fd il floppy, md il software raid, nbd il network
// block device e pmem la memoria persistente. Nessuno di questi espone
// attributi SMART, e metterli nella lista significa un ciclo di smartctl ogni
// 10 minuti che fallisce sempre.
var smartExcludedPrefixes = []string{"loop", "ram", "zram", "dm-", "sr", "fd", "md", "nbd", "pmem"}

// smartROMType è il valore di /sys/block/<dev>/device/type che identifica un
// device TYPE_ROM (cdrom). Alcuni driver espongono il disco ottico con un nome
// che i prefissi qui sopra non coprono, per questo il tipo viene controllato a
// parte: un cdrom senza attributi SMART darebbe un allarme "FAIL" inventato.
const smartROMType = "5"

// detectSmartDevices elenca, ordinati, i dischi fisici presenti sotto
// sysfsRoot. Legge solo sysfs: nessun processo esterno, nessun sudo.
//
// Ritorna nil (con una warning) quando la root non è leggibile o quando nessun
// disco sopravvive ai filtri. I due casi restano indistinguibili per chi la
// usa, ed è voluto: "non riesco a leggere" e "non c'è nessun disco" devono
// entrambi tradursi in "non aggiornare la lista configurata", perché azzerare
// i dispositivi di un NAS a causa di un errore di lettura spegnerebbe il
// monitoraggio SMART proprio quando serve.
func detectSmartDevices(sysfsRoot string) []string {
	entries, err := os.ReadDir(sysfsRoot)
	if err != nil {
		slog.Warn("SMART device detection skipped, sysfs root unreadable",
			"root", sysfsRoot, "err", err)
		return nil
	}

	detected := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if hasAnyPrefix(name, smartExcludedPrefixes) {
			continue
		}
		devicePath := filepath.Join(sysfsRoot, name, "device")
		if _, err := os.Stat(devicePath); err != nil {
			// Senza <name>/device il nome compare in /sys/block ma non punta a
			// un device raggiungibile: è il caso di zram su alcuni kernel.
			continue
		}
		if typeBytes, err := os.ReadFile(filepath.Join(devicePath, "type")); err == nil &&
			strings.TrimSpace(string(typeBytes)) == smartROMType {
			continue
		}
		detected = append(detected, name)
	}

	if len(detected) == 0 {
		slog.Warn("SMART device detection found no disk", "root", sysfsRoot)
		return nil
	}
	slices.Sort(detected)
	return detected
}

// hasAnyPrefix riporta se s comincia con uno dei prefissi dati.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// sortedNormalizedNameList normalizza (trim, dedup, niente vuoti) e ordina una
// lista di dispositivi. È la forma su cui si confrontano le liste, così che
// l'ordine in cui sono state scritte a mano in config.json non produca mai una
// riscrittura inutile: l'ordine non è informazione, è solo churn.
func sortedNormalizedNameList(items []string) []string {
	normalized := slices.Clone(normalizeStringList(items))
	slices.Sort(normalized)
	return normalized
}

// smartDevicesEqual confronta due liste di dispositivi come insiemi, dopo
// normalizzazione e ordinamento.
func smartDevicesEqual(a, b []string) bool {
	return slices.Equal(sortedNormalizedNameList(a), sortedNormalizedNameList(b))
}

// syncSMARTDevicesAtBoot allinea notifications.smart.devices con i dischi
// realmente presenti, una volta all'avvio.
//
// Ritorna le voci da aggiungere alla lista delle correzioni di loadConfig: una
// lista cambiata è una cosa che è cambiata, quindi fa parte del "qualcosa è
// cambiato" che decide se riscrivere il file.
//
// Detection vuota o fallita = nessuna azione: la lista configurata resta quella
// che ha scritto l'operatore, mai azzerata.
func syncSMARTDevicesAtBoot(c *Config) []string {
	if c == nil || !c.Notifications.SMART.Enabled {
		return nil
	}
	detected := detectSmartDevices(smartSysfsRoot)
	if len(detected) == 0 {
		return nil
	}
	if smartDevicesEqual(detected, c.Notifications.SMART.Devices) {
		return nil
	}
	c.Notifications.SMART.Devices = detected
	return []string{fmt.Sprintf("notifications.smart.devices -> %v", detected)}
}

// syncSMARTDevicesPeriodic risincronizza la lista durante il funzionamento
// (hotplug): un disco montato o staccato a runtime viene registrato al prossimo
// refresh della cache SMART, cioè al massimo 10 minuti dopo, che è il
// rate-limit implicito di questa sync.
//
// Passa per applyConfigPatch, lo stesso percorso della UI di /configset: la
// chiave scritta è quindi una chiave che la UI può già scrivere, con la stessa
// sanitizzazione e lo stesso pubblicazione della snapshot.
func syncSMARTDevicesPeriodic(ctx *AppContext) {
	if ctx == nil {
		return
	}
	// Una sola lettura della snapshot: tra il controllo di Enabled e il
	// confronto con la lista configurata un reload potrebbe pubblicare una
	// configurazione diversa, e le due letture vedrebbero mondi diversi.
	cfg := ctx.Cfg()
	if cfg == nil || !cfg.Notifications.SMART.Enabled {
		return
	}
	detected := detectSmartDevices(smartSysfsRoot)
	if len(detected) == 0 {
		return
	}
	if smartDevicesEqual(detected, cfg.Notifications.SMART.Devices) {
		return
	}

	// Nessun config su disco: publishDefaultConfig non crea il file per
	// scelta, e la sync non deve farlo al suo posto. Senza questo controllo
	// ogni refresh loggherebbe un errore ENOENT su un installazione che gira
	// volutamente sui default.
	path, err := resolveConfigPath()
	if err != nil {
		slog.Warn("SMART device sync skipped, invalid config path", "err", err)
		return
	}
	if _, err := os.Stat(path); err != nil {
		return
	}

	patch := map[string]interface{}{
		"notifications": map[string]interface{}{
			"smart": map[string]interface{}{
				"devices": detected,
			},
		},
	}
	result, err := applyConfigPatch(patch)
	if err != nil {
		slog.Warn("SMART device sync failed", "devices", detected, "err", err)
		return
	}
	// Una chiave rifiutata non è una sincronizzazione riuscita: notifications
	// .smart.devices è una chiave che la UI può già scrivere, quindi un
	// Ignored qui significa che qualcosa nella struttura della config è
	// cambiato e il file NON contiene i dispositivi rilevati. Loggare il
	// successo in questo caso direbbe all'operatore il contrario del vero.
	if len(result.Ignored) > 0 {
		slog.Warn("SMART device sync refused", "fields", result.Ignored, "devices", detected)
		return
	}
	slog.Info("SMART device list synchronized", "devices", detected)
}
