package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf8"

	"nasbot/internal/format"
	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  FILESYSTEM WATCHDOG - Lazy Evaluation Approach
// ═══════════════════════════════════════════════════════════════════
//
//  Designed for low-resource embedded systems (ARM, 1GB RAM)
//
//  Strategy:
//  1. Light check every 30-60 min using syscall.Statfs (instant, no I/O)
//  2. Deep scan ONLY when disk usage > 90% (emergency condition)
//  3. Memory-efficient: scan directory-by-directory, no full file lists
//
// ═══════════════════════════════════════════════════════════════════

// FSWatchdogConfig holds filesystem watchdog configuration
// Defined in config_types.go

const (
	// fsWarningAlertCooldown è l'intervallo minimo fra due avvisi di spazio in
	// warning sullo stesso percorso. Valore storico (1h), tenuto com'è: non
	// cambiare il comportamento di chi ha già il watchdog in funzione senza
	// metterlo in config.json.
	//
	// TODO(config): va in config.json come FSWatchdog.WarningAlertCooldownMins
	// (pkg/model/config_types.go + default in config_defaults.go + clamp in
	// config.go). Stesso per fsCriticalCooldownFactor, che oggi è un
	// moltiplicatore invece di un valore assoluto.
	fsWarningAlertCooldown = 1 * time.Hour

	// fsCriticalCooldownFactor scala intervals.critical_alert_cooldown_minutes
	// per il ramo CRITICAL, che oltre ad avvisare lancia una scansione
	// ricorsiva dell'intero volume. Ripetere quella scansione ogni 30 minuti su
	// un disco già oltre soglia è un costo I/O costante che l'utente non può
	// controllare da fuori. Con il default (30 min) il cooldown critical
	// diventa 6h invece di 30 min, cioè 2 allarmi e 2 deep scan al giorno
	// invece di 48.
	fsCriticalCooldownFactor = 12

	// fsCriticalCooldownFloor è il minimo assoluto del cooldown CRITICAL.
	// Serve a coprire chi abbassa critical_alert_cooldown_minutes per le
	// risorse: quel valore non ha niente a che fare con una scansione ricorsiva
	// e non deve poterla riportare a pochi minuti.
	fsCriticalCooldownFloor = 6 * time.Hour

	// maxScanErrorSamples limita quanti percorsi illeggibili finiscono nel log
	// della deep scan (il conteggio nel report resta esatto).
	maxScanErrorSamples = 20

	// fsWatchdogFirstCheckDelay è quanto aspetta il manager dei watchdog prima
	// del primo controllo del filesystem. Non è un intervallo: è il ritardo
	// iniziale, per non aspettare un'intera cadenza prima della prima misura
	// dopo l'avvio (un disco già pieno deve essere visto subito).
	fsWatchdogFirstCheckDelay = 1 * time.Minute

	// fsWatchdogDefaultInterval è la cadenza quando check_interval_minutes
	// manca o non è valido. Deve restare uguale al default in
	// internal/app/config_defaults.go (30 minuti).
	fsWatchdogDefaultInterval = 30 * time.Minute
)

// DirUsage holds directory usage info (memory-efficient)
type DirUsage struct {
	Path  string
	Size  int64
	Files int
}

// FileInfo holds basic file info for top-N tracking
type FileInfo struct {
	Path string
	Size int64
}

// FSWatchdog manages filesystem monitoring
//
// Il watchdog è una lane del manager in monitors_manager.go: avvio, Timer,
// cancellazione da runCtx e ripresa da panic sono quelli di tutte le altre, non
// un meccanismo parallelo.
//
// La configurazione non è sua: si legge dalla snapshot pubblicata dal bot con
// ctx.Cfg(), che un reload rimpiazza senza riavvio. I campi mutabili
// (lastAlerts, isScanning, i timestamp dei check) restano protetti da mu,
// perché vengono toccati dal loop, dalla deep scan in background e da
// /diskinfo su goroutine diverse.
type FSWatchdog struct {
	mu             model.RWMutex
	full           atomic.Pointer[Config]
	lastLightCheck time.Time
	lastDeepScan   time.Time
	// lastAlerts è indicizzato per percorso: un solo campo globale faceva
	// tacere i dischi secondari mentre il primo era in allarme.
	lastAlerts map[string]time.Time
	isScanning bool
}

var (
	fsWatchdog     *FSWatchdog
	fsWatchdogOnce sync.Once
)

// GetFSWatchdog returns the singleton FSWatchdog instance
func GetFSWatchdog() *FSWatchdog {
	fsWatchdogOnce.Do(func() {
		w := &FSWatchdog{
			lastAlerts: make(map[string]time.Time),
		}
		w.publishConfig(&cfg)
		fsWatchdog = w
	})
	return fsWatchdog
}

// publishConfig installs conf as the configuration the watchdog falls back to.
//
// Non è più il canale del loop: quello legge ctx.Cfg(), quindi un reload si
// vede senza toccare niente qui. Resta perché config.go chiama questa funzione
// su ogni publishConfig e perché currentCfg() ci ripiega prima che esista un
// AppContext (bootstrap, contesti costruiti a mano nei test). Il chiamante non
// deve più modificare conf dopo.
func (w *FSWatchdog) publishConfig(conf *Config) {
	if conf == nil {
		return
	}
	w.full.Store(conf)
}

// Cfg returns the published configuration snapshot, never nil.
//
// Valido solo per il fallback di cui sopra: il percorso vivo legge ctx.Cfg().
func (w *FSWatchdog) Cfg() *Config {
	if conf := w.full.Load(); conf != nil {
		return conf
	}
	return &cfg
}

// fsWatchdogConfigFrom takes the FS watchdog section out of a config snapshot.
// Le slice vengono copiate: sono le uniche parti mutabili di Config e i
// lettori non devono poterle vedere a metà.
func fsWatchdogConfigFrom(conf *Config) FSWatchdogConfig {
	if conf == nil {
		return FSWatchdogConfig{}
	}
	return FSWatchdogConfig{
		Enabled:           conf.FSWatchdog.Enabled,
		CheckIntervalMins: conf.FSWatchdog.CheckIntervalMins,
		WarningThreshold:  conf.FSWatchdog.WarningThreshold,
		CriticalThreshold: conf.FSWatchdog.CriticalThreshold,
		DeepScanPaths:     append([]string(nil), conf.FSWatchdog.DeepScanPaths...),
		ExcludePatterns:   append([]string(nil), conf.FSWatchdog.ExcludePatterns...),
		TopNFiles:         conf.FSWatchdog.TopNFiles,
	}
}

// fsWatchdogConfig legge la sezione FS watchdog dalla snapshot pubblicata dal
// bot. Copia le due slice mutabili, così un reload che le rimpiazza non può
// cambiarle sotto i piedi di un check in corso.
func fsWatchdogConfig(ctx *AppContext) FSWatchdogConfig {
	if ctx == nil {
		return FSWatchdogConfig{}
	}
	return fsWatchdogConfigFrom(ctx.Cfg())
}

// monitoredPaths elenca i volumi da tenere d'occhio: root, il volume SSD e i
// dischi secondari dichiarati in notifica. Deduplicati e senza vuoti, perché
// lo stato di allarme è indicizzato per percorso.
func monitoredPaths(conf *Config) []string {
	if conf == nil {
		return nil
	}
	paths := []string{"/", conf.Paths.SSD}
	for mount := range conf.Notifications.SecondaryDisks {
		paths = append(paths, mount)
	}
	seen := make(map[string]struct{}, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// ═══════════════════════════════════════════════════════════════════
//  LIGHT CHECK - Instant, no I/O overhead
// ═══════════════════════════════════════════════════════════════════

// StatfsResult holds the result of a statfs call
type StatfsResult struct {
	Path       string
	TotalBytes uint64
	// FreeBytes counts every free block, including the reserved ones a
	// non-privileged user cannot touch. Kept for diagnosis (it is the gap
	// between FreeBytes and AvailBytes that explains a df discrepancy), not
	// for thresholds: those use AvailBytes.
	FreeBytes   uint64
	AvailBytes  uint64 // Available to non-root users
	UsedBytes   uint64
	UsedPercent float64
	Inodes      uint64
	FreeInodes  uint64
}

// GetDiskUsage performs a lightweight disk usage check using syscall.Statfs
// This is O(1), instant, and causes no disk I/O
func GetDiskUsage(path string) (*StatfsResult, error) {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}

	// Calculate sizes
	blockSize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * blockSize
	freeBytes := stat.Bfree * blockSize
	availBytes := stat.Bavail * blockSize // Available to non-privileged users
	// Le soglie devono usare Bavail, non Bfree: con blocchi riservati
	// (fsck, ext4 reserved blocks) Bfree conta spazio che un utente non
	// può usare, quindi la percentuale risultava più bassa di quella di df
	// e le soglie scattavano più tardi del dovuto.
	usedBytes := totalBytes - availBytes

	// Calculate percentage
	var usedPercent float64
	if totalBytes > 0 {
		usedPercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return &StatfsResult{
		Path:        path,
		TotalBytes:  totalBytes,
		FreeBytes:   freeBytes,
		AvailBytes:  availBytes,
		UsedBytes:   usedBytes,
		UsedPercent: usedPercent,
		Inodes:      stat.Files,
		FreeInodes:  stat.Ffree,
	}, nil
}

// LightCheck performs a fast disk usage check
// Returns: usedPercent, freeGB, error
func (w *FSWatchdog) LightCheck(path string) (float64, float64, error) {
	result, err := GetDiskUsage(path)
	if err != nil {
		return 0, 0, err
	}

	// freeGB è lo spazio che le soglie stanno misurando, cioè Bavail.
	freeGB := float64(result.AvailBytes) / 1024 / 1024 / 1024

	w.mu.Lock()
	w.lastLightCheck = time.Now()
	w.mu.Unlock()

	return result.UsedPercent, freeGB, nil
}

// ═══════════════════════════════════════════════════════════════════
//  DEEP SCAN - Memory-efficient directory-by-directory scan
// ═══════════════════════════════════════════════════════════════════

// DeepScanResult holds the results of a deep filesystem scan
type DeepScanResult struct {
	ScanTime     time.Time
	Duration     time.Duration
	TotalScanned int64
	DirUsages    []DirUsage // Top directories by size
	LargestFiles []FileInfo // Top N largest files
	Errors       []string   // errori sui path di base (deep_scan_paths)
	// SkippedPaths conta le directory/file che la scansione ha saltato per
	// un errore: senza questo il report diceva "0 paths skipped" dopo aver
	// scavalcato tutte le directory non leggibili.
	SkippedPaths int
}

// shouldExcludePath checks if a path should be excluded from scanning
func shouldExcludePath(patterns []string, path string) bool {
	for _, pattern := range patterns {
		if strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

// deepScanState raccoglie lo stato della scansione ricorsiva: i top-N file e
// gli errori incontrati. Un errore su una directory non abortisce la scansione
// (si perderebbe il resto del volume) ma viene contato, altrimenti il report
// mentirebbe sul numero di percorsi saltati.
type deepScanState struct {
	topFiles    []FileInfo
	maxTopFiles int
	patterns    []string
	skipped     int
	skippedList []string
}

func (s *deepScanState) noteSkipped(path string, err error) {
	s.skipped++
	if len(s.skippedList) < maxScanErrorSamples {
		s.skippedList = append(s.skippedList, fmt.Sprintf("%s: %v", path, err))
	}
}

// scanDirectory scans a single directory and returns its total size
// Memory-efficient: does not store file list, only aggregates size
func (s *deepScanState) scanDirectory(dirPath string) (int64, int) {
	var totalSize int64
	var fileCount int

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		// Permission denied or other errors: skip, but count it.
		s.noteSkipped(dirPath, err)
		return 0, 0
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())

		// Skip excluded paths
		if shouldExcludePath(s.patterns, fullPath) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			// File vanished or unreadable between ReadDir and Info.
			s.noteSkipped(fullPath, err)
			continue
		}

		if info.IsDir() {
			// Recursive scan for directories
			subSize, subCount := s.scanDirectory(fullPath)
			totalSize += subSize
			fileCount += subCount
		} else {
			// Regular file
			size := info.Size()
			totalSize += size
			fileCount++

			// Track top N largest files (memory-efficient insertion sort)
			if size > 0 {
				s.insertTopFile(FileInfo{Path: fullPath, Size: size})
			}
		}
	}

	return totalSize, fileCount
}

// insertTopFile maintains a sorted slice of top N largest files
// Memory-efficient: keeps only N items, insertion sort O(N)
func (s *deepScanState) insertTopFile(newFile FileInfo) {
	maxN := s.maxTopFiles
	files := &s.topFiles

	// Find insertion position
	pos := len(*files)
	for i, f := range *files {
		if newFile.Size > f.Size {
			pos = i
			break
		}
	}

	// Insert if within top N
	if pos < maxN {
		if len(*files) < maxN {
			*files = append(*files, FileInfo{})
		}
		// Shift elements
		copy((*files)[pos+1:], (*files)[pos:])
		(*files)[pos] = newFile

		// Trim to max N
		if len(*files) > maxN {
			*files = (*files)[:maxN]
		}
	}
}

// DeepScan performs a comprehensive filesystem scan
// Only called when disk usage exceeds critical threshold
func (w *FSWatchdog) DeepScan(ctx *AppContext, paths []string) *DeepScanResult {
	w.mu.Lock()
	if w.isScanning {
		w.mu.Unlock()
		return nil // Already scanning
	}
	w.isScanning = true
	w.mu.Unlock()

	// Lo snapshot viene preso qui e tenuto per tutta la scansione: i parametri
	// sono coerenti e non possono cambiare a metà. Non serve tenere il lock
	// durante le ore di I/O.
	conf := fsWatchdogConfig(ctx)

	defer func() {
		w.mu.Lock()
		w.isScanning = false
		w.mu.Unlock()
	}()

	topNFiles := conf.TopNFiles
	if topNFiles <= 0 {
		topNFiles = 10
	}

	startTime := time.Now()
	result := &DeepScanResult{
		ScanTime:     startTime,
		DirUsages:    make([]DirUsage, 0),
		LargestFiles: make([]FileInfo, 0, topNFiles),
		Errors:       make([]string, 0),
	}
	st := &deepScanState{
		topFiles:    result.LargestFiles,
		maxTopFiles: topNFiles,
		patterns:    conf.ExcludePatterns,
	}

	slog.Info("[FSWatchdog] Starting deep scan...")

	for _, basePath := range paths {
		if shouldExcludePath(st.patterns, basePath) {
			continue
		}

		// Scan first-level directories to get per-directory usage
		entries, err := os.ReadDir(basePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", basePath, err))
			continue
		}

		for _, entry := range entries {
			fullPath := filepath.Join(basePath, entry.Name())

			if shouldExcludePath(st.patterns, fullPath) {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				st.noteSkipped(fullPath, err)
				continue
			}

			if info.IsDir() {
				// Scan directory recursively
				size, files := st.scanDirectory(fullPath)
				if size > 0 {
					result.DirUsages = append(result.DirUsages, DirUsage{
						Path:  fullPath,
						Size:  size,
						Files: files,
					})
				}
				result.TotalScanned += size
			} else {
				// Root-level file
				result.TotalScanned += info.Size()
				st.insertTopFile(FileInfo{
					Path: fullPath,
					Size: info.Size(),
				})
			}
		}
	}

	// Sort directories by size (largest first)
	sort.Slice(result.DirUsages, func(i, j int) bool {
		return result.DirUsages[i].Size > result.DirUsages[j].Size
	})

	// Keep only top 20 directories
	if len(result.DirUsages) > 20 {
		result.DirUsages = result.DirUsages[:20]
	}

	// The top-N slice was filled in place, publish it back to the result.
	result.LargestFiles = st.topFiles
	result.SkippedPaths = st.skipped

	result.Duration = time.Since(startTime)

	w.mu.Lock()
	w.lastDeepScan = time.Now()
	w.mu.Unlock()

	slog.Info("[FSWatchdog] Deep scan complete",
		"duration", result.Duration.Round(time.Millisecond).String(),
		"scanned", format.FormatBytes(uint64(result.TotalScanned)))

	if st.skipped > 0 {
		slog.Warn("[FSWatchdog] Deep scan skipped unreadable paths",
			"count", st.skipped,
			"sample", strings.Join(st.skippedList, "; "))
	}

	return result
}

// ═══════════════════════════════════════════════════════════════════
//  WATCHDOG TICK - Lazy Evaluation Strategy
// ═══════════════════════════════════════════════════════════════════

// checkFSWatchdogPaths è un giro della lane: fa la light check su ogni volume
// monitorato e lascia che sia checkAndAlert a decidere se serve un avviso.
//
// Non c'è un ciclo qui dentro. Il Timer, il primo giro differito di
// fsWatchdogFirstCheckDelay, la rilettura della cadenza a ogni tick e la
// cancellazione da runCtx sono gestiti da runWatchdogLoop, come per tutte le
// altre lane: mettere qui un select significa un secondo modo di fare la stessa
// cosa, con due timer e due spegnimenti da tenere allineati.
func checkFSWatchdogPaths(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	w := GetFSWatchdog()

	paths := monitoredPaths(ctx.Cfg())
	for _, path := range paths {
		w.checkAndAlert(ctx, bot, path, runCtx)
	}

	live := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		live[p] = struct{}{}
	}
	w.pruneAlertState(live)
}

// pruneAlertState dimentica i dischi che non esistono più, così la mappa
// non cresce per sempre e un disco che torna con lo stesso nome riparte senza
// cooldown residue.
func (w *FSWatchdog) pruneAlertState(live map[string]struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()

	removed := 0
	for path := range w.lastAlerts {
		if _, ok := live[path]; !ok {
			delete(w.lastAlerts, path)
			removed++
		}
	}
	if removed > 0 {
		slog.Info("[FSWatchdog] Alert state pruned", "removed", removed, "tracked", len(w.lastAlerts))
	}
}

// allowAlert dice se il percorso può generare un avviso adesso, e prenota lo
// slot in modo atomico: due check sullo stesso path non possono passare
// insieme. Lo slot viene comunque consumato anche se il messaggio non parte
// (bot assente, invio fallito): altrimenti un invio fallito riproverebbe a ogni
// CheckIntervalMins.
func (w *FSWatchdog) allowAlert(path string, cooldown time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if last, ok := w.lastAlerts[path]; ok && time.Since(last) < cooldown {
		return false
	}
	if w.lastAlerts == nil {
		w.lastAlerts = make(map[string]time.Time, 4)
	}
	w.lastAlerts[path] = time.Now()
	return true
}

// clearAlert azzera lo stato di allarme del percorso: tornare sotto la soglia
// chiude l'episodio, così il prossimo avviso non aspetta il cooldown.
func (w *FSWatchdog) clearAlert(path string) {
	w.mu.Lock()
	delete(w.lastAlerts, path)
	w.mu.Unlock()
}

// criticalAlertCooldown deriva il cooldown del ramo CRITICAL dalla soglia
// generica intervals.critical_alert_cooldown_minutes, con un pavimento: la
// soglia in questione è pensata per gli alert delle risorse, non per una
// scansione ricorsiva del volume.
func criticalAlertCooldown(baseMins int) time.Duration {
	if baseMins <= 0 {
		baseMins = 30
	}
	scaled := time.Duration(baseMins) * time.Minute * fsCriticalCooldownFactor
	if scaled < fsCriticalCooldownFloor {
		return fsCriticalCooldownFloor
	}
	return scaled
}

// Nota sulle quiet hours: questa lane non tace mai, nonostante il bot abbia
// una finestra di silenzio. Era la posizione dello standalone, che non aveva i
// settings per fare altro, ed è rimasta una scelta deliberata quando il codice
// è entrato nel bot: un disco che si riempie alle 3 deve svegliare il
// proprietario. Per questo qui non c'è nessun wrapper silenzioso e ctx.Tr si
// chiama senza condizioni; se un giorno la si vuole applicare a questa lane,
// è ctx.IsQuietHours(), una riga, non una funzione che risponde sempre false.
//

// checkAndAlert performs the lazy evaluation check
func (w *FSWatchdog) checkAndAlert(ctx *AppContext, bot BotAPI, path string, runCtx context.Context) {
	// Step 1: Light check (instant, no I/O)
	usedPercent, freeGB, err := w.LightCheck(path)
	if err != nil {
		slog.Error("[FSWatchdog] Light check failed", "err", err)
		return
	}

	// Heartbeat: una riga per disco per giro, con il path (prima non c'era:
	// con tre dischi non si sapeva quale fosse quello misurato).
	slog.Info("[FSWatchdog] Light check",
		"path", path, "used_pct", usedPercent, "free_gb", freeGB)

	full := ctx.Cfg()
	conf := fsWatchdogConfigFrom(full)

	// Step 2: below the warning threshold: stay dormant and forget any
	// pending alert state, so the next episode warns immediately.
	if usedPercent < conf.WarningThreshold {
		w.clearAlert(path)
		return
	}

	critical := usedPercent >= conf.CriticalThreshold

	// Step 3: one throttle for both branches. It used to live only in the
	// warning branch, so a disk past the critical threshold warned and
	// re-scanned the whole volume every CheckIntervalMins, forever.
	cooldown := fsWarningAlertCooldown
	if critical {
		cooldown = criticalAlertCooldown(full.Intervals.CriticalAlertCooldownMins)
	}
	if !w.allowAlert(path, cooldown) {
		slog.Debug("[FSWatchdog] Alert suppressed by cooldown",
			"path", path, "used_pct", usedPercent, "cooldown", cooldown)
		return
	}

	if !critical {
		notifyWatchdog(ctx, bot, "space-warning",
			fmt.Sprintf(ctx.Tr("fswd_space_warn"), path, usedPercent, freeGB))

		slog.Warn("[FSWatchdog] disk space warning",
			"path", path, "used_pct", usedPercent, "free_gb", freeGB)
		return
	}

	// Step 4: CRITICAL - Trigger deep scan
	slog.Warn("[FSWatchdog] CRITICAL", "path", path, "used_pct", usedPercent)

	// Notify user
	notifyWatchdog(ctx, bot, "space-critical",
		fmt.Sprintf(ctx.Tr("fswd_space_crit"), path, usedPercent, freeGB))

	// Run deep scan in background to not block
	deepScanPaths := conf.DeepScanPaths
	goSafe("fs-deepscan", func() {
		if runCtx.Err() != nil {
			return // shutting down, no point in walking the whole volume
		}
		result := w.DeepScan(ctx, deepScanPaths)
		if result == nil {
			return // Scan already in progress
		}

		// Send results
		if runCtx.Err() != nil {
			return // shut down while scanning
		}
		w.sendDeepScanReport(ctx, bot, result)
	})

	slog.Error("[FSWatchdog] disk space critical, deep scan triggered",
		"path", path, "used_pct", usedPercent)
}

// notifyWatchdog consegna un messaggio del filesystem watchdog al proprietario.
//
// Il ramo senza sessione Telegram resta per i test: dentro il bot bot è
// sempre non-nil, ma la funzione resta chiamabile senza e in quel caso il testo
// va nel log invece di sparire. kind serve solo a distinguere le righe.
func notifyWatchdog(ctx *AppContext, bot BotAPI, kind, text string) {
	if bot == nil {
		slog.Info("[FSWatchdog] no bot session, message logged only",
			"kind", kind, "msg", text)
		return
	}
	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, text)
	m.ParseMode = "Markdown"
	safeSend(bot, m)
}

// sendDeepScanReport formats and sends the deep scan results
func (w *FSWatchdog) sendDeepScanReport(ctx *AppContext, bot BotAPI, result *DeepScanResult) {
	var b strings.Builder

	b.WriteString(ctx.Tr("fswd_deepscan_title"))
	b.WriteString(fmt.Sprintf("⏱ Scan time: `%v`\n", result.Duration.Round(time.Millisecond)))
	b.WriteString(fmt.Sprintf("📁 Total scanned: `%s`\n\n", format.FormatBytes(uint64(result.TotalScanned))))

	// Top directories
	if len(result.DirUsages) > 0 {
		b.WriteString(ctx.Tr("fswd_largest_dirs"))
		for i, dir := range result.DirUsages {
			if i >= 10 {
				break
			}
			b.WriteString(fmt.Sprintf("`%s` %s\n",
				format.FormatBytes(uint64(dir.Size)),
				truncatePath(dir.Path, 35)))
		}
		b.WriteString("\n")
	}

	// Top files
	if len(result.LargestFiles) > 0 {
		b.WriteString(ctx.Tr("fswd_largest_files"))
		for i, file := range result.LargestFiles {
			if i >= 10 {
				break
			}
			b.WriteString(fmt.Sprintf("`%s` %s\n",
				format.FormatBytes(uint64(file.Size)),
				truncatePath(file.Path, 35)))
		}
	}

	// Paths that could not be read. The detail stays in the log: the report
	// carries the exact count, not a sample of it.
	skipped := result.SkippedPaths + len(result.Errors)
	if skipped > 0 {
		b.WriteString(fmt.Sprintf("\n_⚠️ %d paths skipped due to errors_", skipped))
	}

	notifyWatchdog(ctx, bot, "deep-scan-report", b.String())
}

// truncateRunes tronca per runa, non per byte: un nome file UTF-8 non-ASCII
// tagliato a metà produce testo non valido e Telegram rifiuta l'intero
// rapporto.
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if n >= len(r) {
		return s
	}
	return string(r[:n])
}

// truncatePath shortens a path for display
func truncatePath(path string, maxLen int) string {
	if path == "" {
		return ""
	}
	if maxLen <= 0 {
		return ""
	}
	if maxLen <= 4 {
		if strings.HasPrefix(path, "/") {
			return "/..."[:maxLen]
		}
		return strings.Repeat(".", maxLen)
	}

	parts := strings.Split(path, "/")
	pathLen := utf8.RuneCountInString(path)
	if len(parts) <= 2 && pathLen <= maxLen {
		return path
	}

	if len(parts) > 4 {
		result := "..." + filepath.Join(parts[len(parts)-3:]...)
		if utf8.RuneCountInString(result) > maxLen {
			return truncateRunes(result, maxLen-3) + "..."
		}
		return result
	}

	if pathLen <= maxLen {
		return path
	}
	if len(parts) <= 2 {
		return truncateRunes(path, maxLen-3) + "..."
	}

	result := "..." + filepath.Join(parts[len(parts)-3:]...)
	if utf8.RuneCountInString(result) > maxLen {
		return truncateRunes(result, maxLen-3) + "..."
	}
	return result
}

// ═══════════════════════════════════════════════════════════════════
//  MANUAL TRIGGER (for /diskinfo command)
// ═══════════════════════════════════════════════════════════════════

// GetDiskInfoText returns disk usage info for manual command
func GetDiskInfoText(ctx *AppContext) string {
	var b strings.Builder
	b.WriteString(ctx.Tr("fswd_disk_status_title"))

	for _, path := range monitoredPaths(ctx.Cfg()) {
		result, err := GetDiskUsage(path)
		if err != nil {
			continue
		}

		icon := "✅"
		if result.UsedPercent >= 90 {
			icon = "🚨"
		} else if result.UsedPercent >= 80 {
			icon = "⚠️"
		}

		b.WriteString(fmt.Sprintf("%s `%s`\n", icon, path))
		// AvailBytes, non FreeBytes: è il numero su cui scattano le soglie,
		// e con blocchi riservati i due non quadravano.
		b.WriteString(fmt.Sprintf("   Used: `%.1f%%` · Free: `%s`\n",
			result.UsedPercent,
			format.FormatBytes(result.AvailBytes)))
		inodePct := 0.0
		if result.Inodes > 0 {
			inodePct = float64(result.FreeInodes) / float64(result.Inodes) * 100
		}
		b.WriteString(fmt.Sprintf("   Inodes: `%.1f%%` free\n\n", inodePct))
	}

	w := GetFSWatchdog()
	lastLight, lastDeep := w.lastCheckTimes()
	if !lastLight.IsZero() {
		b.WriteString(fmt.Sprintf("_Last check: %s_\n", lastLight.Format("15:04")))
	}
	if !lastDeep.IsZero() {
		b.WriteString(fmt.Sprintf("_Last deep scan: %s_", lastDeep.Format("02/01 15:04")))
	}

	return b.String()
}

// lastCheckTimes legge i timestamp dei check sotto lock: /diskinfo gira su una
// goroutine diversa dal loop del watchdog.
func (w *FSWatchdog) lastCheckTimes() (light, deep time.Time) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastLightCheck, w.lastDeepScan
}
