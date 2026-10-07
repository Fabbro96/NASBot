package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// sysfsFixture descrive un device finto da creare sotto la root di sysfs di un
// test.
type sysfsFixture struct {
	name string
	// deviceDir false = manca <name>/device, come su zram di alcuni kernel.
	deviceDir bool
	// deviceType è il contenuto di device/type; vuoto = file type assente
	// (è il caso di nvme0n1, che ha device ma non type).
	deviceType string
}

// makeFakeSysfs costruisce una root di sysfs finta e ne ritorna il percorso.
// Le directory vengono create nell'ordine dei fixture, così un test che pretende
// l'ordinamento non può passare per caso se l'elenco arrivasse già ordinato da
// ReadDir.
func makeFakeSysfs(t *testing.T, fixtures ...sysfsFixture) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range fixtures {
		if !f.deviceDir {
			if err := os.Mkdir(filepath.Join(root, f.name), 0o755); err != nil {
				t.Fatalf("create %s: %v", f.name, err)
			}
			continue
		}
		deviceDir := filepath.Join(root, f.name, "device")
		if err := os.MkdirAll(deviceDir, 0o755); err != nil {
			t.Fatalf("create %s/device: %v", f.name, err)
		}
		if f.deviceType != "" {
			if err := os.WriteFile(filepath.Join(deviceDir, "type"), []byte(f.deviceType+"\n"), 0o644); err != nil {
				t.Fatalf("write %s/device/type: %v", f.name, err)
			}
		}
	}
	return root
}

// fullDiskSysfs è la root usata dai test di sync: i tre dischi, nell'ordine in
// cui li restituisce un detection riuscito.
func fullDiskSysfs(t *testing.T) string {
	t.Helper()
	return makeFakeSysfs(t,
		sysfsFixture{name: "sdb", deviceDir: true, deviceType: "0"},
		sysfsFixture{name: "nvme0n1", deviceDir: true},
		sysfsFixture{name: "sda", deviceDir: true, deviceType: "0"},
	)
}

// pinConfigPathAndSysfs punta configFile e smartSysfsRoot ai valori dati e li
// ripristina alla fine del test.
//
// NASBOT_CONFIG viene azzerato: resolveConfigPath lo preferisce a configFile, e
// un ambiente che lo impostasse farebbe leggere al test un file diverso da
// quello che ha appena scritto.
func pinConfigPathAndSysfs(t *testing.T, configPath, sysfsRoot string) {
	t.Helper()
	t.Setenv("NASBOT_CONFIG", "")

	oldConfigFile := configFile
	oldSysfsRoot := smartSysfsRoot
	configFile = configPath
	smartSysfsRoot = sysfsRoot
	t.Cleanup(func() {
		configFile = oldConfigFile
		smartSysfsRoot = oldSysfsRoot
	})
}

// enableBootSyncForTest riaccende la sync di boot di loadConfig per la durata
// del test.
//
// È spenta sotto go test (smartBootSyncAtLoad): l'init() di runtime_main.go
// chiama loadConfig() anche nel binario di test e, con /sys/block reale, la
// sync riscriverebbe i dischi dell'host dentro il config.json della root del
// repo. I test che esercitano proprio quella sync la riaccendono qui.
func enableBootSyncForTest(t *testing.T) {
	t.Helper()
	previous := smartBootSyncAtLoad
	smartBootSyncAtLoad = true
	t.Cleanup(func() { smartBootSyncAtLoad = previous })
}

// writeCompleteConfig scrive un config.json completo: nessun default mancante e
// nessuna correzione di sanitizzazione prodotta da defaultConfigTemplate. Una
// riscrittura del file dopo loadConfig è quindi attribuibile soltanto alla sync
// SMART, e il caso "nessuna sync" si può riconoscere dall'assenza del .bak che
// writeConfigFile crea prima di ogni riscrittura.
func writeCompleteConfig(t *testing.T, devices []string) string {
	t.Helper()
	base := defaultConfigTemplate()
	base.BotToken = "123456:TEST-TOKEN"
	base.AllowedUserID = 42
	// UTC e non Europe/Rome: LoadLocation("UTC") non legge il disco, così il
	// test non dipende da tzdata.
	base.Timezone = "UTC"
	base.Notifications.SMART.Devices = devices

	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// readConfigDevices legge notifications.smart.devices dal file.
func readConfigDevices(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	return c.Notifications.SMART.Devices
}

// assertNoRewrite verifica che il file di fixture non sia stato toccato: il
// contenuto è identico byte per byte e il .bak che writeConfigFile crea prima
// di ogni riscrittura non esiste.
func assertNoRewrite(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("config file was rewritten:\nbefore: %s\nafter:  %s", before, after)
	}
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Fatalf("config file was rewritten (unexpected %s.bak)", path)
	}
}

func TestDetectSmartDevices_IncludesDisksAndFiltersNoise(t *testing.T) {
	// Ordine di creazione volutamente sparso: l'output deve comunque essere
	// quello ordinato.
	root := makeFakeSysfs(t,
		sysfsFixture{name: "sdb", deviceDir: true, deviceType: "0"},   // incluso
		sysfsFixture{name: "loop0", deviceDir: true, deviceType: "0"}, // prefisso
		sysfsFixture{name: "nvme0n1", deviceDir: true},                // device senza type: incluso
		sysfsFixture{name: "zram0"},                                   // prefisso e senza device
		sysfsFixture{name: "sda", deviceDir: true, deviceType: "0"},   // incluso
		sysfsFixture{name: "dm-0", deviceDir: true, deviceType: "0"},  // prefisso
		sysfsFixture{name: "ram0", deviceDir: true, deviceType: "0"},  // prefisso
		sysfsFixture{name: "sr0", deviceDir: true, deviceType: "0"},   // prefisso
		sysfsFixture{name: "fd0", deviceDir: true, deviceType: "0"},   // prefisso
		sysfsFixture{name: "md0", deviceDir: true, deviceType: "0"},   // prefisso
		sysfsFixture{name: "nbd0", deviceDir: true, deviceType: "0"},  // prefisso
		sysfsFixture{name: "pmem0", deviceDir: true, deviceType: "0"}, // prefisso
		// TYPE_ROM con un nome che i prefissi non coprono: escluso dal tipo.
		sysfsFixture{name: "cdrom0", deviceDir: true, deviceType: "5"},
		// Nessun <name>/device: escluso per il device mancante.
		sysfsFixture{name: "nodev0"},
	)

	got := detectSmartDevices(root)
	want := []string{"nvme0n1", "sda", "sdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("detectSmartDevices = %v, want %v", got, want)
	}
}

func TestDetectSmartDevices_MissingRoot(t *testing.T) {
	if got := detectSmartDevices(filepath.Join(t.TempDir(), "does-not-exist")); got != nil {
		t.Fatalf("missing root must yield nil, got %v", got)
	}
}

func TestDetectSmartDevices_EmptyRoot(t *testing.T) {
	if got := detectSmartDevices(t.TempDir()); got != nil {
		t.Fatalf("empty root must yield nil, got %v", got)
	}
}

func TestLoadConfigSyncSMARTDevices_Add(t *testing.T) {
	path := writeCompleteConfig(t, nil)
	pinConfigPathAndSysfs(t, path, fullDiskSysfs(t))
	enableBootSyncForTest(t)

	loadConfig()

	got := readConfigDevices(t, path)
	want := []string{"nvme0n1", "sda", "sdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("boot sync did not add the detected disks: got %v, want %v", got, want)
	}
	// Il .bak esiste solo se writeConfigFile è passato di qui: la scrittura
	// deve essere avvenuta davvero, non solo essere rimasta in memoria.
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("expected the config to be rewritten: %v", err)
	}
}

func TestLoadConfigSyncSMARTDevices_Remove(t *testing.T) {
	// La lista configurata include un disco che il sistema non ha più.
	path := writeCompleteConfig(t, []string{"sda", "sdb"})
	root := makeFakeSysfs(t, sysfsFixture{name: "sda", deviceDir: true, deviceType: "0"})
	pinConfigPathAndSysfs(t, path, root)
	enableBootSyncForTest(t)

	loadConfig()

	got := readConfigDevices(t, path)
	if !slices.Equal(got, []string{"sda"}) {
		t.Fatalf("boot sync did not drop the unplugged disk: got %v, want [sda]", got)
	}
}

func TestLoadConfigSyncSMARTDevices_NoopDoesNotRewrite(t *testing.T) {
	// Lista già allineata ma in ordine diverso: il confronto è sulle liste
	// normalizzate e ordinate, quindi nessuna riscrittura e nessun churn.
	path := writeCompleteConfig(t, []string{"sdb", "sda", "nvme0n1"})
	pinConfigPathAndSysfs(t, path, fullDiskSysfs(t))
	enableBootSyncForTest(t)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	loadConfig()

	assertNoRewrite(t, path, before)
	if got := readConfigDevices(t, path); !slices.Equal(got, []string{"sdb", "sda", "nvme0n1"}) {
		t.Fatalf("no-op sync changed the configured order: got %v", got)
	}
}

func TestLoadConfigSyncSMARTDevices_EmptyDetectionKeepsList(t *testing.T) {
	// Detection vuota (root esistente ma senza dischi): la lista scritta
	// dall'operatore deve restare intatta, mai azzerata.
	path := writeCompleteConfig(t, []string{"sda"})
	pinConfigPathAndSysfs(t, path, t.TempDir())
	enableBootSyncForTest(t)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	loadConfig()

	assertNoRewrite(t, path, before)
	if got := readConfigDevices(t, path); !slices.Equal(got, []string{"sda"}) {
		t.Fatalf("empty detection wiped the configured list: got %v", got)
	}
}

// fakeSMARTctlOutput è la risposta del runner finto a `sudo -n smartctl`.
//
// mockRunner risponde uguale a `smartctl -A` e a `smartctl -H`, quindi
// l'uscita deve essere plausibile per entrambi i parse: la riga di attributo
// con la temperatura in RAW_VALUE (campo 10, come nella uscita reale) e la
// riga di esito di -H. L'intestazione reale NON c'è di proposito: contiene
// WHEN_FAILED, che il parse di -H leggerebbe come "disco rotto" e farebbe
// fallire il test con un allarme inventato.
const fakeSMARTctlOutput = "  5 Temperature_Celsius     0x0032   35   40    0    -       Always       -        35\n" +
	"SMART overall-health self-assessment test result: PASSED\n"

// TestSMARTMonitorCheck_PeriodicSyncWiring copre l'unico legame runtime fra
// SMARTMonitor.Check e syncSMARTDevicesPeriodic: la chiamata che rigenera la
// cache è il solo punto in cui la lista di config.json viene riallineata ai
// dischi plugati a runtime, e nessun altro test la esercita.
//
// Il test fissa la root di sysfs su una fixture, invecchia la data
// dell'ultimo controllo di oltre 10 minuti (ramo needsCheck) e sostituisce il
// runner di comando, perché readDiskSMART esegue `sudo smartctl` e il
// tripwire della suite rifiuta sudo: senza il fake il test toccherebbe
// l'host e la suite fallirebbe a fine run.
func TestSMARTMonitorCheck_PeriodicSyncWiring(t *testing.T) {
	// Il file di fixture parte con "sda", che la root finta non ha: se la
	// sync gira davvero, il file deve finire con i due dischi rilevati.
	path := writeCompleteConfig(t, []string{"sda"})
	root := makeFakeSysfs(t,
		sysfsFixture{name: "sdb", deviceDir: true, deviceType: "0"},
		sysfsFixture{name: "nvme0n1", deviceDir: true},
	)
	pinConfigPathAndSysfs(t, path, root)

	restoreRunner := setCommandRunner(mockRunner{exists: true, out: []byte(fakeSMARTctlOutput)})
	t.Cleanup(restoreRunner)

	// app va puntato al contesto del test: publishConfig della sync
	// pubblica su app (come nel bot vero), così il ciclo di lettura della
	// cache vede la lista appena riscritta invece di quella vecchia.
	previousApp := app
	app = newSMARTSyncContext([]string{"sda"})
	t.Cleanup(func() { app = previousApp })
	ctx := app

	// Oltre i 10 minuti di rate-limit: senza questo il ramo else copia la
	// cache vecchia e la sync non viene mai chiamata.
	ctx.Monitor.SmartLastCheckTime = time.Now().Add(-11 * time.Minute)

	if alerts := (&SMARTMonitor{}).Check(ctx, &Stats{}); len(alerts) != 0 {
		t.Fatalf("healthy canned SMART data must not alert, got %v", alerts)
	}

	// Il config.json di fixture cambia con la lista rilevata.
	got := readConfigDevices(t, path)
	want := []string{"nvme0n1", "sdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("Check did not sync the config file: got %v, want %v", got, want)
	}

	// Stesso ciclo: la sync gira PRIMA della rigenerazione, quindi la cache
	// contiene già i dischi rilevati e non la vecchia lista "sda".
	ctx.Monitor.Mu.Lock()
	cacheLen := len(ctx.Monitor.SmartCache)
	_, hasNVMe := ctx.Monitor.SmartCache["nvme0n1"]
	_, hasSDB := ctx.Monitor.SmartCache["sdb"]
	_, hasSDA := ctx.Monitor.SmartCache["sda"]
	lastCheck := ctx.Monitor.SmartLastCheckTime
	ctx.Monitor.Mu.Unlock()

	if cacheLen != 2 || !hasNVMe || !hasSDB || hasSDA {
		t.Fatalf("cache not rebuilt from the synced list: len=%d nvme0n1=%v sdb=%v sda=%v",
			cacheLen, hasNVMe, hasSDB, hasSDA)
	}
	if age := time.Since(lastCheck); age > time.Minute {
		t.Fatalf("SmartLastCheckTime not refreshed by Check: %v old", age)
	}
}

// newSMARTSyncContext costruisce un contesto con SMART attivo e la lista data.
func newSMARTSyncContext(devices []string) *AppContext {
	ctx := newTestAppContext()
	ctx.Config.Notifications.SMART = SmartConfig{Enabled: true, Devices: devices}
	return ctx
}

func TestSyncSMARTDevicesPeriodic_UpdatesConfigFile(t *testing.T) {
	path := writeCompleteConfig(t, []string{"sda"})
	pinConfigPathAndSysfs(t, path, makeFakeSysfs(t,
		sysfsFixture{name: "sdb", deviceDir: true, deviceType: "0"},
		sysfsFixture{name: "nvme0n1", deviceDir: true},
	))
	ctx := newSMARTSyncContext([]string{"sda"})

	syncSMARTDevicesPeriodic(ctx)

	got := readConfigDevices(t, path)
	want := []string{"nvme0n1", "sdb"}
	if !slices.Equal(got, want) {
		t.Fatalf("periodic sync did not update the file: got %v, want %v", got, want)
	}
}

func TestSyncSMARTDevicesPeriodic_NoopWhenListMatches(t *testing.T) {
	// Stessa lista in ordine diverso: nessuna scrittura, perché è churn.
	path := writeCompleteConfig(t, []string{"sda", "sdb"})
	root := makeFakeSysfs(t,
		sysfsFixture{name: "sdb", deviceDir: true, deviceType: "0"},
		sysfsFixture{name: "sda", deviceDir: true, deviceType: "0"},
	)
	pinConfigPathAndSysfs(t, path, root)
	ctx := newSMARTSyncContext([]string{"sdb", "sda"})

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	syncSMARTDevicesPeriodic(ctx)

	assertNoRewrite(t, path, before)
}

func TestSyncSMARTDevicesPeriodic_DisabledDoesNothing(t *testing.T) {
	path := writeCompleteConfig(t, []string{"sda"})
	pinConfigPathAndSysfs(t, path, fullDiskSysfs(t))
	ctx := newSMARTSyncContext([]string{"sda"})
	ctx.Config.Notifications.SMART.Enabled = false

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	syncSMARTDevicesPeriodic(ctx)

	assertNoRewrite(t, path, before)
}

func TestSyncSMARTDevicesPeriodic_EmptyDetectionKeepsList(t *testing.T) {
	path := writeCompleteConfig(t, []string{"sda"})
	pinConfigPathAndSysfs(t, path, t.TempDir())
	ctx := newSMARTSyncContext([]string{"sda"})

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	syncSMARTDevicesPeriodic(ctx)

	assertNoRewrite(t, path, before)
	if got := readConfigDevices(t, path); !slices.Equal(got, []string{"sda"}) {
		t.Fatalf("empty detection wiped the configured list: got %v", got)
	}
}
