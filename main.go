package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// ═══════════════════════════════════════════════════════════════════
//  CONFIGURAZIONE
// ═══════════════════════════════════════════════════════════════════

const (
	// Soglie allarme
	SogliaCPU   = 90.0
	SogliaRAM   = 90.0
	SogliaDisco = 90.0 // Allarme disco quasi pieno

	// Path volumi (adatta al tuo NAS)
	PathSSD = "/Volume1"
	PathHDD = "/Volume2"

	// Intervalli background
	IntervalloStats     = 5 * time.Second  // Aggiorna cache stats
	IntervalloMonitor   = 30 * time.Second // Check allarmi
	DurataStressDisco   = 2 * time.Minute  // Soglia stress I/O prolungato
	SogliaIOCritico     = 95.0             // % I/O per considerare stress
	SogliaRAMCritica    = 95.0             // RAM critica per azioni autonome
	MaxRestartContainer = 3                // Max riavvii automatici container/ora

	// Orari report giornalieri (Europe/Rome)
	ReportMattina = 7  // 07:30
	ReportSera    = 18 // 18:30
	ReportMinuti  = 30
	Timezone      = "Europe/Rome"

	// Stato persistente
	StateFile = "/Volume1/public/nasbot_state.json"
)

var (
	BotToken      string
	AllowedUserID int64

	// Cache globale con mutex
	statsCache Stats
	statsMutex sync.RWMutex
	statsReady bool

	// Tracking stress I/O
	stressStartTime   time.Time
	stressNotified    bool
	consecutiveStress int

	// Tracking azioni autonome
	autoRestarts      map[string][]time.Time
	autoRestartsMutex sync.Mutex

	// Report tracking
	lastReportTime    time.Time
	reportEvents      []ReportEvent
	reportEventsMutex sync.Mutex
	location          *time.Location // Timezone

	// Pending confirmations
	pendingAction      string
	pendingActionMutex sync.Mutex

	// Container action pending
	pendingContainerAction string
	pendingContainerName   string
	pendingContainerMutex  sync.Mutex
)

// ReportEvent traccia eventi per il report periodico
type ReportEvent struct {
	Time    time.Time
	Type    string // "warning", "critical", "action", "info"
	Message string
}

// BotState per persistenza
type BotState struct {
	LastReportTime time.Time              `json:"last_report_time"`
	AutoRestarts   map[string][]time.Time `json:"auto_restarts"`
}

// ═══════════════════════════════════════════════════════════════════
//  INIT & MAIN
// ═══════════════════════════════════════════════════════════════════

func init() {
	BotToken = os.Getenv("BOT_TOKEN")
	if BotToken == "" {
		log.Fatal("❌ BOT_TOKEN mancante!")
	}
	uidStr := os.Getenv("BOT_USER_ID")
	if uidStr == "" {
		log.Fatal("❌ BOT_USER_ID mancante!")
	}
	var err error
	AllowedUserID, err = strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		log.Fatal("❌ BOT_USER_ID deve essere numerico!")
	}

	// Inizializza timezone
	location, err = time.LoadLocation(Timezone)
	if err != nil {
		log.Printf("⚠️ Timezone %s non trovata, uso UTC", Timezone)
		location = time.UTC
	}

	// Inizializza mappe
	autoRestarts = make(map[string][]time.Time)

	// Carica stato persistente
	loadState()
}

func loadState() {
	data, err := os.ReadFile(StateFile)
	if err != nil {
		log.Printf("📁 Nessuno stato precedente trovato (primo avvio)")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("⚠️ Errore lettura stato: %v", err)
		return
	}
	lastReportTime = state.LastReportTime
	if state.AutoRestarts != nil {
		autoRestarts = state.AutoRestarts
	}
	log.Printf("✅ Stato ripristinato (ultimo report: %s)", lastReportTime.Format("15:04"))
}

func saveState() {
	autoRestartsMutex.Lock()
	state := BotState{
		LastReportTime: lastReportTime,
		AutoRestarts:   autoRestarts,
	}
	autoRestartsMutex.Unlock()

	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("⚠️ Errore serializzazione stato: %v", err)
		return
	}
	if err := os.WriteFile(StateFile, data, 0644); err != nil {
		log.Printf("⚠️ Errore salvataggio stato: %v", err)
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ PANIC: %v\n%s", r, debug.Stack())
			// Salva stato prima di crashare
			saveState()
		}
	}()

	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		log.Fatalf("❌ Errore avvio bot: %v", err)
	}
	log.Printf("✅ NASBot avviato: @%s", bot.Self.UserName)

	// Notifica avvio (utile dopo recovery da crash)
	nextReport, isMorning := getNextReportTime()
	nextReportStr := "stasera 18:30"
	if isMorning {
		nextReportStr = "domani 07:30"
		if time.Now().In(location).Hour() < ReportMattina {
			nextReportStr = "oggi 07:30"
		}
	}

	startupText := fmt.Sprintf(`🤖 *NASBot Online*

✅ Bot avviato correttamente
📬 Report: 07:30 e 18:30
🛡 Protezione autonoma attiva

_Prossimo report: %s_
_Usa /help per i comandi_`, nextReportStr)

	_ = nextReport
	startupMsg := tgbotapi.NewMessage(AllowedUserID, startupText)
	startupMsg.ParseMode = "Markdown"
	bot.Send(startupMsg)

	// Gestione shutdown graceful
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("🛑 Shutdown richiesto, salvataggio stato...")
		saveState()
		os.Exit(0)
	}()

	// Avvia goroutine background
	go statsCollector()
	go monitorAlerts(bot)
	go periodicReport(bot)
	go autonomousManager(bot)

	// Aspetta primo ciclo stats
	time.Sleep(IntervalloStats + 500*time.Millisecond)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// Callback (pulsanti inline)
		if update.CallbackQuery != nil {
			go handleCallback(bot, update.CallbackQuery)
			continue
		}
		// Comandi
		if update.Message == nil || update.Message.Chat.ID != AllowedUserID {
			continue
		}
		if update.Message.IsCommand() {
			go handleCommand(bot, update.Message)
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  HANDLER COMANDI
// ═══════════════════════════════════════════════════════════════════

func handleCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	args := msg.CommandArguments()

	switch msg.Command() {
	case "status", "start":
		sendWithKeyboard(bot, chatID, getStatusText())
	case "docker":
		sendDockerMenu(bot, chatID)
	case "dstats":
		sendWithKeyboard(bot, chatID, getDockerStatsText())
	case "temp":
		sendWithKeyboard(bot, chatID, getTempText())
	case "net":
		sendMarkdown(bot, chatID, getNetworkText())
	case "speedtest":
		runSpeedtest(bot, chatID)
	case "logs":
		sendMarkdown(bot, chatID, getLogsText())
	case "report":
		sendMarkdown(bot, chatID, generateReport(true))
	case "container":
		handleContainerCommand(bot, chatID, args)
	case "reboot":
		sendPowerConfirm(bot, chatID, "reboot")
	case "shutdown":
		sendPowerConfirm(bot, chatID, "shutdown")
	case "help":
		sendMarkdown(bot, chatID, getHelpText())
	default:
		bot.Send(tgbotapi.NewMessage(chatID, "❓ Comando sconosciuto. Usa /help"))
	}
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery) {
	bot.Request(tgbotapi.NewCallback(query.ID, ""))

	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	data := query.Data

	// Gestione conferma power
	if data == "confirm_reboot" || data == "confirm_shutdown" {
		handlePowerConfirm(bot, chatID, msgID, data)
		return
	}
	if data == "cancel_power" {
		editMessage(bot, chatID, msgID, "❌ Operazione annullata.", nil)
		return
	}

	// Gestione container actions
	if strings.HasPrefix(data, "container_") {
		handleContainerCallback(bot, chatID, msgID, data)
		return
	}

	// Navigazione normale
	var text string
	var kb *tgbotapi.InlineKeyboardMarkup
	switch data {
	case "refresh_status":
		text = getStatusText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_temp":
		text = getTempText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_docker":
		text, kb = getDockerMenuText()
	case "show_dstats":
		text = getDockerStatsText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_net":
		text = getNetworkText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "show_report":
		text = generateReport(true)
		mainKb := getMainKeyboard()
		kb = &mainKb
	case "back_main":
		text = getStatusText()
		mainKb := getMainKeyboard()
		kb = &mainKb
	default:
		return
	}
	editMessage(bot, chatID, msgID, text, kb)
}

// ═══════════════════════════════════════════════════════════════════
//  GENERATORI TESTO (usano cache, risposta istantanea)
// ═══════════════════════════════════════════════════════════════════

func getStatusText() string {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return "⏳ *Raccolta dati in corso...*\nRiprova tra qualche secondo."
	}

	var b strings.Builder
	now := time.Now().Format("15:04:05")

	// Header
	b.WriteString(fmt.Sprintf("📊 *SISTEMA* ─ %s\n", now))
	b.WriteString(fmt.Sprintf("⏱ Uptime: _%s_\n", formatUptime(s.Uptime)))
	b.WriteString("─────────────────────\n")

	// CPU
	b.WriteString(fmt.Sprintf("%s *CPU*  %s  `%.1f%%`\n", getIcon(s.CPU, 70, 90), progressBar(s.CPU, 10), s.CPU))

	// RAM
	b.WriteString(fmt.Sprintf("%s *RAM*  %s  `%.1f%%`\n", getIcon(s.RAM, 70, 90), progressBar(s.RAM, 10), s.RAM))
	b.WriteString(fmt.Sprintf("   ↳ Libera: `%s` di `%s`\n", formatRAM(s.RAMFreeMB), formatRAM(s.RAMTotalMB)))

	// SWAP (solo se usato)
	if s.Swap > 0.1 {
		b.WriteString(fmt.Sprintf("%s *SWAP* %s  `%.1f%%`\n", getIcon(s.Swap, 50, 75), progressBar(s.Swap, 10), s.Swap))
	}

	b.WriteString("─────────────────────\n")

	// Storage
	b.WriteString(fmt.Sprintf("💾 *SSD*  %s  `%.1f%%`\n", progressBar(s.VolSSD.Used, 10), s.VolSSD.Used))
	b.WriteString(fmt.Sprintf("   ↳ Libero: `%s`\n", formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("💿 *HDD*  %s  `%.1f%%`\n", progressBar(s.VolHDD.Used, 10), s.VolHDD.Used))
	b.WriteString(fmt.Sprintf("   ↳ Libero: `%s`\n", formatBytes(s.VolHDD.Free)))

	// I/O
	ioIcon := "🟢"
	if s.DiskUtil > 80 {
		ioIcon = "🟡"
	}
	if s.DiskUtil > 95 {
		ioIcon = "🔴"
	}
	b.WriteString(fmt.Sprintf("%s *I/O*  `%.0f%%` ─ R:`%.1f` W:`%.1f` MB/s\n", ioIcon, s.DiskUtil, s.ReadMBs, s.WriteMBs))

	b.WriteString("─────────────────────\n")

	// Top processi
	b.WriteString("🔥 *Top CPU:*\n")
	b.WriteString(formatTopProcs(s.TopCPU, "cpu"))
	b.WriteString("\n🧠 *Top RAM:*\n")
	b.WriteString(formatTopProcs(s.TopRAM, "mem"))

	return b.String()
}

func getTempText() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🌡 *TEMPERATURE* ─ %s\n\n", time.Now().Format("15:04:05")))

	// CPU temp
	cpuTemp := readCPUTemp()
	cpuIcon := "🟢"
	if cpuTemp > 60 {
		cpuIcon = "🟡"
	}
	if cpuTemp > 75 {
		cpuIcon = "🔴"
	}
	b.WriteString(fmt.Sprintf("%s *CPU:* `%.1f°C`\n\n", cpuIcon, cpuTemp))

	// Dischi SMART
	b.WriteString("💾 *Dischi:*\n")
	for _, dev := range []string{"sda", "sdb"} {
		temp, health := readDiskSMART(dev)
		icon := "🟢"
		if strings.Contains(strings.ToUpper(health), "FAIL") {
			icon = "🔴"
		} else if temp > 45 {
			icon = "🟡"
		}
		b.WriteString(fmt.Sprintf("%s `%s` %d°C ─ %s\n", icon, dev, temp, health))
	}

	return b.String()
}

func getDockerText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "⏱ *Timeout Docker* — riprova."
		}
		return "❌ *Docker non disponibile*\n\n_Verifica che Docker sia installato e in esecuzione._"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "🐳 *Nessun container trovato*"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🐳 *DOCKER* ─ %s\n\n", time.Now().Format("15:04:05")))

	running, stopped := 0, 0
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		name := truncate(parts[0], 14)
		status := parts[1]

		icon := "🔴"
		statusShort := "Stopped"
		if strings.Contains(status, "Up") {
			icon = "🟢"
			statusParts := strings.Fields(status)
			if len(statusParts) >= 2 {
				statusShort = statusParts[1]
				if len(statusParts) >= 3 && (statusParts[2] == "hours" || statusParts[2] == "days" || statusParts[2] == "minutes") {
					statusShort = statusParts[1] + " " + statusParts[2][:1]
				}
			}
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("%s `%-14s` %s\n", icon, name, statusShort))
	}

	b.WriteString(fmt.Sprintf("\n✅ %d running  🔴 %d stopped", running, stopped))
	return b.String()
}

func getDockerStatsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "⏱ *Timeout Docker Stats* — riprova."
		}
		return "❌ *Docker Stats non disponibile*"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "📈 *Nessun container attivo*"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📈 *RISORSE CONTAINER* ─ %s\n", time.Now().Format("15:04:05")))
	b.WriteString("```\n")
	b.WriteString(fmt.Sprintf("%-12s %6s %6s %s\n", "CONTAINER", "CPU", "MEM%", "MEM"))
	b.WriteString("────────────────────────────────\n")

	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		name := truncate(parts[0], 12)
		cpuP := strings.TrimSpace(parts[1])
		memUsage := strings.TrimSpace(parts[2])
		memP := strings.TrimSpace(parts[3])

		memShort := strings.Split(memUsage, " ")[0]
		memShort = strings.Replace(memShort, "MiB", "M", 1)
		memShort = strings.Replace(memShort, "GiB", "G", 1)

		b.WriteString(fmt.Sprintf("%-12s %6s %6s %s\n", name, cpuP, memP, memShort))
	}
	b.WriteString("```")

	return b.String()
}

func getNetworkText() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🌐 *RETE* ─ %s\n\n", time.Now().Format("15:04:05")))

	localIP := "N/A"
	if out, err := exec.Command("hostname", "-I").Output(); err == nil {
		ips := strings.Fields(string(out))
		if len(ips) > 0 {
			localIP = ips[0]
		}
	}
	b.WriteString(fmt.Sprintf("🏠 *LAN:* `%s`\n", localIP))

	publicIP := "N/A"
	client := http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if body, err := io.ReadAll(resp.Body); err == nil {
			publicIP = string(body)
		}
	}
	b.WriteString(fmt.Sprintf("🌍 *WAN:* `%s`\n", publicIP))

	return b.String()
}

func getLogsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dmesg")
	out, err := cmd.Output()
	if err != nil {
		cmd = exec.CommandContext(ctx, "journalctl", "-n", "15", "--no-pager")
		out, _ = cmd.Output()
	}

	lines := strings.Split(string(out), "\n")
	start := len(lines) - 15
	if start < 0 {
		start = 0
	}
	recentLogs := strings.Join(lines[start:], "\n")

	if len(recentLogs) > 3500 {
		recentLogs = recentLogs[:3500] + "..."
	}

	return fmt.Sprintf("📜 *SYSTEM LOGS*\n```\n%s\n```", recentLogs)
}

func getHelpText() string {
	return `🤖 *NASBot — Guida Comandi*

📊 *Monitoraggio:*
/status — Panoramica risorse (CPU, RAM, Dischi)
/temp — Temperature e salute dischi (SMART)
/report — Report completo stato NAS
/net — Indirizzo IP locale e pubblico
/logs — Ultimi messaggi/errori di sistema

🐳 *Docker:*
/docker — Menu gestione container
/dstats — Consumo risorse per container
/container \<nome\> — Info container specifico

🔧 *Utilità:*
/speedtest — Misura velocità connessione

⚡ *Sistema:*
/reboot — Riavvia il NAS
/shutdown — Spegni il NAS in sicurezza

━━━━━━━━━━━━━━━━━━━━━━━━━━━
🌅 *Report mattina:* 07:30
🌆 *Report sera:* 18:30
🛡 *Protezione automatica attiva*
━━━━━━━━━━━━━━━━━━━━━━━━━━━
💡 _Usa i pulsanti per navigare_`
}

// ═══════════════════════════════════════════════════════════════════
//  GESTIONE CONTAINER DOCKER
// ═══════════════════════════════════════════════════════════════════

func getContainerList() []ContainerInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}|{{.ID}}")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var containers []ContainerInfo
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) >= 4 {
			containers = append(containers, ContainerInfo{
				Name:    parts[0],
				Status:  parts[1],
				Image:   parts[2],
				ID:      parts[3],
				Running: strings.Contains(parts[1], "Up"),
			})
		}
	}
	return containers
}

type ContainerInfo struct {
	Name    string
	Status  string
	Image   string
	ID      string
	Running bool
}

func sendDockerMenu(bot *tgbotapi.BotAPI, chatID int64) {
	text, kb := getDockerMenuText()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if kb != nil {
		msg.ReplyMarkup = kb
	}
	bot.Send(msg)
}

func getDockerMenuText() (string, *tgbotapi.InlineKeyboardMarkup) {
	containers := getContainerList()
	if len(containers) == 0 {
		mainKb := getMainKeyboard()
		return "🐳 *Nessun container trovato*\n\n_Verifica che Docker sia installato._", &mainKb
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🐳 *GESTIONE CONTAINER* ─ %s\n", time.Now().Format("15:04:05")))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	running, stopped := 0, 0
	for _, c := range containers {
		icon := "🔴"
		statusText := "Fermo"
		if c.Running {
			icon = "🟢"
			statusText = parseUptime(c.Status)
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("%s *%s*\n", icon, c.Name))
		b.WriteString(fmt.Sprintf("   ↳ %s │ `%s`\n", statusText, truncate(c.Image, 20)))
	}

	b.WriteString(fmt.Sprintf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━\n"))
	b.WriteString(fmt.Sprintf("✅ *%d* attivi  🔴 *%d* fermi\n", running, stopped))
	b.WriteString("\n_Seleziona un container per gestirlo:_")

	// Crea bottoni per ogni container
	var rows [][]tgbotapi.InlineKeyboardButton
	for _, c := range containers {
		icon := "🔴"
		if c.Running {
			icon = "🟢"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(fmt.Sprintf("%s %s", icon, c.Name), "container_select_"+c.Name),
		))
	}
	// Aggiungi bottone indietro
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "show_docker"),
		tgbotapi.NewInlineKeyboardButtonData("◀️ Indietro", "back_main"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.String(), &kb
}

func handleContainerCallback(bot *tgbotapi.BotAPI, chatID int64, msgID int, data string) {
	parts := strings.Split(data, "_")
	if len(parts) < 3 {
		return
	}

	action := parts[1]
	containerName := strings.Join(parts[2:], "_")

	switch action {
	case "select":
		// Mostra menu azioni per questo container
		showContainerActions(bot, chatID, msgID, containerName)
	case "start", "stop", "restart", "logs":
		// Conferma azione
		confirmContainerAction(bot, chatID, msgID, containerName, action)
	case "confirm":
		// Esegui azione confermata
		executeContainerAction(bot, chatID, msgID, containerName, strings.Join(parts[3:], "_"))
	case "cancel":
		// Torna al menu container
		text, kb := getDockerMenuText()
		editMessage(bot, chatID, msgID, text, kb)
	}
}

func showContainerActions(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName string) {
	containers := getContainerList()
	var container *ContainerInfo
	for _, c := range containers {
		if c.Name == containerName {
			container = &c
			break
		}
	}

	if container == nil {
		editMessage(bot, chatID, msgID, "❌ Container non trovato.", nil)
		return
	}

	var b strings.Builder
	icon := "🔴"
	statusText := "Fermo"
	if container.Running {
		icon = "🟢"
		statusText = parseUptime(container.Status)
	}

	b.WriteString(fmt.Sprintf("🐳 *Container: %s*\n", container.Name))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")
	b.WriteString(fmt.Sprintf("%s *Stato:* %s\n", icon, statusText))
	b.WriteString(fmt.Sprintf("📦 *Immagine:* `%s`\n", container.Image))
	b.WriteString(fmt.Sprintf("🆔 *ID:* `%s`\n", container.ID[:12]))

	// Ottieni stats se running
	if container.Running {
		stats := getContainerStats(containerName)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n📊 *Risorse:*\n%s", stats))
		}
	}

	b.WriteString("\n\n_Seleziona un'azione:_")

	// Bottoni azioni
	var rows [][]tgbotapi.InlineKeyboardButton
	if container.Running {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏹ Stop", "container_stop_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("🔄 Restart", "container_restart_"+containerName),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📜 Logs", "container_logs_"+containerName),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶️ Start", "container_start_"+containerName),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("◀️ Lista Container", "show_docker"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

func confirmContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	if action == "logs" {
		// I logs non richiedono conferma
		showContainerLogs(bot, chatID, msgID, containerName)
		return
	}

	actionText := map[string]string{
		"start":   "avviare",
		"stop":    "fermare",
		"restart": "riavviare",
	}[action]

	emoji := map[string]string{
		"start":   "▶️",
		"stop":    "⏹",
		"restart": "🔄",
	}[action]

	text := fmt.Sprintf("%s *Confermi di voler %s* `%s`*?*", emoji, actionText, containerName)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Sì", fmt.Sprintf("container_confirm_%s_%s", containerName, action)),
			tgbotapi.NewInlineKeyboardButtonData("❌ No", "container_cancel_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

func executeContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	editMessage(bot, chatID, msgID, fmt.Sprintf("⏳ *Esecuzione %s su* `%s`*...*", action, containerName), nil)

	var cmd *exec.Cmd
	switch action {
	case "start":
		cmd = exec.CommandContext(ctx, "docker", "start", containerName)
	case "stop":
		cmd = exec.CommandContext(ctx, "docker", "stop", containerName)
	case "restart":
		cmd = exec.CommandContext(ctx, "docker", "restart", containerName)
	default:
		return
	}

	err := cmd.Run()
	var resultText string
	if err != nil {
		resultText = fmt.Sprintf("❌ *Errore:* %v", err)
		addReportEvent("warning", fmt.Sprintf("Errore %s container %s: %v", action, containerName, err))
	} else {
		emoji := map[string]string{"start": "▶️", "stop": "⏹", "restart": "🔄"}[action]
		resultText = fmt.Sprintf("%s *Container* `%s` *%s completato!*", emoji, containerName, action)
		addReportEvent("info", fmt.Sprintf("Container %s: %s (manuale)", containerName, action))
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("◀️ Lista Container", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

func showContainerLogs(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "30", containerName)
	out, err := cmd.CombinedOutput()

	var text string
	if err != nil {
		text = fmt.Sprintf("❌ *Errore lettura logs:* %v", err)
	} else {
		logs := string(out)
		if len(logs) > 3500 {
			logs = logs[len(logs)-3500:]
		}
		if logs == "" {
			logs = "(nessun log disponibile)"
		}
		text = fmt.Sprintf("📜 *Logs:* `%s`\n```\n%s\n```", containerName, logs)
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "container_logs_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("◀️ Indietro", "container_select_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

func getContainerStats(containerName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) < 4 {
		return ""
	}

	return fmt.Sprintf("   CPU: `%s` │ RAM: `%s` (`%s`)\n   Net: `%s`",
		strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]),
		strings.TrimSpace(parts[2]),
		strings.TrimSpace(parts[3]))
}

func handleContainerCommand(bot *tgbotapi.BotAPI, chatID int64, args string) {
	if args == "" {
		sendDockerMenu(bot, chatID)
		return
	}

	// Cerca container per nome
	containers := getContainerList()
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			// Invia info container con menu
			msg := tgbotapi.NewMessage(chatID, "")
			msg.ParseMode = "Markdown"
			text, _ := getContainerInfoText(c)
			msg.Text = text
			bot.Send(msg)
			return
		}
	}
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("❌ Container `%s` non trovato.", args)))
}

func getContainerInfoText(c ContainerInfo) (string, *tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	icon := "🔴"
	if c.Running {
		icon = "🟢"
	}
	b.WriteString(fmt.Sprintf("🐳 *%s* %s\n", c.Name, icon))
	b.WriteString(fmt.Sprintf("📦 `%s`\n", c.Image))
	b.WriteString(fmt.Sprintf("📋 %s\n", c.Status))

	if c.Running {
		stats := getContainerStats(c.Name)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n%s", stats))
		}
	}

	return b.String(), nil
}

func parseUptime(status string) string {
	if !strings.Contains(status, "Up") {
		return "Fermo"
	}
	parts := strings.Fields(status)
	if len(parts) >= 2 {
		result := parts[1]
		if len(parts) >= 3 {
			result += " " + parts[2]
		}
		return result
	}
	return "Attivo"
}

// ═══════════════════════════════════════════════════════════════════
//  POWER MANAGEMENT (con conferma)
// ═══════════════════════════════════════════════════════════════════

func sendPowerConfirm(bot *tgbotapi.BotAPI, chatID int64, action string) {
	pendingActionMutex.Lock()
	pendingAction = action
	pendingActionMutex.Unlock()

	emoji := "🔄"
	text := "riavviare"
	if action == "shutdown" {
		emoji = "🛑"
		text = "spegnere"
	}

	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("%s *Confermi di voler %s il NAS?*\n\n⚠️ _Questa azione è irreversibile._", emoji, text))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Sì, procedi", "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData("❌ Annulla", "cancel_power"),
		),
	)
	bot.Send(msg)
}

func handlePowerConfirm(bot *tgbotapi.BotAPI, chatID int64, msgID int, data string) {
	pendingActionMutex.Lock()
	action := pendingAction
	pendingAction = ""
	pendingActionMutex.Unlock()

	expectedAction := strings.TrimPrefix(data, "confirm_")
	if action == "" || action != expectedAction {
		editMessage(bot, chatID, msgID, "⏱ *Sessione scaduta* — usa di nuovo il comando.", nil)
		return
	}

	cmd := "reboot"
	emoji := "🔄"
	if action == "shutdown" {
		cmd = "poweroff"
		emoji = "🛑"
	}

	editMessage(bot, chatID, msgID, fmt.Sprintf("%s *Eseguo %s...*\n\n_Il bot tornerà online dopo il riavvio._", emoji, action), nil)

	go func() {
		time.Sleep(1 * time.Second)
		exec.Command(cmd).Run()
	}()
}

// ═══════════════════════════════════════════════════════════════════
//  REPORT GIORNALIERI (07:30 e 18:30)
// ═══════════════════════════════════════════════════════════════════

// getNextReportTime calcola il prossimo orario di report (07:30 o 18:30)
func getNextReportTime() (time.Time, bool) {
	now := time.Now().In(location)

	// Report mattina (07:30) e sera (18:30)
	morningReport := time.Date(now.Year(), now.Month(), now.Day(), ReportMattina, ReportMinuti, 0, 0, location)
	eveningReport := time.Date(now.Year(), now.Month(), now.Day(), ReportSera, ReportMinuti, 0, 0, location)

	// Determina il prossimo report
	if now.Before(morningReport) {
		return morningReport, true // true = mattina
	} else if now.Before(eveningReport) {
		return eveningReport, false // false = sera
	} else {
		// Dopo le 18:30, prossimo è domani mattina
		return morningReport.Add(24 * time.Hour), true
	}
}

func periodicReport(bot *tgbotapi.BotAPI) {
	// Aspetta che le stats siano pronte
	time.Sleep(IntervalloStats * 2)

	for {
		// Calcola prossimo orario di report
		nextReport, isMorning := getNextReportTime()
		sleepDuration := time.Until(nextReport)

		greeting := "🌅 *Buongiorno!*"
		if !isMorning {
			greeting = "🌆 *Buonasera!*"
		}

		log.Printf("📋 Prossimo report: %s", nextReport.Format("02/01 15:04"))
		time.Sleep(sleepDuration)

		// Genera e invia report
		report := generateDailyReport(greeting)
		msg := tgbotapi.NewMessage(AllowedUserID, report)
		msg.ParseMode = "Markdown"
		bot.Send(msg)

		lastReportTime = time.Now()
		saveState()

		// Pulisci eventi vecchi (mantieni ultimi 2 giorni)
		cleanOldReportEvents()
	}
}

func generateDailyReport(greeting string) string {
	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	// Saluto
	b.WriteString(fmt.Sprintf("%s\n", greeting))
	b.WriteString(fmt.Sprintf("📅 %s\n", now.Format("Monday 02/01/2006")))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Stato generale con emoji grande
	healthIcon, healthText, hasProblems := getHealthStatus(s)
	b.WriteString(fmt.Sprintf("%s *Stato NAS:* %s\n\n", healthIcon, healthText))

	// Se ci sono stati problemi, mostrali per primi
	reportEventsMutex.Lock()
	events := make([]ReportEvent, len(reportEvents))
	copy(events, reportEvents)
	reportEventsMutex.Unlock()

	if len(events) > 0 || hasProblems {
		b.WriteString("📝 *Cosa è successo:*\n")
		if len(events) == 0 {
			b.WriteString("└ _Nessun evento particolare_\n")
		} else {
			for _, e := range events {
				icon := "ℹ️"
				switch e.Type {
				case "warning":
					icon = "⚠️"
				case "critical":
					icon = "🚨"
				case "action":
					icon = "🤖"
				}
				timeStr := e.Time.In(location).Format("15:04")
				b.WriteString(fmt.Sprintf("%s `%s` %s\n", icon, timeStr, e.Message))
			}
		}
		b.WriteString("\n")
	}

	// Risorse attuali (compatto)
	b.WriteString("📊 *Risorse:*\n")
	b.WriteString(fmt.Sprintf("├ CPU: %s `%.0f%%`  RAM: %s `%.0f%%`\n",
		miniBar(s.CPU), s.CPU, miniBar(s.RAM), s.RAM))
	b.WriteString(fmt.Sprintf("└ I/O: %s `%.0f%%`  Swap: `%.0f%%`\n\n",
		miniBar(s.DiskUtil), s.DiskUtil, s.Swap))

	// Storage
	b.WriteString("💾 *Storage:*\n")
	b.WriteString(fmt.Sprintf("├ SSD: %s `%.0f%%` (%s liberi)\n",
		miniBar(s.VolSSD.Used), s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("└ HDD: %s `%.0f%%` (%s liberi)\n\n",
		miniBar(s.VolHDD.Used), s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	// Container Docker
	containers := getContainerList()
	running, stopped := 0, 0
	var stoppedNames []string
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
			stoppedNames = append(stoppedNames, c.Name)
		}
	}
	b.WriteString(fmt.Sprintf("🐳 *Docker:* %d attivi", running))
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d fermi", stopped))
		if len(stoppedNames) <= 3 {
			b.WriteString(fmt.Sprintf(" (%s)", strings.Join(stoppedNames, ", ")))
		}
	}
	b.WriteString("\n\n")

	// Uptime
	b.WriteString(fmt.Sprintf("⏱ *Uptime:* %s\n", formatUptime(s.Uptime)))

	// Footer
	b.WriteString("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	nextReport, isMorning := getNextReportTime()
	nextTime := "stasera 18:30"
	if !isMorning {
		nextTime = "domani 07:30"
	}
	b.WriteString(fmt.Sprintf("_Prossimo report: %s_", nextTime))

	// Se è il report serale, aggiungi buonanotte
	if !strings.Contains(greeting, "Buongiorno") {
		b.WriteString("\n\n🌙 _Buona serata!_")
	}

	_ = nextReport // evita warning unused
	return b.String()
}

func getHealthStatus(s Stats) (icon, text string, hasProblems bool) {
	// Controlla problemi critici
	reportEventsMutex.Lock()
	criticalCount := 0
	warningCount := 0
	for _, e := range reportEvents {
		if e.Type == "critical" {
			criticalCount++
		} else if e.Type == "warning" {
			warningCount++
		}
	}
	reportEventsMutex.Unlock()

	// Valuta stato attuale
	if criticalCount > 0 || s.CPU > SogliaCPU || s.RAM > SogliaRAMCritica || s.VolSSD.Used > 95 || s.VolHDD.Used > 95 {
		return "🔴", "Problemi rilevati", true
	}
	if warningCount > 0 || s.CPU > 80 || s.RAM > 85 || s.DiskUtil > 90 || s.VolSSD.Used > SogliaDisco || s.VolHDD.Used > SogliaDisco {
		return "🟡", "Qualche attenzione", true
	}
	return "🟢", "Tutto OK!", false
}

// generateReport per richieste manuali (/report)
func generateReport(manual bool) string {
	if !manual {
		return generateDailyReport("📋 *Report NAS*")
	}

	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	// Header
	b.WriteString("📋 *REPORT NAS* ─ _su richiesta_\n")
	b.WriteString(fmt.Sprintf("🕐 %s\n", now.Format("02/01/2006 15:04")))
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Stato generale
	healthIcon, healthText, _ := getHealthStatus(s)
	b.WriteString(fmt.Sprintf("%s *Stato:* %s\n\n", healthIcon, healthText))

	// Risorse
	b.WriteString("📊 *RISORSE*\n")
	b.WriteString(fmt.Sprintf("├ CPU:  %s `%.1f%%`\n", miniBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("├ RAM:  %s `%.1f%%` (%s libera)\n", miniBar(s.RAM), s.RAM, formatRAM(s.RAMFreeMB)))
	b.WriteString(fmt.Sprintf("├ I/O:  %s `%.0f%%`\n", miniBar(s.DiskUtil), s.DiskUtil))
	b.WriteString(fmt.Sprintf("└ Swap: %s `%.1f%%`\n\n", miniBar(s.Swap), s.Swap))

	// Storage
	b.WriteString("💾 *STORAGE*\n")
	b.WriteString(fmt.Sprintf("├ SSD: %s `%.1f%%` (%s liberi)\n", miniBar(s.VolSSD.Used), s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("└ HDD: %s `%.1f%%` (%s liberi)\n\n", miniBar(s.VolHDD.Used), s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	// Docker
	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	b.WriteString("🐳 *CONTAINER*\n")
	b.WriteString(fmt.Sprintf("├ Attivi: `%d`\n", running))
	b.WriteString(fmt.Sprintf("└ Fermi:  `%d`\n\n", stopped))

	// Uptime
	b.WriteString(fmt.Sprintf("⏱ *Uptime:* `%s`\n\n", formatUptime(s.Uptime)))

	// Eventi
	reportEventsMutex.Lock()
	events := make([]ReportEvent, len(reportEvents))
	copy(events, reportEvents)
	reportEventsMutex.Unlock()

	if len(events) > 0 {
		b.WriteString("📝 *EVENTI RECENTI*\n")
		for _, e := range events {
			icon := "ℹ️"
			switch e.Type {
			case "warning":
				icon = "⚠️"
			case "critical":
				icon = "🚨"
			case "action":
				icon = "🤖"
			}
			b.WriteString(fmt.Sprintf("%s `%s` %s\n", icon, e.Time.In(location).Format("15:04"), e.Message))
		}
		b.WriteString("\n")
	} else {
		b.WriteString("📝 _Nessun evento significativo_\n\n")
	}

	// Footer
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	nextReport, isMorning := getNextReportTime()
	nextTime := "stasera 18:30"
	if !isMorning {
		nextTime = "domani 07:30"
	}
	b.WriteString(fmt.Sprintf("_Prossimo report automatico: %s_", nextTime))
	_ = nextReport

	return b.String()
}

func addReportEvent(eventType, message string) {
	reportEventsMutex.Lock()
	defer reportEventsMutex.Unlock()

	reportEvents = append(reportEvents, ReportEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
	})

	// Limita a 20 eventi
	if len(reportEvents) > 20 {
		reportEvents = reportEvents[len(reportEvents)-20:]
	}
}

func cleanOldReportEvents() {
	reportEventsMutex.Lock()
	defer reportEventsMutex.Unlock()

	// Mantieni eventi delle ultime 24 ore
	cutoff := time.Now().Add(-24 * time.Hour)
	var newEvents []ReportEvent
	for _, e := range reportEvents {
		if e.Time.After(cutoff) {
			newEvents = append(newEvents, e)
		}
	}
	reportEvents = newEvents
}

func miniBar(percent float64) string {
	if percent > 90 {
		return "🔴"
	}
	if percent > 70 {
		return "🟡"
	}
	return "🟢"
}

// ═══════════════════════════════════════════════════════════════════
//  AUTONOMOUS MANAGER (decisioni automatiche)
// ═══════════════════════════════════════════════════════════════════

func autonomousManager(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		statsMutex.RLock()
		s := statsCache
		ready := statsReady
		statsMutex.RUnlock()

		if !ready {
			continue
		}

		// Check stress I/O prolungato
		checkIOStress(bot, s)

		// Check RAM critica
		if s.RAM >= SogliaRAMCritica {
			handleCriticalRAM(bot, s)
		}

		// Pulizia restart counter (ogni ora)
		cleanRestartCounter()
	}
}

func checkIOStress(bot *tgbotapi.BotAPI, s Stats) {
	if s.DiskUtil >= SogliaIOCritico {
		if stressStartTime.IsZero() {
			stressStartTime = time.Now()
			consecutiveStress = 1
		} else {
			consecutiveStress++
		}

		// Stress prolungato (>2 minuti)?
		if time.Since(stressStartTime) >= DurataStressDisco && !stressNotified {
			// Notifica SOLO se stress prolungato
			msg := fmt.Sprintf("⚠️ *I/O Disco Elevato*\n\n"+
				"💾 Utilizzo I/O: `%.0f%%`\n"+
				"⏱ Durata: `%s`\n"+
				"📖 R: `%.1f` MB/s │ 📝 W: `%.1f` MB/s\n\n"+
				"_Monitoraggio attivo..._",
				s.DiskUtil,
				time.Since(stressStartTime).Round(time.Second),
				s.ReadMBs, s.WriteMBs)

			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)

			stressNotified = true
			addReportEvent("warning", fmt.Sprintf("I/O elevato (%.0f%%) per %s", s.DiskUtil, time.Since(stressStartTime).Round(time.Second)))

			// Se molto prolungato (>5 min), prova azioni
			if time.Since(stressStartTime) >= 5*time.Minute {
				tryMitigateIOStress(bot)
			}
		}
	} else {
		// I/O tornato normale
		if !stressStartTime.IsZero() && stressNotified {
			duration := time.Since(stressStartTime).Round(time.Second)
			msg := fmt.Sprintf("✅ *I/O Normalizzato*\n\nDurata stress: `%s`", duration)
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			addReportEvent("info", fmt.Sprintf("I/O normalizzato dopo %s", duration))
		}
		stressStartTime = time.Time{}
		stressNotified = false
		consecutiveStress = 0
	}
}

func tryMitigateIOStress(bot *tgbotapi.BotAPI) {
	// Trova container con alto I/O (quelli che potrebbero causare il problema)
	containers := getContainerList()
	for _, c := range containers {
		if !c.Running {
			continue
		}

		// Controlla se container usa molte risorse
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.BlockIO}}", c.Name)
		out, _ := cmd.Output()
		cancel()

		blockIO := strings.TrimSpace(string(out))
		// Se il container ha molto I/O, potrebbe essere candidato per restart
		if strings.Contains(blockIO, "GB") {
			log.Printf("🔍 Container %s con alto BlockIO: %s", c.Name, blockIO)
		}
	}
}

func handleCriticalRAM(bot *tgbotapi.BotAPI, s Stats) {
	// RAM critica - trova processi/container che consumano di più
	containers := getContainerList()

	type containerMem struct {
		name   string
		memPct float64
	}

	var heavyContainers []containerMem
	for _, c := range containers {
		if !c.Running {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.MemPerc}}", c.Name)
		out, _ := cmd.Output()
		cancel()

		memStr := strings.TrimSuffix(strings.TrimSpace(string(out)), "%")
		if memPct, err := strconv.ParseFloat(memStr, 64); err == nil && memPct > 20 {
			heavyContainers = append(heavyContainers, containerMem{c.Name, memPct})
		}
	}

	// Se RAM >98% e abbiamo container pesanti, considera restart
	if s.RAM >= 98 && len(heavyContainers) > 0 {
		// Ordina per consumo
		sort.Slice(heavyContainers, func(i, j int) bool {
			return heavyContainers[i].memPct > heavyContainers[j].memPct
		})

		// Prova a riavviare il container più pesante (se non già fatto di recente)
		target := heavyContainers[0]
		if canAutoRestart(target.name) {
			log.Printf("🤖 RAM critica (%.1f%%), riavvio automatico: %s (%.1f%%)", s.RAM, target.name, target.memPct)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "docker", "restart", target.name)
			err := cmd.Run()
			cancel()

			recordAutoRestart(target.name)

			var msgText string
			if err != nil {
				msgText = fmt.Sprintf("🤖 *Azione Automatica Fallita*\n\n"+
					"RAM critica: `%.1f%%`\n"+
					"Tentato restart: `%s`\n"+
					"Errore: %v", s.RAM, target.name, err)
				addReportEvent("critical", fmt.Sprintf("Restart auto fallito: %s (%v)", target.name, err))
			} else {
				msgText = fmt.Sprintf("🤖 *Azione Automatica Eseguita*\n\n"+
					"RAM critica: `%.1f%%`\n"+
					"Container riavviato: `%s`\n"+
					"Consumo memoria: `%.1f%%`\n\n"+
					"_Monitoraggio attivo..._", s.RAM, target.name, target.memPct)
				addReportEvent("action", fmt.Sprintf("Restart auto: %s (RAM %.1f%%)", target.name, s.RAM))
			}

			msg := tgbotapi.NewMessage(AllowedUserID, msgText)
			msg.ParseMode = "Markdown"
			bot.Send(msg)
		}
	}
}

func canAutoRestart(containerName string) bool {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	restarts := autoRestarts[containerName]
	cutoff := time.Now().Add(-1 * time.Hour)

	// Conta restart nell'ultima ora
	count := 0
	for _, t := range restarts {
		if t.After(cutoff) {
			count++
		}
	}

	return count < MaxRestartContainer
}

func recordAutoRestart(containerName string) {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	autoRestarts[containerName] = append(autoRestarts[containerName], time.Now())
	saveState()
}

func cleanRestartCounter() {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	cutoff := time.Now().Add(-2 * time.Hour)
	for name, times := range autoRestarts {
		var newTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				newTimes = append(newTimes, t)
			}
		}
		if len(newTimes) == 0 {
			delete(autoRestarts, name)
		} else {
			autoRestarts[name] = newTimes
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  SPEEDTEST
// ═══════════════════════════════════════════════════════════════════

func runSpeedtest(bot *tgbotapi.BotAPI, chatID int64) {
	initMsg, _ := bot.Send(tgbotapi.NewMessage(chatID, "🚀 *Speedtest in corso...*\n\n⏳ Download 100MB da Hetzner..."))

	start := time.Now()
	resp, err := http.Get("https://fsn1-speed.hetzner.com/100MB.bin")
	if err != nil {
		editMessage(bot, chatID, initMsg.MessageID, "❌ *Errore connessione*", nil)
		return
	}
	defer resp.Body.Close()

	written, _ := io.Copy(io.Discard, resp.Body)
	duration := time.Since(start)
	mbps := (float64(written) * 8) / duration.Seconds() / 1_000_000

	var b strings.Builder
	b.WriteString("🚀 *SPEEDTEST*\n\n")
	b.WriteString(fmt.Sprintf("📦 Scaricati: `%d MB`\n", written/1024/1024))
	b.WriteString(fmt.Sprintf("⏱ Tempo: `%.1f s`\n", duration.Seconds()))
	b.WriteString(fmt.Sprintf("💨 Velocità: `%.1f Mbps`\n\n", mbps))

	if mbps > 100 {
		b.WriteString("✅ _Ottima connessione!_")
	} else if mbps > 50 {
		b.WriteString("🟡 _Connessione buona_")
	} else if mbps > 20 {
		b.WriteString("🟠 _Connessione sufficiente_")
	} else {
		b.WriteString("🔴 _Connessione lenta_")
	}

	editMessage(bot, chatID, initMsg.MessageID, b.String(), nil)
}

// ═══════════════════════════════════════════════════════════════════
//  BACKGROUND STATS COLLECTOR (ottimizzato per NAS lento)
// ═══════════════════════════════════════════════════════════════════

func statsCollector() {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	ticker := time.NewTicker(IntervalloStats)
	defer ticker.Stop()

	for {
		c, _ := cpu.Percent(0, false)
		v, _ := mem.VirtualMemory()
		sw, _ := mem.SwapMemory()
		l, _ := load.Avg()
		h, _ := host.Info()
		dSSD, _ := disk.Usage(PathSSD)
		dHDD, _ := disk.Usage(PathHDD)

		currentIO, _ := disk.IOCounters()
		var readMBs, writeMBs, diskUtil float64
		if lastIO != nil && !lastIOTime.IsZero() {
			elapsed := time.Since(lastIOTime).Seconds()
			if elapsed > 0 {
				var rBytes, wBytes uint64
				var maxUtil float64
				for k, curr := range currentIO {
					if prev, ok := lastIO[k]; ok {
						rBytes += curr.ReadBytes - prev.ReadBytes
						wBytes += curr.WriteBytes - prev.WriteBytes
						deltaIOTime := curr.IoTime - prev.IoTime
						util := float64(deltaIOTime) / (elapsed * 10)
						if util > 100 {
							util = 100
						}
						if util > maxUtil {
							maxUtil = util
						}
					}
				}
				readMBs = float64(rBytes) / elapsed / 1024 / 1024
				writeMBs = float64(wBytes) / elapsed / 1024 / 1024
				diskUtil = maxUtil
			}
		}
		lastIO = currentIO
		lastIOTime = time.Now()

		topCPU, topRAM := getTopProcesses(5)

		newStats := Stats{
			CPU:        safeFloat(c, 0),
			RAM:        v.UsedPercent,
			RAMFreeMB:  v.Available / 1024 / 1024,
			RAMTotalMB: v.Total / 1024 / 1024,
			Swap:       sw.UsedPercent,
			Load1m:     l.Load1,
			Load5m:     l.Load5,
			Load15m:    l.Load15,
			Uptime:     h.Uptime,
			ReadMBs:    readMBs,
			WriteMBs:   writeMBs,
			DiskUtil:   diskUtil,
			TopCPU:     topCPU,
			TopRAM:     topRAM,
		}

		if dSSD != nil {
			newStats.VolSSD = VolumeStats{Used: dSSD.UsedPercent, Free: dSSD.Free}
		}
		if dHDD != nil {
			newStats.VolHDD = VolumeStats{Used: dHDD.UsedPercent, Free: dHDD.Free}
		}

		statsMutex.Lock()
		statsCache = newStats
		statsReady = true
		statsMutex.Unlock()

		<-ticker.C
	}
}

func getTopProcesses(limit int) (topCPU, topRAM []ProcInfo) {
	ps, err := process.Processes()
	if err != nil {
		return nil, nil
	}

	var list []ProcInfo
	for _, p := range ps {
		name, _ := p.Name()
		memP, _ := p.MemoryPercent()
		cpuP, _ := p.CPUPercent()
		if name != "" && (memP > 0.1 || cpuP > 0.1) {
			list = append(list, ProcInfo{Name: name, Mem: float64(memP), Cpu: cpuP})
		}
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Cpu > list[j].Cpu })
	if len(list) > limit {
		topCPU = append([]ProcInfo{}, list[:limit]...)
	} else {
		topCPU = append([]ProcInfo{}, list...)
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Mem > list[j].Mem })
	if len(list) > limit {
		topRAM = append([]ProcInfo{}, list[:limit]...)
	} else {
		topRAM = append([]ProcInfo{}, list...)
	}

	return topCPU, topRAM
}

// ═══════════════════════════════════════════════════════════════════
//  MONITOR ALLARMI (solo critici, no spam)
// ═══════════════════════════════════════════════════════════════════

var lastCriticalAlert time.Time

func monitorAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(IntervalloMonitor)
	defer ticker.Stop()

	for range ticker.C {
		statsMutex.RLock()
		s := statsCache
		ready := statsReady
		statsMutex.RUnlock()

		if !ready {
			continue
		}

		// Solo allarmi CRITICI immediati (disco pieno, SMART failure)
		var criticalAlerts []string

		// Disco quasi pieno (urgente!)
		if s.VolSSD.Used >= 95 {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 *SSD CRITICO:* `%.1f%%` — Spazio quasi esaurito!", s.VolSSD.Used))
		}
		if s.VolHDD.Used >= 95 {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 *HDD CRITICO:* `%.1f%%` — Spazio quasi esaurito!", s.VolHDD.Used))
		}

		// Check SMART dischi (ogni 30 sec è ok, è una lettura leggera)
		for _, dev := range []string{"sda", "sdb"} {
			_, health := readDiskSMART(dev)
			if strings.Contains(strings.ToUpper(health), "FAIL") {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 *DISCO %s FAILING!* — Backup immediato consigliato!", dev))
			}
		}

		// Invia solo alert critici (con cooldown di 30 min per evitare spam)
		if len(criticalAlerts) > 0 && time.Since(lastCriticalAlert).Minutes() >= 30 {
			msg := "🚨 *ALLARME CRITICO NAS*\n\n" + strings.Join(criticalAlerts, "\n")
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			lastCriticalAlert = time.Now()

			for _, alert := range criticalAlerts {
				addReportEvent("critical", alert)
			}
		}

		// Registra warning per il report (non notifica immediata)
		if s.CPU >= SogliaCPU {
			addReportEvent("warning", fmt.Sprintf("CPU elevata: %.1f%%", s.CPU))
		}
		if s.RAM >= SogliaRAM && s.RAM < SogliaRAMCritica {
			addReportEvent("warning", fmt.Sprintf("RAM elevata: %.1f%%", s.RAM))
		}
		if s.VolSSD.Used >= SogliaDisco && s.VolSSD.Used < 95 {
			addReportEvent("warning", fmt.Sprintf("SSD: %.1f%% utilizzato", s.VolSSD.Used))
		}
		if s.VolHDD.Used >= SogliaDisco && s.VolHDD.Used < 95 {
			addReportEvent("warning", fmt.Sprintf("HDD: %.1f%% utilizzato", s.VolHDD.Used))
		}
	}
}

// ═══════════════════════════════════════════════════════════════════
//  HELPERS
// ═══════════════════════════════════════════════════════════════════

type VolumeStats struct {
	Used float64
	Free uint64
}

type Stats struct {
	CPU, RAM, Swap              float64
	RAMFreeMB, RAMTotalMB       uint64
	Load1m, Load5m, Load15m     float64
	Uptime                      uint64
	VolSSD, VolHDD              VolumeStats
	ReadMBs, WriteMBs, DiskUtil float64
	TopCPU, TopRAM              []ProcInfo
}

type ProcInfo struct {
	Name string
	Mem  float64
	Cpu  float64
}

func progressBar(percent float64, width int) string {
	if percent > 100 {
		percent = 100
	}
	if percent < 0 {
		percent = 0
	}
	filled := int(percent / 100 * float64(width))
	empty := width - filled
	return strings.Repeat("▰", filled) + strings.Repeat("▱", empty)
}

func getIcon(val, warn, crit float64) string {
	if val >= crit {
		return "🔴"
	}
	if val >= warn {
		return "🟡"
	}
	return "🟢"
}

func formatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	mins := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}

func formatBytes(bytes uint64) string {
	gb := float64(bytes) / 1024 / 1024 / 1024
	if gb >= 1000 {
		return fmt.Sprintf("%.1f TB", gb/1024)
	}
	return fmt.Sprintf("%.1f GB", gb)
}

func formatRAM(mb uint64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%d MB", mb)
}

func formatTopProcs(procs []ProcInfo, metric string) string {
	if len(procs) == 0 {
		return "```\n N/A\n```"
	}
	var b strings.Builder
	b.WriteString("```\n")
	for _, p := range procs {
		name := truncate(p.Name, 12)
		if metric == "cpu" {
			b.WriteString(fmt.Sprintf("%-12s %5.1f%%\n", name, p.Cpu))
		} else {
			b.WriteString(fmt.Sprintf("%-12s %5.1f%%\n", name, p.Mem))
		}
	}
	b.WriteString("```")
	return b.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func safeFloat(arr []float64, def float64) float64 {
	if len(arr) > 0 {
		return arr[0]
	}
	return def
}

func readCPUTemp() float64 {
	raw, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	val, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return float64(val) / 1000.0
}

func readDiskSMART(device string) (temp int, health string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "smartctl", "-A", "/dev/"+device)
	out, _ := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Temperature_Celsius") || strings.Contains(line, "Temperature_Internal") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				temp, _ = strconv.Atoi(fields[9])
			}
		}
	}

	cmd = exec.CommandContext(ctx, "smartctl", "-H", "/dev/"+device)
	out, _ = cmd.Output()
	health = "OK"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "PASSED") {
			health = "PASSED"
		} else if strings.Contains(line, "FAILED") {
			health = "FAILED!"
		}
	}

	return temp, health
}

func sendMarkdown(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func sendWithKeyboard(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = getMainKeyboard()
	bot.Send(msg)
}

func editMessage(bot *tgbotapi.BotAPI, chatID int64, msgID int, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = "Markdown"
	if keyboard != nil {
		edit.ReplyMarkup = keyboard
	}
	bot.Send(edit)
}

func getMainKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData("🌡 Temp", "show_temp"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Docker", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("📈 Stats", "show_dstats"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 Report", "show_report"),
			tgbotapi.NewInlineKeyboardButtonData("🌐 Rete", "show_net"),
		),
	)
}
