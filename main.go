package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	SogliaCPU      = 90.0
	SogliaRAM      = 90.0
	SogliaDisco    = 90.0 // Allarme disco quasi pieno
	CooldownMinuti = 20

	// Path volumi (adatta al tuo NAS)
	PathSSD = "/Volume1"
	PathHDD = "/Volume2"

	// Intervalli background (ottimizzati per NAS lento)
	IntervalloStats   = 5 * time.Second  // Aggiorna cache stats
	IntervalloMonitor = 30 * time.Second // Check allarmi
)

var (
	BotToken      string
	AllowedUserID int64

	// Cache globale con mutex (evita chiamate lente ripetute)
	statsCache   Stats
	statsMutex   sync.RWMutex
	statsReady   bool
	ultimoAvviso time.Time

	// Pending confirmations per reboot/shutdown
	pendingAction      string
	pendingActionMutex sync.Mutex
)

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
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ PANIC: %v\n%s", r, debug.Stack())
		}
	}()

	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		log.Fatalf("❌ Errore avvio bot: %v", err)
	}
	log.Printf("✅ NASBot avviato: @%s", bot.Self.UserName)

	// Avvia goroutine background (leggere, ottimizzate per ARM)
	go statsCollector()   // Aggiorna cache stats ogni N secondi
	go monitorAlerts(bot) // Controlla soglie e invia allarmi

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
	switch msg.Command() {
	case "status", "start":
		sendWithKeyboard(bot, chatID, getStatusText())
	case "docker":
		sendWithKeyboard(bot, chatID, getDockerText())
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

	// Navigazione normale
	var text string
	switch data {
	case "refresh_status":
		text = getStatusText()
	case "show_temp":
		text = getTempText()
	case "show_docker":
		text = getDockerText()
	case "show_dstats":
		text = getDockerStatsText()
	case "show_net":
		text = getNetworkText()
	default:
		return
	}
	kb := getMainKeyboard()
	editMessage(bot, chatID, msgID, text, &kb)
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
	return `🤖 *NASBot — Comandi*

📊 *Monitoraggio:*
/status — Dashboard sistema
/temp — Temperature CPU e dischi
/docker — Stato container
/dstats — Risorse container
/net — Info rete
/logs — Log di sistema

🔧 *Utilità:*
/speedtest — Test velocità

⚡ *Sistema:*
/reboot — Riavvia NAS
/shutdown — Spegni NAS

━━━━━━━━━━━━━━━━━━
💡 _Usa i pulsanti sotto i messaggi per navigare rapidamente._`
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
//  MONITOR ALLARMI
// ═══════════════════════════════════════════════════════════════════

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

		var alerts []string

		if s.CPU >= SogliaCPU {
			alerts = append(alerts, fmt.Sprintf("🔥 *CPU critica:* `%.1f%%`", s.CPU))
		}
		if s.RAM >= SogliaRAM {
			alerts = append(alerts, fmt.Sprintf("🧠 *RAM critica:* `%.1f%%`", s.RAM))
		}
		if s.VolSSD.Used >= SogliaDisco {
			alerts = append(alerts, fmt.Sprintf("🚀 *SSD quasi pieno:* `%.1f%%`", s.VolSSD.Used))
		}
		if s.VolHDD.Used >= SogliaDisco {
			alerts = append(alerts, fmt.Sprintf("🗄 *HDD quasi pieno:* `%.1f%%`", s.VolHDD.Used))
		}
		if s.DiskUtil >= 98 {
			alerts = append(alerts, "💾 *I/O bloccato!*")
		}

		if len(alerts) > 0 && time.Since(ultimoAvviso).Minutes() >= CooldownMinuti {
			msg := "🚨 *ALLARME NAS*\n\n" + strings.Join(alerts, "\n")
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			ultimoAvviso = time.Now()
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
			tgbotapi.NewInlineKeyboardButtonData("🌐 Rete", "show_net"),
		),
	)
}
