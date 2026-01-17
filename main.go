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
	// Intervalli background
	IntervalloStats     = 5 * time.Second  // Aggiorna cache stats
	IntervalloMonitor   = 30 * time.Second // Check allarmi
	DurataStressDisco   = 2 * time.Minute  // Soglia stress I/O prolungato
	SogliaIOCritico     = 95.0             // % I/O per considerare stress
	SogliaCPUStress     = 90.0             // CPU stress threshold
	SogliaRAMStress     = 90.0             // RAM stress threshold
	SogliaSwapStress    = 50.0             // Swap stress threshold
	SogliaRAMCritica    = 95.0             // RAM critica per azioni autonome
	MaxRestartContainer = 3                // Max riavvii automatici container/ora

	// Quiet hours (niente notifiche)
	QuietHourStart   = 23 // 23:30 inizio silenzio
	QuietHourEnd     = 7  // 07:00 fine silenzio
	QuietMinuteStart = 30

	// Orari report giornalieri (Europe/Rome)
	ReportMattina = 7  // 07:30
	ReportSera    = 18 // 18:30
	ReportMinuti  = 30
	Timezone      = "Europe/Rome"

	// Stato persistente (default, sovrascritto se necessario)
	StateFile = "nasbot_state.json"
)

var (
	BotToken      string
	AllowedUserID int64

	// Configurazione Runtime (da config.json)
	PathSSD     = "/Volume1"
	PathHDD     = "/Volume2"
	SogliaCPU   = 90.0
	SogliaRAM   = 90.0
	SogliaDisco = 90.0

	// Cache globale con mutex
	statsCache Stats
	statsMutex sync.RWMutex
	statsReady bool

	// Tracking stress I/O (HDD)
	stressStartTime   time.Time
	stressNotified    bool
	consecutiveStress int

	// Tracking stress per tutte le risorse
	resourceStress      map[string]*StressTracker
	resourceStressMutex sync.Mutex

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

	// Beta Features variables
	dockerFailureStart time.Time // Quando abbiamo iniziato a non vedere container
	pruneDoneToday     bool      // Se il prune settimanale è stato fatto
)

// ReportEvent traccia eventi per il report periodico
type ReportEvent struct {
	Time    time.Time
	Type    string // "warning", "critical", "action", "info"
	Message string
}

// StressTracker traccia periodi di stress per una risorsa
type StressTracker struct {
	CurrentStart  time.Time     // Inizio stress corrente (zero se non in stress)
	StressCount   int           // Numero di volte sotto stress dall'ultimo report
	LongestStress time.Duration // Durata massima stress
	TotalStress   time.Duration // Tempo totale sotto stress
	Notified      bool          // Se già notificato per questo stress
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
	loadConfig()

	// Inizializza timezone
	var err error
	location, err = time.LoadLocation(Timezone)
	if err != nil {
		log.Printf("[w] Timezone %s non trovata, uso UTC", Timezone)
		location = time.UTC
	}

	// Inizializza mappe
	autoRestarts = make(map[string][]time.Time)
	resourceStress = make(map[string]*StressTracker)
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		resourceStress[res] = &StressTracker{}
	}

	// Carica stato persistente
	loadState()
}

// loadConfig legge i token dal file config.json
func loadConfig() {
	type Paths struct {
		SSD string `json:"ssd"`
		HDD string `json:"hdd"`
	}
	type Thresholds struct {
		CPU  float64 `json:"cpu"`
		RAM  float64 `json:"ram"`
		Disk float64 `json:"disk"`
	}
	type Config struct {
		BotToken      string     `json:"bot_token"`
		AllowedUserID int64      `json:"allowed_user_id"`
		Paths         Paths      `json:"paths"`
		Thresholds    Thresholds `json:"thresholds"`
	}

	data, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("❌ Errore lettura config.json: %v\nCrea il file copiando config.example.json", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("❌ Errore parsing config.json: %v", err)
	}

	BotToken = cfg.BotToken
	AllowedUserID = cfg.AllowedUserID

	// Sovrascrivi default se presenti nel json
	if cfg.Paths.SSD != "" {
		PathSSD = cfg.Paths.SSD
	}
	if cfg.Paths.HDD != "" {
		PathHDD = cfg.Paths.HDD
	}

	if cfg.Thresholds.CPU > 0 {
		SogliaCPU = cfg.Thresholds.CPU
	}
	if cfg.Thresholds.RAM > 0 {
		SogliaRAM = cfg.Thresholds.RAM
	}
	if cfg.Thresholds.Disk > 0 {
		SogliaDisco = cfg.Thresholds.Disk
	}

	if BotToken == "" {
		log.Fatal("❌ bot_token vuoto in config.json")
	}
	if AllowedUserID == 0 {
		log.Fatal("❌ allowed_user_id vuoto o invalido in config.json")
	}

	log.Println("[✓] Config caricata da config.json")
}

// isQuietHours verifica se siamo in orario notturno (23:30 - 07:00)
func isQuietHours() bool {
	now := time.Now().In(location)
	hour := now.Hour()
	minute := now.Minute()

	// Dopo le 23:30
	if hour == QuietHourStart && minute >= QuietMinuteStart {
		return true
	}
	// Dalle 00:00 alle 06:59
	if hour > QuietHourStart || hour < QuietHourEnd {
		return true
	}
	return false
}

func loadState() {
	data, err := os.ReadFile(StateFile)
	if err != nil {
		log.Printf("[i] Primo avvio - nessuno stato")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[w] Errore stato: %v", err)
		return
	}
	lastReportTime = state.LastReportTime
	if state.AutoRestarts != nil {
		autoRestarts = state.AutoRestarts
	}
	log.Printf("[+] Stato ripristinato")
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
		log.Printf("[w] Serialize: %v", err)
		return
	}
	if err := os.WriteFile(StateFile, data, 0644); err != nil {
		log.Printf("[w] Save: %v", err)
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[!] PANIC: %v\n%s", r, debug.Stack())
			saveState()
		}
	}()

	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		log.Fatalf("[!] Avvio bot: %v", err)
	}
	log.Printf("[+] NASBot @%s", bot.Self.UserName)

	// Notifica avvio
	nextReport, isMorning := getNextReportTime()
	nextReportStr := "18:30"
	if isMorning {
		nextReportStr = "07:30"
	}

	startupText := fmt.Sprintf(`*NASBot is online* 👋

I'll keep an eye on things.
Next report at %s

_Type /help to see what I can do_`, nextReportStr)

	_ = nextReport
	startupMsg := tgbotapi.NewMessage(AllowedUserID, startupText)
	startupMsg.ParseMode = "Markdown"
	bot.Send(startupMsg)

	// Gestione shutdown graceful
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("[-] Shutdown")
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
		bot.Send(tgbotapi.NewMessage(chatID, "Hmm, I don't know that one. Try /help"))
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
		editMessage(bot, chatID, msgID, "\u2717 Annullato", nil)
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
		return "_loading..._"
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf("🖥 *NAS* at %s\n\n", time.Now().Format("15:04")))

	b.WriteString(fmt.Sprintf("🧠 CPU %s %2.0f%%\n", makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("💾 RAM %s %2.0f%%\n", makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("🔄 Swap %s %2.0f%%\n", makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\n💿 SSD %2.0f%% · %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("🗄 HDD %2.0f%% · %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	if s.DiskUtil > 10 {
		b.WriteString(fmt.Sprintf("\n📡 Disk I/O at %.0f%%", s.DiskUtil))
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			b.WriteString(fmt.Sprintf(" (R %.0f / W %.0f MB/s)", s.ReadMBs, s.WriteMBs))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf("\n_⏱ Running for %s_", formatUptime(s.Uptime)))

	return b.String()
}

// makeProgressBar creates a 10-step visual progress bar
func makeProgressBar(percent float64) string {
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}

	// Arrotonda al 10% più vicino (55% -> 60% -> 6 tacche)
	filled := int((percent + 5) / 10)
	if filled > 10 {
		filled = 10
	}

	// Usa caratteri block per la barra
	return strings.Repeat("█", filled) + strings.Repeat("░", 10-filled)
}

func getTempText() string {
	var b strings.Builder
	b.WriteString("🌡 *Temperatures*\n\n")

	cpuTemp := readCPUTemp()
	cpuIcon := "✅"
	cpuStatus := "looking good"
	if cpuTemp > 60 {
		cpuIcon = "🟡"
		cpuStatus = "a bit warm"
	}
	if cpuTemp > 75 {
		cpuIcon = "🔥"
		cpuStatus = "running hot!"
	}
	b.WriteString(fmt.Sprintf("%s CPU: %.0f°C — %s\n\n", cpuIcon, cpuTemp, cpuStatus))

	b.WriteString("*Disks*\n")
	for _, dev := range []string{"sda", "sdb"} {
		temp, health := readDiskSMART(dev)
		icon := "✅"
		status := "healthy"
		if strings.Contains(strings.ToUpper(health), "FAIL") {
			icon = "🚨"
			status = "FAILING!"
		} else if temp > 45 {
			icon = "🟡"
			status = "warm"
		}
		b.WriteString(fmt.Sprintf("%s %s: %d°C — %s\n", icon, dev, temp, status))
	}
	return b.String()
}

func getDockerText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "*timeout*"
		}
		return "*docker n/a*"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "_No containers found_"
	}

	var b strings.Builder
	b.WriteString("*Container*\n────────────────────\n")

	running, stopped := 0, 0
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		name := truncate(parts[0], 14)
		status := parts[1]

		icon := "-"
		statusShort := "off"
		if strings.Contains(status, "Up") {
			icon = "+"
			statusParts := strings.Fields(status)
			if len(statusParts) >= 2 {
				statusShort = statusParts[1]
				if len(statusParts) >= 3 && len(statusParts[2]) > 0 {
					statusShort += string(statusParts[2][0])
				}
			}
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("\n%s %-14s %s", icon, name, statusShort))
	}

	b.WriteString(fmt.Sprintf("\n\nContainers: %d running · %d stopped", running, stopped))
	return b.String()
}

func getDockerStatsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "*timeout*"
		}
		return "*stats n/a*"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "_No containers running_"
	}

	var b strings.Builder
	b.WriteString("*Container resources*\n```\n")
	b.WriteString(fmt.Sprintf("%-12s %5s %5s %s\n", "NAME", "CPU", "MEM%", "MEM"))

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

		b.WriteString(fmt.Sprintf("%-12s %5s %5s %s\n", name, cpuP, memP, memShort))
	}
	b.WriteString("```")

	return b.String()
}

func getNetworkText() string {
	var b strings.Builder
	b.WriteString("🌐 *Network*\n\n")

	localIP := "n/a"
	if out, err := exec.Command("hostname", "-I").Output(); err == nil {
		ips := strings.Fields(string(out))
		if len(ips) > 0 {
			localIP = ips[0]
		}
	}
	b.WriteString(fmt.Sprintf("🏠 Local: `%s`\n", localIP))

	publicIP := "checking..."
	client := http.Client{Timeout: 3 * time.Second}
	if resp, err := client.Get("https://api.ipify.org"); err == nil {
		defer resp.Body.Close()
		if body, err := io.ReadAll(resp.Body); err == nil {
			publicIP = string(body)
		}
	}
	b.WriteString(fmt.Sprintf("🌍 Public: `%s`\n", publicIP))

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

	return fmt.Sprintf("*Recent system logs*\n```\n%s\n```", recentLogs)
}

func getHelpText() string {
	return `👋 *Hey! I'm NASBot*

Here's what I can do for you:

📊 /status — quick system overview
🌡 /temp — check temperatures
🐳 /docker — manage containers
📊 /dstats — container resources
🌐 /net — network info
📋 /report — full detailed report
⚡ /reboot · /shutdown

_📨 Daily reports at 7:30 & 18:30_
_🌙 Quiet hours: 23:30 — 7:00_`
}

// ═══════════════════════════════════════════════════════════════════
//  GESTIONE CONTAINER DOCKER
// ═══════════════════════════════════════════════════════════════════

func getContainerList() []ContainerInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}|{{.ID}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("x Docker error: %v - Output: %s", err, string(out))
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
		return "_No containers found. Is Docker running?_", &mainKb
	}

	var b strings.Builder
	b.WriteString("🐳 *Containers*\n\n")

	running, stopped := 0, 0
	for _, c := range containers {
		icon := "⏸"
		statusText := "stopped"
		if c.Running {
			icon = "▶️"
			statusText = parseUptime(c.Status)
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("%s *%s* — %s\n", icon, c.Name, statusText))
	}

	b.WriteString(fmt.Sprintf("\n_%d running, %d stopped_", running, stopped))

	// Bottoni - 2 per riga
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(containers); i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		for j := 0; j < 2 && i+j < len(containers); j++ {
			c := containers[i+j]
			icon := "⏸"
			if c.Running {
				icon = "▶"
			}
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s %s", icon, truncate(c.Name, 10)),
				"container_select_"+c.Name,
			))
		}
		rows = append(rows, row)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "show_docker"),
		tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "back_main"),
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

	switch action {
	case "select", "start", "stop", "restart", "logs", "cancel":
		// Nome container è tutto dopo parts[1]
		containerName := strings.Join(parts[2:], "_")
		switch action {
		case "select":
			showContainerActions(bot, chatID, msgID, containerName)
		case "start", "stop", "restart", "logs":
			confirmContainerAction(bot, chatID, msgID, containerName, action)
		case "cancel":
			text, kb := getDockerMenuText()
			editMessage(bot, chatID, msgID, text, kb)
		}
	case "confirm":
		// Format: container_confirm_CONTAINERNAME_ACTION
		// L'azione è l'ultimo elemento, il nome è tutto il resto
		if len(parts) < 4 {
			return
		}
		containerAction := parts[len(parts)-1]
		containerName := strings.Join(parts[2:len(parts)-1], "_")
		executeContainerAction(bot, chatID, msgID, containerName, containerAction)
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
		editMessage(bot, chatID, msgID, "❓ Container not found", nil)
		return
	}

	var b strings.Builder
	icon := "⏸"
	statusText := "stopped"
	if container.Running {
		icon = "▶️"
		statusText = parseUptime(container.Status)
	}

	b.WriteString(fmt.Sprintf("%s *%s*\n\n", icon, container.Name))
	b.WriteString(fmt.Sprintf("Status: %s\n", statusText))
	b.WriteString(fmt.Sprintf("Image: `%s`\n", truncate(container.Image, 20)))
	b.WriteString(fmt.Sprintf("ID: `%s`\n", container.ID[:12]))

	if container.Running {
		stats := getContainerStats(containerName)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n%s", stats))
		}
	}

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
		tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "show_docker"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

func confirmContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	if action == "logs" {
		showContainerLogs(bot, chatID, msgID, containerName)
		return
	}

	actionText := map[string]string{
		"start":   "▶️ Start",
		"stop":    "⏹ Stop",
		"restart": "🔄 Restart",
	}[action]

	text := fmt.Sprintf("%s *%s*?", actionText, containerName)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes", fmt.Sprintf("container_confirm_%s_%s", containerName, action)),
			tgbotapi.NewInlineKeyboardButtonData("❌ No", "container_cancel_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

func executeContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	editMessage(bot, chatID, msgID, fmt.Sprintf("... `%s` %s", containerName, action), nil)

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

	output, err := cmd.CombinedOutput()
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf("❌ Couldn't %s *%s*\n`%s`", action, containerName, errMsg)
		addReportEvent("warning", fmt.Sprintf("Error %s container %s: %s", action, containerName, errMsg))
	} else {
		actionPast := map[string]string{"start": "started ▶️", "stop": "stopped ⏹", "restart": "restarted 🔄"}[action]
		resultText = fmt.Sprintf("✅ *%s* %s", containerName, actionPast)
		addReportEvent("info", fmt.Sprintf("Container %s: %s (manual)", containerName, action))
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Containers", "show_docker"),
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
		text = fmt.Sprintf("Couldn't read logs: %v", err)
	} else {
		logs := string(out)
		if len(logs) > 3500 {
			logs = logs[len(logs)-3500:]
		}
		if logs == "" {
			logs = "(no logs available)"
		}
		text = fmt.Sprintf("*Logs for %s*\n```\n%s\n```", containerName, logs)
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "container_logs_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "container_select_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

func getContainerStats(containerName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("x Container `%s` non trovato.", args)))
}

func getContainerInfoText(c ContainerInfo) (string, *tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	icon := "x"
	if c.Running {
		icon = ">"
	}
	b.WriteString(fmt.Sprintf("> *%s* %s\n", c.Name, icon))
	b.WriteString(fmt.Sprintf("> `%s`\n", c.Image))
	b.WriteString(fmt.Sprintf("> %s\n", c.Status))

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
		return "stopped"
	}
	parts := strings.Fields(status)
	if len(parts) >= 2 {
		result := parts[1]
		if len(parts) >= 3 {
			result += " " + parts[2]
		}
		return result
	}
	return "running"
}

// ═══════════════════════════════════════════════════════════════════
//  POWER MANAGEMENT (con conferma)
// ═══════════════════════════════════════════════════════════════════

func sendPowerConfirm(bot *tgbotapi.BotAPI, chatID int64, action string) {
	pendingActionMutex.Lock()
	pendingAction = action
	pendingActionMutex.Unlock()

	question := "🔄 *Reboot* the NAS?"
	if action == "shutdown" {
		question = "⚠️ *Shut down* the NAS?"
	}

	msg := tgbotapi.NewMessage(chatID, question)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, do it", "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_power"),
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
		editMessage(bot, chatID, msgID, "_Session expired — try again_", nil)
		return
	}

	cmd := "reboot"
	actionMsg := "Rebooting now..."
	if action == "shutdown" {
		cmd = "poweroff"
		actionMsg = "Shutting down... See you later!"
	}

	editMessage(bot, chatID, msgID, actionMsg, nil)

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

		greeting := "Good morning! ☀️"
		if !isMorning {
			greeting = "Good evening! 🌙"
		}

		log.Printf("> Prossimo report: %s", nextReport.Format("02/01 15:04"))
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

	b.WriteString(fmt.Sprintf("*%s*\n", greeting))
	b.WriteString(fmt.Sprintf("_%s_\n\n", now.Format("Mon 02/01")))

	healthIcon, healthText, _ := getHealthStatus(s)
	b.WriteString(fmt.Sprintf("%s %s\n\n", healthIcon, healthText))

	reportEventsMutex.Lock()
	events := filterSignificantEvents(reportEvents)
	reportEventsMutex.Unlock()

	if len(events) > 0 {
		b.WriteString("*Events*\n")
		for _, e := range events {
			icon := "·"
			switch e.Type {
			case "warning":
				icon = "~"
			case "critical":
				icon = "!"
			case "action":
				icon = ">"
			}
			timeStr := e.Time.In(location).Format("15:04")
			b.WriteString(fmt.Sprintf("%s %s %s\n", icon, timeStr, truncate(e.Message, 28)))
		}
		b.WriteString("\n")
	}

	b.WriteString("*Resources*\n")
	b.WriteString(fmt.Sprintf("🧠 CPU %s %2.0f%%\n", makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("💾 RAM %s %2.0f%%\n", makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("🔄 Swap %s %2.0f%%\n", makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\n💿 SSD %2.0f%% · %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("🗄 HDD %2.0f%% · %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	b.WriteString(fmt.Sprintf("\n🐳 %d container", running))
	if running != 1 {
		b.WriteString("s")
	}
	b.WriteString(" running")
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d stopped", stopped))
	}

	stressSummary := getStressSummary()
	if stressSummary != "" {
		b.WriteString("\n\n💨 *Been under stress:*\n")
		b.WriteString(stressSummary)
	}

	b.WriteString(fmt.Sprintf("\n\n_⏱ Up for %s_", formatUptime(s.Uptime)))

	resetStressCounters()
	return b.String()
}

func getHealthStatus(s Stats) (icon, text string, hasProblems bool) {
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

	if criticalCount > 0 || s.CPU > SogliaCPU || s.RAM > SogliaRAMCritica || s.VolSSD.Used > 95 || s.VolHDD.Used > 95 {
		return "⚠️", "Some issues to look at", true
	}
	if warningCount > 0 || s.CPU > 80 || s.RAM > 85 || s.DiskUtil > 90 || s.VolSSD.Used > SogliaDisco || s.VolHDD.Used > SogliaDisco {
		return "👀", "A few things need attention", true
	}
	return "✨", "Everything's running smoothly", false
}

// generateReport per richieste manuali (/report)
func generateReport(manual bool) string {
	if !manual {
		return generateDailyReport("> *Report NAS*")
	}

	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	b.WriteString("*Report*\n")
	b.WriteString(fmt.Sprintf("%s\n\n", now.Format("02/01 15:04")))

	healthIcon, healthText, _ := getHealthStatus(s)
	b.WriteString(fmt.Sprintf("%s %s\n\n", healthIcon, healthText))

	b.WriteString("*Resources*\n")
	b.WriteString(fmt.Sprintf("CPU %s %.1f%%\n", makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf("RAM %s %.1f%% (%s free)\n", makeProgressBar(s.RAM), s.RAM, formatRAM(s.RAMFreeMB)))
	if s.DiskUtil > 5 {
		b.WriteString(fmt.Sprintf("I/O %s %.0f%%\n", makeProgressBar(s.DiskUtil), s.DiskUtil))
	}
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf("Swap %s %.1f%%\n", makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf("\nSSD %.1f%% · %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf("HDD %.1f%% · %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	b.WriteString(fmt.Sprintf("\nContainers: %d on · %d off\n", running, stopped))

	b.WriteString(fmt.Sprintf("\n_Up for %s_\n", formatUptime(s.Uptime)))

	reportEventsMutex.Lock()
	events := make([]ReportEvent, len(reportEvents))
	copy(events, reportEvents)
	reportEventsMutex.Unlock()

	if len(events) > 0 {
		b.WriteString("\n*Eventi*\n")
		for _, e := range events {
			icon := "."
			switch e.Type {
			case "warning", "critical":
				icon = "!"
			case "action":
				icon = ">"
			}
			b.WriteString(fmt.Sprintf("%s `%s` %s\n", icon, e.Time.In(location).Format("15:04"), truncate(e.Message, 24)))
		}
	}

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

	// Keep events from last 24 hours
	cutoff := time.Now().Add(-24 * time.Hour)
	var newEvents []ReportEvent
	for _, e := range reportEvents {
		if e.Time.After(cutoff) {
			newEvents = append(newEvents, e)
		}
	}
	reportEvents = newEvents
}

// filterSignificantEvents removes trivial events (e.g., short stress periods)
func filterSignificantEvents(events []ReportEvent) []ReportEvent {
	var filtered []ReportEvent
	for _, e := range events {
		// Skip trivial stress events (those mentioning short durations)
		msg := strings.ToLower(e.Message)
		if strings.Contains(msg, "for 30s") || strings.Contains(msg, "for 1m") ||
			strings.Contains(msg, "after 30s") || strings.Contains(msg, "after 1m") {
			continue
		}
		filtered = append(filtered, e)
	}
	return filtered
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

		// Check stress per tutte le risorse
		checkResourceStress(bot, "HDD", s.DiskUtil, SogliaIOCritico)
		checkResourceStress(bot, "CPU", s.CPU, SogliaCPUStress)
		checkResourceStress(bot, "RAM", s.RAM, SogliaRAMStress)
		checkResourceStress(bot, "Swap", s.Swap, SogliaSwapStress)
		checkResourceStress(bot, "SSD", s.VolSSD.Used, SogliaDisco)

		// Check RAM critica
		if s.RAM >= SogliaRAMCritica {
			handleCriticalRAM(bot, s)
		}

		// Pulizia restart counter (ogni ora)
		cleanRestartCounter()
		
		// ════════════ BETA FEATURES ════════════
		checkDockerHealth(bot)
		checkWeeklyPrune(bot)
	}
}

func checkDockerHealth(bot *tgbotapi.BotAPI) {
	// Verifica se il servizio Docker risponde e ha container
	// getContainerList restituisce nil in caso di errore (es. docker non in esecuzione)
	// o lista vuota se docker gira ma non ci sono container.
	containers := getContainerList()
	
	isHealthy := containers != nil && len(containers) > 0
	
	if isHealthy {
		// Tutto ok, resetta timer
		if !dockerFailureStart.IsZero() {
			dockerFailureStart = time.Time{}
			log.Println("[Beta] Docker recoverato/popolato.")
		}
		return
	}
	
	// Rilevato problema (errore o 0 container)
	if dockerFailureStart.IsZero() {
		dockerFailureStart = time.Now()
		log.Println("[Beta] Docker warning: 0 containers o servizio down. Avvio timer 2m.")
		return
	}
	
	// Se siamo in stato di failure da più di 2 minuti
	if time.Since(dockerFailureStart) > 2*time.Minute {
		log.Println("[Beta] ⚠️ Docker down > 2m. Tentativo restart...")
		
		bot.Send(tgbotapi.NewMessage(AllowedUserID, "⚠️ *Docker Watchdog*\n\nNessun container rilevato per 2 minuti.\nRiavvio il servizio Docker..."))
		
		addReportEvent("action", "Docker watchdog restart triggered")
		
		// Reset timer per evitare loop immediato (aspetta altri 2 min dopo restart)
		dockerFailureStart = time.Now() 
		
		// Esegui comando restart
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()
		
		cmd := exec.CommandContext(ctx, "systemctl", "restart", "docker")
		// Fallback per sistemi init.d se systemctl non c'è
		if _, err := exec.LookPath("systemctl"); err != nil {
			cmd = exec.CommandContext(ctx, "/etc/init.d/nasbot", "restart") // No, restartare docker
			// Prova service command standard
			cmd = exec.CommandContext(ctx, "service", "docker", "restart")
		}
		
		out, err := cmd.CombinedOutput()
		if err != nil {
			bot.Send(tgbotapi.NewMessage(AllowedUserID, fmt.Sprintf("❌ Errore restart Docker:\n`%v`", err)))
			log.Printf("[!] Docker restart fail: %v\n%s", err, string(out))
		} else {
			bot.Send(tgbotapi.NewMessage(AllowedUserID, "✅ Comando restart inviato."))
		}
	}
}

func checkWeeklyPrune(bot *tgbotapi.BotAPI) {
	now := time.Now().In(location)
	
	// Esegui SOLO la Domenica (Sunday) alle 04:xx di notte
	isTime := now.Weekday() == time.Sunday && now.Hour() == 4
	
	if isTime {
		if !pruneDoneToday {
			log.Println("[Beta] Esecuzione Weekly Prune...")
			pruneDoneToday = true
			
			go func() {
				// docker system prune -a (all images) -f (force)
				cmd := exec.Command("docker", "system", "prune", "-a", "-f")
				out, err := cmd.CombinedOutput()
				
				var msg string
				if err != nil {
					msg = fmt.Sprintf("🧹 *Weekly Prune Error*\n\n`%v`", err)
				} else {
					// Estrai info utili dall'output (spazio recuperato)
					output := string(out)
					lines := strings.Split(output, "\n")
					lastLine := ""
					if len(lines) > 0 {
						for i := len(lines)-1; i >= 0; i-- {
							if strings.TrimSpace(lines[i]) != "" {
								lastLine = lines[i]
								break
							}
						}
					}
					msg = fmt.Sprintf("🧹 *Weekly Prune*\n\nImmagini inutilizzate rimosse.\n`%s`", lastLine)
					addReportEvent("info", "Weekly docker prune completed")
				}
				
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
			}()
		}
	} else {
		// Resetta il flag quando non siamo più nell'ora X (così è pronto per la settimana dopo)
		if now.Hour() != 4 {
			pruneDoneToday = false
		}
	}
}

// checkResourceStress traccia lo stress di una risorsa e notifica se necessario
func checkResourceStress(bot *tgbotapi.BotAPI, resource string, currentValue, threshold float64) {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	tracker := resourceStress[resource]
	if tracker == nil {
		tracker = &StressTracker{}
		resourceStress[resource] = tracker
	}

	isStressed := currentValue >= threshold

	if isStressed {
		// Inizia nuovo periodo di stress
		if tracker.CurrentStart.IsZero() {
			tracker.CurrentStart = time.Now()
			tracker.StressCount++
			tracker.Notified = false
		}

		// Notifica se stress prolungato (>2 min) e non già notificato e non in quiet hours
		stressDuration := time.Since(tracker.CurrentStart)
		if stressDuration >= DurataStressDisco && !tracker.Notified && !isQuietHours() {
			var emoji, unit string
			switch resource {
			case "HDD":
				emoji = "~"
				unit = "I/O"
			case "SSD":
				emoji = "~"
				unit = "Usage"
			case "CPU":
				emoji = "~"
				unit = "Usage"
			case "RAM":
				emoji = "~"
				unit = "Usage"
			case "Swap":
				emoji = "~"
				unit = "Usage"
			}

			msg := fmt.Sprintf("%s *%s stress*\n\n"+
				"%s: `%.0f%%` for `%s`\n\n"+
				"_Watching..._",
				emoji, resource, unit, currentValue,
				stressDuration.Round(time.Second))

			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)

			tracker.Notified = true
			addReportEvent("warning", fmt.Sprintf("%s high (%.0f%%) for %s", resource, currentValue, stressDuration.Round(time.Second)))
		}
	} else {
		// Fine stress
		if !tracker.CurrentStart.IsZero() {
			stressDuration := time.Since(tracker.CurrentStart)
			tracker.TotalStress += stressDuration

			// Aggiorna durata massima
			if stressDuration > tracker.LongestStress {
				tracker.LongestStress = stressDuration
			}

			// Notify stress end if it was notified and not in quiet hours
			if tracker.Notified && !isQuietHours() {
				msg := fmt.Sprintf("✓ *%s back to normal* after `%s`", resource, stressDuration.Round(time.Second))
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
				addReportEvent("info", fmt.Sprintf("%s normalized after %s", resource, stressDuration.Round(time.Second)))
			}

			tracker.CurrentStart = time.Time{}
			tracker.Notified = false
		}
	}
}

// getStressSummary returns a summary of significant stress events
func getStressSummary() string {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	var parts []string

	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		tracker := resourceStress[res]
		if tracker == nil || tracker.StressCount == 0 {
			continue
		}

		// Skip trivial stress (< 5 min longest duration)
		if tracker.LongestStress < 5*time.Minute {
			continue
		}

		entry := fmt.Sprintf("%s %dx", res, tracker.StressCount)
		if tracker.LongestStress > 0 {
			entry += fmt.Sprintf(" `%s`", formatDuration(tracker.LongestStress))
		}
		parts = append(parts, entry)
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// resetStressCounters resetta i contatori stress per il nuovo periodo di report
func resetStressCounters() {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	for _, tracker := range resourceStress {
		tracker.StressCount = 0
		tracker.LongestStress = 0
		tracker.TotalStress = 0
	}
}

// formatDuration formatta una durata in modo leggibile
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", h, m)
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
			log.Printf("> RAM critica (%.1f%%), riavvio automatico: %s (%.1f%%)", s.RAM, target.name, target.memPct)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "docker", "restart", target.name)
			err := cmd.Run()
			cancel()

			recordAutoRestart(target.name)

			var msgText string
			if err != nil {
				msgText = fmt.Sprintf("! *Auto-restart failed*\n\n"+
					"RAM critical: `%.1f%%`\n"+
					"Container: `%s`\n"+
					"Error: %v", s.RAM, target.name, err)
				addReportEvent("critical", fmt.Sprintf("Auto-restart failed: %s (%v)", target.name, err))
			} else {
				msgText = fmt.Sprintf("> *Auto-restart done*\n\n"+
					"RAM was critical: `%.1f%%`\n"+
					"Restarted: `%s` (`%.1f%%` mem)\n\n"+
					"_Watching..._", s.RAM, target.name, target.memPct)
				addReportEvent("action", fmt.Sprintf("Auto-restart: %s (RAM %.1f%%)", target.name, s.RAM))
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

		// Disk almost full
		if s.VolSSD.Used >= 95 {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("SSD critical: `%.1f%%`", s.VolSSD.Used))
		}
		if s.VolHDD.Used >= 95 {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("HDD critical: `%.1f%%`", s.VolHDD.Used))
		}

		// Check SMART
		for _, dev := range []string{"sda", "sdb"} {
			_, health := readDiskSMART(dev)
			if strings.Contains(strings.ToUpper(health), "FAIL") {
				criticalAlerts = append(criticalAlerts, fmt.Sprintf("Disk %s FAILING — backup now!", dev))
			}
		}

		// Send critical alerts with 30min cooldown
		if len(criticalAlerts) > 0 && time.Since(lastCriticalAlert).Minutes() >= 30 && !isQuietHours() {
			msg := "! *Critical*\n\n" + strings.Join(criticalAlerts, "\n")
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			lastCriticalAlert = time.Now()
		}

		// Registra sempre gli eventi critici per il report
		if len(criticalAlerts) > 0 {
			for _, alert := range criticalAlerts {
				addReportEvent("critical", alert)
			}
		}

		// Record warnings for the report
		if s.CPU >= SogliaCPU {
			addReportEvent("warning", fmt.Sprintf("CPU high: %.1f%%", s.CPU))
		}
		if s.RAM >= SogliaRAM && s.RAM < SogliaRAMCritica {
			addReportEvent("warning", fmt.Sprintf("RAM high: %.1f%%", s.RAM))
		}
		if s.VolSSD.Used >= SogliaDisco && s.VolSSD.Used < 95 {
			addReportEvent("warning", fmt.Sprintf("SSD at %.1f%%", s.VolSSD.Used))
		}
		if s.VolHDD.Used >= SogliaDisco && s.VolHDD.Used < 95 {
			addReportEvent("warning", fmt.Sprintf("HDD at %.1f%%", s.VolHDD.Used))
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

func formatUptime(seconds uint64) string {
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	mins := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	return fmt.Sprintf("%dh%dm", hours, mins)
}

func formatBytes(bytes uint64) string {
	gb := float64(bytes) / 1024 / 1024 / 1024
	if gb >= 1000 {
		return fmt.Sprintf("%.0fT", gb/1024)
	}
	return fmt.Sprintf("%.0fG", gb)
}

func formatRAM(mb uint64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", float64(mb)/1024.0)
	}
	return fmt.Sprintf("%dM", mb)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "~"
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
			tgbotapi.NewInlineKeyboardButtonData("🌐 Net", "show_net"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Docker", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("📊 Stats", "show_dstats"),
			tgbotapi.NewInlineKeyboardButtonData("📋 Report", "show_report"),
		),
	)
}
