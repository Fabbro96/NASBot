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
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/load"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

// --- CONFIGURAZIONE ---
const (
	SogliaCPU      = 90.0
	SogliaRAM      = 90.0
	PathSSD        = "/Volume1"
	PathHDD        = "/Volume2"
	CooldownMinuti = 20
)

var (
	BotToken      string
	AllowedUserID int64
	ultimoAvviso  time.Time
)

func init() {
	BotToken = os.Getenv("BOT_TOKEN")
	if BotToken == "" {
		log.Fatal("❌ ERRORE: BOT_TOKEN mancante!")
	}
	uidStr := os.Getenv("BOT_USER_ID")
	if uidStr == "" {
		log.Fatal("❌ ERRORE: BOT_USER_ID mancante!")
	}
	var err error
	AllowedUserID, err = strconv.ParseInt(uidStr, 10, 64)
	if err != nil {
		log.Fatal("❌ ERRORE: BOT_USER_ID deve essere numerico!")
	}
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ CRITICAL PANIC: %v\nStack: %s", r, string(debug.Stack()))
		}
	}()

	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		log.Panic("Errore avvio Bot: ", err)
	}

	log.Printf("✅ Platinum Bot avviato: %s", bot.Self.UserName)

	go monitoraggioBackground(bot)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// 1. Gestione Click (Callback)
		if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery)
			continue
		}

		// 2. Gestione Comandi
		if update.Message == nil || update.Message.Chat.ID != AllowedUserID {
			continue
		}

		if update.Message.IsCommand() {
			chatID := update.Message.Chat.ID
			switch update.Message.Command() {
			case "status", "start":
				inviaMessaggioNuovo(bot, chatID, getStatusText())
			case "docker":
				inviaMessaggioNuovo(bot, chatID, getDockerText())
			case "dstats":
				inviaMessaggioNuovo(bot, chatID, getDockerStatsText())
			case "temp":
				inviaMessaggioNuovo(bot, chatID, getTempText())
			case "net":
				inviaNetwork(bot, chatID)
			case "speedtest":
				inviaSpeedtest(bot, chatID)
			case "logs":
				inviaLogs(bot, chatID)
			case "reboot":
				inviaPowerCmd(bot, chatID, "reboot")
			case "shutdown":
				inviaPowerCmd(bot, chatID, "shutdown")
			case "help":
				inviaHelp(bot, chatID)
			default:
				bot.Send(tgbotapi.NewMessage(chatID, "Comando sconosciuto. Usa /help"))
			}
		}
	}
}

// --- GENERATORI DI TESTO ---

func getStatusText() string {
	s, _ := getFullStats()
	uptimeStr := formatUptime(s.Uptime)
	now := time.Now().Format("15:04:05")
	
	hddFreeStr := fmt.Sprintf("%d GB", s.VolHDD.Free)
	if s.VolHDD.Free > 1000 {
		hddFreeStr = fmt.Sprintf("%.1f TB", float64(s.VolHDD.Free)/1024.0)
	}

	// ECCO LA CORREZIONE: Aggiunto blocco RAM alla fine
	return fmt.Sprintf("📊 *STATUS SISTEMA* (Agg: %s)\n⏱ _Uptime: %s_\n\n%s *CPU:* `%.1f%%` (Load: %.2f)\n%s *RAM:* `%.1f%%`\n%s *SWAP:* `%.1f%%`\n\n🚀 *SSD:* `%.1f%%` (Free: %d GB)\n🗄 *HDD:* `%.1f%%` (Free: %s)\n\n⚙️ *I/O:* %s `(%.0f%%)`\n   R: `%.1f` | W: `%.1f` MB/s\n\n🔥 *TOP CPU:*\n%s\n🧠 *TOP RAM:*\n%s",
		now, uptimeStr, getIcon(s.CPU, 70, 90), s.CPU, s.Load1m, getIcon(s.RAM, 70, 90), s.RAM, getIcon(s.Swap, 50, 75), s.Swap, s.VolSSD.Used, s.VolSSD.Free, s.VolHDD.Used, hddFreeStr, "OK", s.DiskUtil, s.ReadMBs, s.WriteMBs, 
		getProcsTable("cpu", 3), // Tabella CPU
		getProcsTable("mem", 3)) // Tabella RAM (Mancava qui!)
}

func getTempText() string {
	var sb strings.Builder
	sb.WriteString("🌡 *Temperature & Salute* (Agg: " + time.Now().Format("15:04:05") + ")\n\n")

	raw, _ := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	val, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	sb.WriteString(fmt.Sprintf("🔥 *CPU:* `%.1f°C`\n\n", float64(val)/1000.0))

	sb.WriteString("💾 *Dischi:*\n")
	disks := []string{"sda", "sdb"}
	for _, d := range disks {
		cmdTemp := exec.Command("bash", "-c", fmt.Sprintf("smartctl -A /dev/%s | grep -i Temperature_Celsius | awk '{print $10}'", d))
		outTemp, _ := cmdTemp.Output()
		temp := strings.TrimSpace(string(outTemp))
		if temp == "" { temp = "??" }

		cmdHealth := exec.Command("bash", "-c", fmt.Sprintf("smartctl -H /dev/%s | grep -i result | cut -d: -f2", d))
		outHealth, _ := cmdHealth.Output()
		health := strings.TrimSpace(string(outHealth))
		
		icon := "🟢"
		if strings.Contains(strings.ToUpper(health), "FAIL") { icon = "🔴" }
		sb.WriteString(fmt.Sprintf("%s *%s:* `%s°C` [%s]\n", icon, d, temp, health))
	}
	return sb.String()
}

func getDockerText() string {
	cmd := exec.Command("docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}")
	out, err := cmd.Output()
	if err != nil { return "❌ Errore Docker." }
	
	lines := strings.Split(string(out), "\n")
	var sb strings.Builder
	sb.WriteString("🐳 *Docker Status* (Agg: " + time.Now().Format("15:04:05") + ")\n```\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) >= 2 {
			name := parts[0]
			if len(name) > 12 { name = name[:11] + "…" }
			status := parts[1]
			if strings.Contains(status, "Up") {
				status = "✅ " + strings.Split(status, " ")[1]
			} else {
				status = "🔴 Exited"
			}
			sb.WriteString(fmt.Sprintf("%-12s | %s\n", name, status))
		}
	}
	sb.WriteString("```")
	return sb.String()
}

func getDockerStatsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemPerc}}")
	out, err := cmd.Output()
	if err != nil { return "❌ Docker Stats Timeout/Error." }
	
	lines := strings.Split(string(out), "\n")
	var sb strings.Builder
	sb.WriteString("📈 *Docker Risorse* (Agg: " + time.Now().Format("15:04:05") + ")\n```\n")
	sb.WriteString(fmt.Sprintf("%-10s %5s %5s\n", "Cont.", "CPU", "MEM"))
	sb.WriteString("-----------+-----+-----\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			name := parts[0]
			if len(name) > 10 { name = name[:9] + "…" }
			sb.WriteString(fmt.Sprintf("%-10s %5s %5s\n", name, strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])))
		}
	}
	sb.WriteString("```")
	return sb.String()
}

// --- LOGICA DI INVIO ---

func getKeyboard() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 Status", "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData("🌡 Temp", "show_temp"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Docker", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("📈 D.Stats", "show_dstats"),
		),
	)
}

func inviaMessaggioNuovo(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = getKeyboard()
	bot.Send(msg)
}

func modificaMessaggio(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	markup := getKeyboard()
	edit.ReplyMarkup = &markup
	bot.Send(edit)
}

func handleCallback(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery) {
	callbackCfg := tgbotapi.NewCallback(query.ID, "Aggiornato!")
	bot.Request(callbackCfg)

	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	var text string

	switch query.Data {
	case "refresh_status":
		text = getStatusText()
	case "show_temp":
		text = getTempText()
	case "show_docker":
		text = getDockerText()
	case "show_dstats":
		text = getDockerStatsText()
	default:
		return
	}

	modificaMessaggio(bot, chatID, msgID, text)
}

// --- ALTRI COMANDI ---

func inviaNetwork(bot *tgbotapi.BotAPI, chatID int64) {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	publicIP := "Errore"
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		publicIP = string(body)
	}
	cmd := exec.Command("hostname", "-I")
	out, _ := cmd.Output()
	localIP := strings.TrimSpace(string(out))
	
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("🌐 *Net Info*\n🏠 IP: `%s`\n🌍 WAN: `%s`", localIP, publicIP))
	msg.ParseMode = "Markdown"
	bot.Send(msg)
}

func inviaSpeedtest(bot *tgbotapi.BotAPI, chatID int64) {
	bot.Send(tgbotapi.NewMessage(chatID, "🚀 *Avvio Speedtest (Download 100MB)...*"))
	start := time.Now()
	resp, err := http.Get("https://fsn1-speed.hetzner.com/100MB.bin")
	if err != nil {
		bot.Send(tgbotapi.NewMessage(chatID, "❌ Errore connessione."))
		return
	}
	defer resp.Body.Close()
	written, _ := io.Copy(io.Discard, resp.Body)
	duration := time.Since(start)
	mbps := (float64(written) * 8) / duration.Seconds() / 1000000
	msg := fmt.Sprintf("🚀 *Risultato Speedtest*\n\n📦 Scaricati: %d MB\n⏱ Tempo: %.1fs\n💨 *Velocità:* `%.2f Mbps`", written/1024/1024, duration.Seconds(), mbps)
	bot.Send(tgbotapi.NewMessage(chatID, msg))
}

func inviaLogs(bot *tgbotapi.BotAPI, chatID int64) {
	cmd := exec.Command("bash", "-c", "dmesg | tail -n 10")
	out, _ := cmd.Output()
	msg := fmt.Sprintf("📜 *Ultimi System Logs*\n```\n%s\n```", string(out))
	m := tgbotapi.NewMessage(chatID, msg)
	m.ParseMode = "Markdown"
	bot.Send(m)
}

func inviaPowerCmd(bot *tgbotapi.BotAPI, chatID int64, action string) {
	bot.Send(tgbotapi.NewMessage(chatID, "⚠️ Eseguo "+action+"..."))
	cmd := "reboot"
	if action == "shutdown" {
		cmd = "poweroff"
	}
	exec.Command(cmd).Run()
}

func inviaHelp(bot *tgbotapi.BotAPI, chatID int64) {
	bot.Send(tgbotapi.NewMessage(chatID, "🤖 *PLATINUM BOT*\n\n/status - Dashboard Interattiva\n/speedtest - Test Velocità\n/logs - System Logs\n/net - IP Info\n/reboot - Riavvia"))
}

// --- MONITORAGGIO ---

func monitoraggioBackground(bot *tgbotapi.BotAPI) {
	for {
		s, err := getFullStats()
		if err == nil {
			motivo := ""
			if s.CPU >= SogliaCPU {
				motivo = "🔥 CPU CRITICA"
			} else if s.DiskUtil >= 98.0 {
				motivo = "💾 I/O BLOCK"
			}
			if motivo != "" && time.Since(ultimoAvviso).Minutes() >= CooldownMinuti {
				bot.Send(tgbotapi.NewMessage(int64(AllowedUserID), "🚨 *ALLARME:* "+motivo))
				ultimoAvviso = time.Now()
			}
		}
		time.Sleep(30 * time.Second)
	}
}

// --- HELPERS ---

func getFullStats() (Stats, error) {
	ioStart, _ := disk.IOCounters()
	time.Sleep(1000 * time.Millisecond)
	ioEnd, _ := disk.IOCounters()
	c, _ := cpu.Percent(0, false)
	v, _ := mem.VirtualMemory()
	s, _ := mem.SwapMemory()
	l, _ := load.Avg()
	h, _ := host.Info()
	dSSD, _ := disk.Usage(PathSSD)
	dHDD, _ := disk.Usage(PathHDD)
	var rBytes, wBytes uint64
	maxUtil := 0.0
	for k := range ioEnd {
		if startVal, ok := ioStart[k]; ok {
			rBytes += ioEnd[k].ReadBytes - startVal.ReadBytes
			wBytes += ioEnd[k].WriteBytes - startVal.WriteBytes
			deltaIoTime := ioEnd[k].IoTime - startVal.IoTime
			util := float64(deltaIoTime) / 10.0
			if util > 100.0 { util = 100.0 }
			if util > maxUtil { maxUtil = util }
		}
	}
	ssdUsed, ssdFree := 0.0, uint64(0)
	if dSSD != nil {
		ssdUsed = dSSD.UsedPercent
		ssdFree = dSSD.Free / 1024 / 1024 / 1024
	}
	hddUsed, hddFree := 0.0, uint64(0)
	if dHDD != nil {
		hddUsed = dHDD.UsedPercent
		hddFree = dHDD.Free / 1024 / 1024 / 1024
	}
	return Stats{CPU: c[0], RAM: v.UsedPercent, Swap: s.UsedPercent, VolSSD: VolumeStats{Used: ssdUsed, Free: ssdFree}, VolHDD: VolumeStats{Used: hddUsed, Free: hddFree}, ReadMBs: float64(rBytes) / 1024 / 1024, WriteMBs: float64(wBytes) / 1024 / 1024, DiskUtil: maxUtil, Load1m: l.Load1, Uptime: h.Uptime}, nil
}

type VolumeStats struct {
	Used float64
	Free uint64
}
type Stats struct {
	CPU      float64
	RAM      float64
	Swap     float64
	VolSSD   VolumeStats
	VolHDD   VolumeStats
	ReadMBs  float64
	WriteMBs float64
	DiskUtil float64
	Load1m   float64
	Uptime   uint64
}

func getIcon(val float64, warn float64, crit float64) string {
	if val >= crit { return "🔴" }
	if val >= warn { return "🟡" }
	return "🟢"
}

type ProcInfo struct {
	Name string
	Mem  float64
	Cpu  float64
}

func getProcsTable(sortBy string, limit int) string {
	ps, _ := process.Processes()
	var list []ProcInfo
	for _, p := range ps {
		n, _ := p.Name()
		m, _ := p.MemoryPercent()
		c, _ := p.CPUPercent()
		if n != "" && (m > 0.1 || c > 0.1) {
			list = append(list, ProcInfo{Name: n, Mem: float64(m), Cpu: c})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if sortBy == "cpu" {
			return list[i].Cpu > list[j].Cpu
		}
		return list[i].Mem > list[j].Mem
	})
	var sb strings.Builder
	sb.WriteString("```\n")
	if sortBy == "cpu" {
		sb.WriteString(fmt.Sprintf("%-10s %5s\n", "Proc", "CPU"))
	} else {
		sb.WriteString(fmt.Sprintf("%-10s %5s\n", "Proc", "MEM"))
	}
	if len(list) < limit {
		limit = len(list)
	}
	for i := 0; i < limit; i++ {
		nome := list[i].Name
		if len(nome) > 10 {
			nome = nome[:9] + "…"
		}
		val := 0.0
		if sortBy == "cpu" {
			val = list[i].Cpu
		} else {
			val = list[i].Mem
		}
		sb.WriteString(fmt.Sprintf("%-10s %4.1f%%\n", nome, val))
	}
	sb.WriteString("```")
	return sb.String()
}

func formatUptime(seconds uint64) string {
	minutes := seconds / 60
	seconds = seconds % 60
	hours := minutes / 60
	minutes = minutes % 60
	days := hours / 24
	hours = hours % 24
	return fmt.Sprintf("%dg %dh %dm", days, hours, minutes)
}
