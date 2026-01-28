package main

import (
	"bytes"
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
//  CONFIGURATION STRUCTURES
// ═══════════════════════════════════════════════════════════════════

// Config holds all configuration from config.json
type Config struct {
	BotToken      string `json:"bot_token"`
	AllowedUserID int64  `json:"allowed_user_id"`
	GeminiAPIKey  string `json:"gemini_api_key"`

	Paths struct {
		SSD string `json:"ssd"`
		HDD string `json:"hdd"`
	} `json:"paths"`

	Timezone string `json:"timezone"`

	Reports struct {
		Enabled bool `json:"enabled"`
		Morning struct {
			Enabled bool `json:"enabled"`
			Hour    int  `json:"hour"`
			Minute  int  `json:"minute"`
		} `json:"morning"`
		Evening struct {
			Enabled bool `json:"enabled"`
			Hour    int  `json:"hour"`
			Minute  int  `json:"minute"`
		} `json:"evening"`
	} `json:"reports"`

	QuietHours struct {
		Enabled     bool `json:"enabled"`
		StartHour   int  `json:"start_hour"`
		StartMinute int  `json:"start_minute"`
		EndHour     int  `json:"end_hour"`
		EndMinute   int  `json:"end_minute"`
	} `json:"quiet_hours"`

	Notifications struct {
		CPU struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"cpu"`
		RAM struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"ram"`
		Swap struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"swap"`
		DiskSSD struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"disk_ssd"`
		DiskHDD struct {
			Enabled           bool    `json:"enabled"`
			WarningThreshold  float64 `json:"warning_threshold"`
			CriticalThreshold float64 `json:"critical_threshold"`
		} `json:"disk_hdd"`
		DiskIO struct {
			Enabled          bool    `json:"enabled"`
			WarningThreshold float64 `json:"warning_threshold"`
		} `json:"disk_io"`
		SMART struct {
			Enabled bool `json:"enabled"`
		} `json:"smart"`
	} `json:"notifications"`

	StressTracking struct {
		Enabled                  bool `json:"enabled"`
		DurationThresholdMinutes int  `json:"duration_threshold_minutes"`
	} `json:"stress_tracking"`

	Docker struct {
		Watchdog struct {
			Enabled            bool `json:"enabled"`
			TimeoutMinutes     int  `json:"timeout_minutes"`
			AutoRestartService bool `json:"auto_restart_service"`
		} `json:"watchdog"`
		WeeklyPrune struct {
			Enabled bool   `json:"enabled"`
			Day     string `json:"day"`
			Hour    int    `json:"hour"`
		} `json:"weekly_prune"`
		AutoRestartOnRAMCritical struct {
			Enabled            bool    `json:"enabled"`
			MaxRestartsPerHour int     `json:"max_restarts_per_hour"`
			RAMThreshold       float64 `json:"ram_threshold"`
		} `json:"auto_restart_on_ram_critical"`
	} `json:"docker"`

	Intervals struct {
		StatsSeconds              int `json:"stats_seconds"`
		MonitorSeconds            int `json:"monitor_seconds"`
		CriticalAlertCooldownMins int `json:"critical_alert_cooldown_minutes"`
	} `json:"intervals"`
}

// Default values
const (
	DefaultStateFile = "nasbot_state.json"
)

var (
	// Global config
	cfg Config

	// Computed values from config
	BotToken      string
	AllowedUserID int64
	PathSSD       = "/Volume1"
	PathHDD       = "/Volume2"

	// Intervals (computed from config)
	IntervalStats   = 5 * time.Second
	IntervalMonitor = 30 * time.Second

	// Global cache with mutex
	statsCache Stats
	statsMutex sync.RWMutex
	statsReady bool

	// Stress tracking for all resources
	resourceStress      map[string]*StressTracker
	resourceStressMutex sync.Mutex

	// Autonomous action tracking
	autoRestarts      map[string][]time.Time
	autoRestartsMutex sync.Mutex

	// Report tracking
	lastReportTime    time.Time
	reportEvents      []ReportEvent
	reportEventsMutex sync.Mutex
	location          *time.Location

	// Pending confirmations
	pendingAction      string
	pendingActionMutex sync.Mutex

	// Container action pending
	pendingContainerAction string
	pendingContainerName   string
	pendingContainerMutex  sync.Mutex

	// Docker watchdog
	dockerFailureStart time.Time
	pruneDoneToday     bool

	// Bot start time for ping
	botStartTime time.Time

	// Container state tracking for unexpected stops
	lastContainerStates map[string]bool
	containerStateMutex sync.Mutex

	// Disk usage history for prediction
	diskUsageHistory      []DiskUsagePoint
	diskUsageHistoryMutex sync.Mutex

	// User settings (persistent)
	currentLanguage = "en"
	reportMode      = 2 // 0=disabled, 1=once daily, 2=twice daily

	// Runtime config (overridable by user)
	reportMorningHour   = 7
	reportMorningMinute = 30
	reportEveningHour   = 18
	reportEveningMinute = 30

	quietHoursEnabled = true
	quietStartHour    = 23
	quietStartMinute  = 30
	quietEndHour      = 7
	quietEndMinute    = 0

	dockerPruneEnabled = true
	dockerPruneDay     = "sunday"
	dockerPruneHour    = 4
)

// Translations
var translations = map[string]map[string]string{
	"en": {
		"status_title":     "🖥 *NAS* at %s\n\n",
		"cpu_fmt":          "🧠 CPU  %s %2.0f%%\n",
		"ram_fmt":          "💾 RAM  %s %2.0f%%\n",
		"swap_fmt":         "🔄 Swap %s %2.0f%%\n",
		"ssd_fmt":          "\n💿 SSD %2.0f%% · %s free\n",
		"hdd_fmt":          "🗄 HDD %2.0f%% · %s free\n",
		"disk_io_fmt":      "\n📡 Disk I/O at %.0f%%",
		"disk_rw_fmt":      " (R %.0f / W %.0f MB/s)",
		"uptime_fmt":       "\n_⏱ Running for %s_",
		"loading":          "_loading..._",
		"lang_select":      "🌐 Select Language / Seleziona Lingua",
		"lang_set_en":      "✅ Language set to English",
		"lang_set_it":      "✅ Lingua impostata su Italiano",
		"start":            "▶️ Start",
		"stop":             "⏹ Stop",
		"restart":          "🔄 Restart",
		"kill":             "💀 Force Kill",
		"logs":             "📝 Logs",
		"yes":              "✅ Yes",
		"no":               "❌ No",
		"confirm_action":   "%s *%s*?",
		"kill_warn":        "\n\n⚠️ _This will forcefully terminate the container!_",
		"back":             "⬅️ Back",
		"settings_title":   "⚙️ *Settings*",
		"settings_lang":    "🌐 Language",
		"settings_reports": "📨 Daily Reports",
		"settings_quiet":   "🌙 Quiet Hours",
		"settings_prune":   "🧹 Docker Prune",
		"report_disabled":  "Disabled",
		"report_once":      "Once daily",
		"report_twice":     "Twice daily",
		"settings_saved":   "✅ Settings saved",
		"quiet_enabled":    "Enabled",
		"quiet_disabled":   "Disabled",
		"prune_enabled":    "Enabled",
		"prune_disabled":   "Disabled",
		"set_time":         "Set Time",
		"enabled":          "Enabled",
		"disabled":         "Disabled",
		"enable":           "Enable",
		"disable":          "Disable",
		"schedule":         "Schedule",
		"monday":           "Monday",
		"tuesday":          "Tuesday",
		"wednesday":        "Wednesday",
		"thursday":         "Thursday",
		"friday":           "Friday",
		"saturday":         "Saturday",
		"sunday":           "Sunday",
		"good_morning":     "Good morning! ☀️",
		"good_evening":     "Good evening! 🌙",
		"report_events":    "Events",
		"report_resources": "Resources",
		"report_stress":    "Been under stress:",
		"containers_running": "containers running",
		"container_running":  "container running",
		"containers_stopped": "stopped",
		"health_critical":    "Some issues to look at",
		"health_warning":     "A few things need attention",
		"health_ok":          "Everything's running smoothly",
		"ai_summary":         "AI Summary",
	},
	"it": {
		"status_title":     "🖥 *NAS* alle %s\n\n",
		"cpu_fmt":          "🧠 CPU  %s %2.0f%%\n",
		"ram_fmt":          "💾 RAM  %s %2.0f%%\n",
		"swap_fmt":         "🔄 Swap %s %2.0f%%\n",
		"ssd_fmt":          "\n💿 SSD %2.0f%% · %s liberi\n",
		"hdd_fmt":          "🗄 HDD %2.0f%% · %s liberi\n",
		"disk_io_fmt":      "\n📡 I/O Disco al %.0f%%",
		"disk_rw_fmt":      " (L %.0f / S %.0f MB/s)",
		"uptime_fmt":       "\n_⏱ Attivo da %s_",
		"loading":          "_caricamento..._",
		"lang_select":      "🌐 Seleziona Lingua",
		"lang_set_en":      "✅ Language set to English",
		"lang_set_it":      "✅ Lingua impostata su Italiano",
		"start":            "▶️ Avvia",
		"stop":             "⏹ Ferma",
		"restart":          "🔄 Riavvia",
		"kill":             "💀 Uccidi",
		"logs":             "📝 Logs",
		"yes":              "✅ Sì",
		"no":               "❌ No",
		"confirm_action":   "%s *%s*?",
		"kill_warn":        "\n\n⚠️ _Questo terminerà forzatamente il container!_",
		"back":             "⬅️ Indietro",
		"settings_title":   "⚙️ *Impostazioni*",
		"settings_lang":    "🌐 Lingua",
		"settings_reports": "📨 Report Giornalieri",
		"settings_quiet":   "🌙 Ore Silenziose",
		"settings_prune":   "🧹 Pulizia Docker",
		"report_disabled":  "Disabilitati",
		"report_once":      "Una volta al giorno",
		"report_twice":     "Due volte al giorno",
		"settings_saved":   "✅ Impostazioni salvate",
		"quiet_enabled":    "Abilitate",
		"quiet_disabled":   "Disabilitate",
		"prune_enabled":    "Abilitata",
		"prune_disabled":   "Disabilitata",
		"set_time":         "Imposta Orario",
		"enabled":          "Abilitato",
		"disabled":         "Disabilitato",
		"enable":           "Abilita",
		"disable":          "Disabilita",
		"schedule":         "Programmazione",
		"monday":           "Lunedì",
		"tuesday":          "Martedì",
		"wednesday":        "Mercoledì",
		"thursday":         "Giovedì",
		"friday":           "Venerdì",
		"saturday":         "Sabato",
		"sunday":           "Domenica",
		"good_morning":     "Buongiorno! ☀️",
		"good_evening":     "Buonasera! 🌙",
		"report_events":    "Eventi",
		"report_resources": "Risorse",
		"report_stress":    "Sotto stress:",
		"containers_running": "container attivi",
		"container_running":  "container attivo",
		"containers_stopped": "fermati",
		"health_critical":    "Alcuni problemi da verificare",
		"health_warning":     "Alcune cose richiedono attenzione",
		"health_ok":          "Tutto funziona correttamente",
		"ai_summary":         "Riepilogo AI",
	},
}

func tr(key string) string {
	if currentLanguage == "" {
		currentLanguage = "en"
	}
	t, ok := translations[currentLanguage]
	if !ok {
		t = translations["en"]
	}
	if v, ok := t[key]; ok {
		return v
	}
	if tEn, ok := translations["en"]; ok {
		if v, ok := tEn[key]; ok {
			return v
		}
	}
	return key
}

// DiskUsagePoint stores disk usage at a point in time
type DiskUsagePoint struct {
	Time    time.Time
	SSDUsed float64
	HDDUsed float64
	SSDFree uint64
	HDDFree uint64
}

// ReportEvent tracks events for the periodic report
type ReportEvent struct {
	Time    time.Time
	Type    string // "warning", "critical", "action", "info"
	Message string
}

// StressTracker tracks stress periods for a resource
type StressTracker struct {
	CurrentStart  time.Time
	StressCount   int
	LongestStress time.Duration
	TotalStress   time.Duration
	Notified      bool
}

// BotState for persistence
type BotState struct {
	LastReportTime time.Time              `json:"last_report_time"`
	AutoRestarts   map[string][]time.Time `json:"auto_restarts"`
	Language       string                 `json:"language"`
	ReportMode     int                    `json:"report_mode"` // 0=disabled, 1=once, 2=twice

	// User-configurable settings
	ReportMorningHour   int `json:"report_morning_hour"`
	ReportMorningMinute int `json:"report_morning_minute"`
	ReportEveningHour   int `json:"report_evening_hour"`
	ReportEveningMinute int `json:"report_evening_minute"`

	QuietHoursEnabled bool `json:"quiet_hours_enabled"`
	QuietStartHour    int  `json:"quiet_start_hour"`
	QuietStartMinute  int  `json:"quiet_start_minute"`
	QuietEndHour      int  `json:"quiet_end_hour"`
	QuietEndMinute    int  `json:"quiet_end_minute"`

	DockerPruneEnabled bool   `json:"docker_prune_enabled"`
	DockerPruneDay     string `json:"docker_prune_day"`
	DockerPruneHour    int    `json:"docker_prune_hour"`
}

// ═══════════════════════════════════════════════════════════════════
//  INIT & MAIN
// ═══════════════════════════════════════════════════════════════════

func init() {
	botStartTime = time.Now()
	loadConfig()

	// Initialize timezone
	tz := cfg.Timezone
	if tz == "" {
		tz = "Europe/Rome"
	}
	var err error
	location, err = time.LoadLocation(tz)
	if err != nil {
		log.Printf("[w] Timezone %s not found, using UTC", tz)
		location = time.UTC
	}

	// Initialize maps
	autoRestarts = make(map[string][]time.Time)
	resourceStress = make(map[string]*StressTracker)
	lastContainerStates = make(map[string]bool)
	diskUsageHistory = make([]DiskUsagePoint, 0, 288) // ~24h of data at 5min intervals
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		resourceStress[res] = &StressTracker{}
	}

	// Load persistent state
	loadState()
}

// loadConfig reads configuration from config.json with smart defaults
func loadConfig() {
	data, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("❌ Error reading config.json: %v\nCreate the file by copying config.example.json", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("❌ Error parsing config.json: %v", err)
	}

	// Required fields
	BotToken = cfg.BotToken
	AllowedUserID = cfg.AllowedUserID

	if BotToken == "" {
		log.Fatal("❌ bot_token empty in config.json")
	}
	if AllowedUserID == 0 {
		log.Fatal("❌ allowed_user_id empty or invalid in config.json")
	}

	// Paths with defaults
	if cfg.Paths.SSD != "" {
		PathSSD = cfg.Paths.SSD
	}
	if cfg.Paths.HDD != "" {
		PathHDD = cfg.Paths.HDD
	}

	// Apply sensible defaults for missing config values
	applyConfigDefaults()

	// Update intervals from config
	if cfg.Intervals.StatsSeconds > 0 {
		IntervalStats = time.Duration(cfg.Intervals.StatsSeconds) * time.Second
	}
	if cfg.Intervals.MonitorSeconds > 0 {
		IntervalMonitor = time.Duration(cfg.Intervals.MonitorSeconds) * time.Second
	}

	log.Println("[✓] Config loaded from config.json")
}

// applyConfigDefaults sets sensible defaults for missing configuration
func applyConfigDefaults() {
	// Timezone
	if cfg.Timezone == "" {
		cfg.Timezone = "Europe/Rome"
	}

	// Reports defaults
	if cfg.Reports.Morning.Hour == 0 && cfg.Reports.Morning.Minute == 0 {
		cfg.Reports.Morning.Hour = 7
		cfg.Reports.Morning.Minute = 30
		cfg.Reports.Morning.Enabled = true
	}
	if cfg.Reports.Evening.Hour == 0 && cfg.Reports.Evening.Minute == 0 {
		cfg.Reports.Evening.Hour = 18
		cfg.Reports.Evening.Minute = 30
		cfg.Reports.Evening.Enabled = true
	}

	// Quiet hours defaults
	if !cfg.QuietHours.Enabled && cfg.QuietHours.StartHour == 0 {
		cfg.QuietHours.Enabled = true
		cfg.QuietHours.StartHour = 23
		cfg.QuietHours.StartMinute = 30
		cfg.QuietHours.EndHour = 7
		cfg.QuietHours.EndMinute = 0
	}

	// Notification defaults
	if cfg.Notifications.CPU.WarningThreshold == 0 {
		cfg.Notifications.CPU.Enabled = true
		cfg.Notifications.CPU.WarningThreshold = 90.0
		cfg.Notifications.CPU.CriticalThreshold = 95.0
	}
	if cfg.Notifications.RAM.WarningThreshold == 0 {
		cfg.Notifications.RAM.Enabled = true
		cfg.Notifications.RAM.WarningThreshold = 90.0
		cfg.Notifications.RAM.CriticalThreshold = 95.0
	}
	if cfg.Notifications.Swap.WarningThreshold == 0 {
		cfg.Notifications.Swap.WarningThreshold = 50.0
		cfg.Notifications.Swap.CriticalThreshold = 80.0
		// Swap disabled by default as per user's example
	}
	if cfg.Notifications.DiskSSD.WarningThreshold == 0 {
		cfg.Notifications.DiskSSD.Enabled = true
		cfg.Notifications.DiskSSD.WarningThreshold = 90.0
		cfg.Notifications.DiskSSD.CriticalThreshold = 95.0
	}
	if cfg.Notifications.DiskHDD.WarningThreshold == 0 {
		cfg.Notifications.DiskHDD.Enabled = true
		cfg.Notifications.DiskHDD.WarningThreshold = 90.0
		cfg.Notifications.DiskHDD.CriticalThreshold = 95.0
	}
	if cfg.Notifications.DiskIO.WarningThreshold == 0 {
		cfg.Notifications.DiskIO.Enabled = true
		cfg.Notifications.DiskIO.WarningThreshold = 95.0
	}
	if !cfg.Notifications.SMART.Enabled {
		cfg.Notifications.SMART.Enabled = true
	}

	// Stress tracking defaults
	if cfg.StressTracking.DurationThresholdMinutes == 0 {
		cfg.StressTracking.Enabled = true
		cfg.StressTracking.DurationThresholdMinutes = 2
	}

	// Docker defaults
	if cfg.Docker.Watchdog.TimeoutMinutes == 0 {
		cfg.Docker.Watchdog.Enabled = true
		cfg.Docker.Watchdog.TimeoutMinutes = 2
		cfg.Docker.Watchdog.AutoRestartService = true
	}
	if cfg.Docker.WeeklyPrune.Day == "" {
		cfg.Docker.WeeklyPrune.Enabled = true
		cfg.Docker.WeeklyPrune.Day = "sunday"
		cfg.Docker.WeeklyPrune.Hour = 4
	}
	if cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold == 0 {
		cfg.Docker.AutoRestartOnRAMCritical.Enabled = true
		cfg.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour = 3
		cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold = 98.0
	}

	// Intervals defaults
	if cfg.Intervals.StatsSeconds == 0 {
		cfg.Intervals.StatsSeconds = 5
	}
	if cfg.Intervals.MonitorSeconds == 0 {
		cfg.Intervals.MonitorSeconds = 30
	}
	if cfg.Intervals.CriticalAlertCooldownMins == 0 {
		cfg.Intervals.CriticalAlertCooldownMins = 30
	}
}

// isQuietHours checks if we are in quiet hours based on config
func isQuietHours() bool {
	if !quietHoursEnabled {
		return false
	}

	now := time.Now().In(location)
	hour := now.Hour()
	minute := now.Minute()
	currentMins := hour*60 + minute

	startMins := quietStartHour*60 + quietStartMinute
	endMins := quietEndHour*60 + quietEndMinute

	// Handle overnight quiet hours (e.g., 23:30 - 07:00)
	if startMins > endMins {
		return currentMins >= startMins || currentMins < endMins
	}
	return currentMins >= startMins && currentMins < endMins
}

func loadState() {
	data, err := os.ReadFile(DefaultStateFile)
	if err != nil {
		log.Printf("[i] First run - no state")
		return
	}
	var state BotState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("[w] State error: %v", err)
		return
	}
	lastReportTime = state.LastReportTime
	if state.AutoRestarts != nil {
		autoRestarts = state.AutoRestarts
	}
	if state.Language != "" {
		currentLanguage = state.Language
	}
	if state.ReportMode > 0 {
		reportMode = state.ReportMode
	}

	// Load user-configurable settings
	if state.ReportMorningHour > 0 || state.ReportMorningMinute > 0 {
		reportMorningHour = state.ReportMorningHour
		reportMorningMinute = state.ReportMorningMinute
	}
	if state.ReportEveningHour > 0 || state.ReportEveningMinute > 0 {
		reportEveningHour = state.ReportEveningHour
		reportEveningMinute = state.ReportEveningMinute
	}

	if state.QuietStartHour > 0 || state.QuietStartMinute > 0 {
		quietHoursEnabled = state.QuietHoursEnabled
		quietStartHour = state.QuietStartHour
		quietStartMinute = state.QuietStartMinute
		quietEndHour = state.QuietEndHour
		quietEndMinute = state.QuietEndMinute
	}

	if state.DockerPruneDay != "" {
		dockerPruneEnabled = state.DockerPruneEnabled
		dockerPruneDay = state.DockerPruneDay
		dockerPruneHour = state.DockerPruneHour
	}

	log.Printf("[+] State restored")
}

func saveState() {
	autoRestartsMutex.Lock()
	state := BotState{
		LastReportTime:      lastReportTime,
		AutoRestarts:        autoRestarts,
		Language:            currentLanguage,
		ReportMode:          reportMode,
		ReportMorningHour:   reportMorningHour,
		ReportMorningMinute: reportMorningMinute,
		ReportEveningHour:   reportEveningHour,
		ReportEveningMinute: reportEveningMinute,
		QuietHoursEnabled:   quietHoursEnabled,
		QuietStartHour:      quietStartHour,
		QuietStartMinute:    quietStartMinute,
		QuietEndHour:        quietEndHour,
		QuietEndMinute:      quietEndMinute,
		DockerPruneEnabled:  dockerPruneEnabled,
		DockerPruneDay:      dockerPruneDay,
		DockerPruneHour:     dockerPruneHour,
	}
	autoRestartsMutex.Unlock()

	data, err := json.Marshal(state)
	if err != nil {
		log.Printf("[w] Serialize: %v", err)
		return
	}
	if err := os.WriteFile(DefaultStateFile, data, 0644); err != nil {
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
		log.Fatalf("[!] Start bot: %v", err)
	}
	log.Printf("[+] NASBot @%s", bot.Self.UserName)

	// Startup notification
	nextReportStr := getNextReportDescription()

	var quietInfo string
	if quietHoursEnabled {
		quietInfo = fmt.Sprintf("\n🌙 _Quiet: %02d:%02d — %02d:%02d_",
			quietStartHour, quietStartMinute,
			quietEndHour, quietEndMinute)
	}

	startupText := fmt.Sprintf(`*NASBot is online* 👋

I'll keep an eye on things.%s%s

_Type /help to see what I can do_`, nextReportStr, quietInfo)

	startupMsg := tgbotapi.NewMessage(AllowedUserID, startupText)
	startupMsg.ParseMode = "Markdown"
	bot.Send(startupMsg)

	// Graceful shutdown management
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("[-] Shutdown")
		saveState()
		os.Exit(0)
	}()

	// Start background goroutines
	go statsCollector()
	go monitorAlerts(bot)
	go periodicReport(bot)
	go autonomousManager(bot)

	// Wait for first stats cycle
	time.Sleep(IntervalStats + 500*time.Millisecond)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// Callback (inline buttons)
		if update.CallbackQuery != nil {
			go handleCallback(bot, update.CallbackQuery)
			continue
		}
		// Commands
		if update.Message == nil || update.Message.Chat.ID != AllowedUserID {
			continue
		}
		if update.Message.IsCommand() {
			go handleCommand(bot, update.Message)
		}
	}
}

// getNextReportDescription returns a description of the next scheduled report
func getNextReportDescription() string {
	if reportMode == 0 {
		return "\n📭 _Reports disabled_"
	}

	now := time.Now().In(location)

	if reportMode == 2 {
		// Both enabled, find next
		morning := time.Date(now.Year(), now.Month(), now.Day(),
			reportMorningHour, reportMorningMinute, 0, 0, location)
		evening := time.Date(now.Year(), now.Month(), now.Day(),
			reportEveningHour, reportEveningMinute, 0, 0, location)

		if now.Before(morning) {
			return fmt.Sprintf("\n📨 _Next report: %02d:%02d_", reportMorningHour, reportMorningMinute)
		} else if now.Before(evening) {
			return fmt.Sprintf("\n📨 _Next report: %02d:%02d_", reportEveningHour, reportEveningMinute)
		}
		return fmt.Sprintf("\n📨 _Next report: %02d:%02d (tomorrow)_", reportMorningHour, reportMorningMinute)
	}

	// Once daily (morning)
	return fmt.Sprintf("\n📨 _Daily report: %02d:%02d_", reportMorningHour, reportMorningMinute)
}

// ═══════════════════════════════════════════════════════════════════
//  COMMAND HANDLERS
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
	case "top":
		sendWithKeyboard(bot, chatID, getTopProcText())
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
	case "kill":
		handleKillCommand(bot, chatID, args)
	case "ping":
		sendMarkdown(bot, chatID, getPingText())
	case "config":
		sendMarkdown(bot, chatID, getConfigText())
	case "sysinfo":
		sendMarkdown(bot, chatID, getSysInfoText())
	case "speedtest":
		handleSpeedtest(bot, chatID)
	case "diskpred", "prediction":
		sendMarkdown(bot, chatID, getDiskPredictionText())
	case "restartdocker":
		askDockerRestartConfirmation(bot, chatID)
	case "reboot":
		askPowerConfirmation(bot, chatID, 0, "reboot")
	case "shutdown":
		askPowerConfirmation(bot, chatID, 0, "shutdown")
	case "language":
		sendLanguageSelection(bot, chatID)
	case "settings":
		sendSettingsMenu(bot, chatID)
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

	// Language selection
	if data == "set_lang_en" {
		currentLanguage = "en"
		saveState()
		editMessage(bot, chatID, msgID, tr("lang_set_en"), nil)
		return
	}
	if data == "set_lang_it" {
		currentLanguage = "it"
		saveState()
		editMessage(bot, chatID, msgID, tr("lang_set_it"), nil)
		return
	}

	// Settings menu
	if data == "settings_change_lang" {
		msg := tgbotapi.NewEditMessageText(chatID, msgID, tr("lang_select"))
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🇬🇧 English", "set_lang_en_settings"),
				tgbotapi.NewInlineKeyboardButtonData("🇮🇹 Italiano", "set_lang_it_settings"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
			),
		)
		msg.ReplyMarkup = &kb
		bot.Send(msg)
		return
	}
	if data == "set_lang_en_settings" {
		currentLanguage = "en"
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "set_lang_it_settings" {
		currentLanguage = "it"
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_reports" {
		text, kb := getReportSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if strings.HasPrefix(data, "set_reports_") {
		mode := 0
		if data == "set_reports_1" {
			mode = 1
		} else if data == "set_reports_2" {
			mode = 2
		}
		reportMode = mode
		saveState()
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "back_settings" {
		text, kb := getSettingsMenuText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_quiet" {
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "quiet_enable" {
		quietHoursEnabled = true
		saveState()
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "quiet_disable" {
		quietHoursEnabled = false
		saveState()
		text, kb := getQuietHoursSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "settings_change_prune" {
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_enable" {
		dockerPruneEnabled = true
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_disable" {
		dockerPruneEnabled = false
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if data == "prune_change_schedule" {
		text, kb := getPruneScheduleText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}
	if strings.HasPrefix(data, "prune_day_") {
		day := strings.TrimPrefix(data, "prune_day_")
		dockerPruneDay = day
		saveState()
		text, kb := getDockerPruneSettingsText()
		editMessage(bot, chatID, msgID, text, &kb)
		return
	}

	// Power confirmation management
	if data == "confirm_reboot" || data == "confirm_shutdown" {
		handlePowerConfirm(bot, chatID, msgID, data)
		return
	}
	if data == "cancel_power" {
		editMessage(bot, chatID, msgID, "❌ Cancelled", nil)
		return
	}
	// Power menu pre-confirmation management
	if data == "pre_confirm_reboot" || data == "pre_confirm_shutdown" {
		action := strings.TrimPrefix(data, "pre_confirm_")
		askPowerConfirmation(bot, chatID, msgID, action)
		return
	}

	// Docker service restart confirmation
	if data == "confirm_restart_docker" {
		executeDockerServiceRestart(bot, chatID, msgID)
		return
	}
	if data == "cancel_restart_docker" {
		editMessage(bot, chatID, msgID, "❌ Docker restart cancelled", nil)
		return
	}

	// Container actions management
	if strings.HasPrefix(data, "container_") {
		handleContainerCallback(bot, chatID, msgID, data)
		return
	}

	// Normal navigation
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
	case "show_top":
		text = getTopProcText()
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
	case "show_power":
		text, kb = getPowerMenuText()
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
//  TEXT GENERATORS (use cache, instant response)
// ═══════════════════════════════════════════════════════════════════

func getStatusText() string {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return tr("loading")
	}

	var b strings.Builder

	b.WriteString(fmt.Sprintf(tr("status_title"), time.Now().Format("15:04")))

	b.WriteString(fmt.Sprintf(tr("cpu_fmt"), makeProgressBar(s.CPU), s.CPU))
	b.WriteString(fmt.Sprintf(tr("ram_fmt"), makeProgressBar(s.RAM), s.RAM))
	if s.Swap > 5 {
		b.WriteString(fmt.Sprintf(tr("swap_fmt"), makeProgressBar(s.Swap), s.Swap))
	}

	b.WriteString(fmt.Sprintf(tr("ssd_fmt"), s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	b.WriteString(fmt.Sprintf(tr("hdd_fmt"), s.VolHDD.Used, formatBytes(s.VolHDD.Free)))

	if s.DiskUtil > 10 {
		b.WriteString(fmt.Sprintf(tr("disk_io_fmt"), s.DiskUtil))
		if s.ReadMBs > 1 || s.WriteMBs > 1 {
			b.WriteString(fmt.Sprintf(tr("disk_rw_fmt"), s.ReadMBs, s.WriteMBs))
		}
		b.WriteString("\n")
	}

	b.WriteString(fmt.Sprintf(tr("uptime_fmt"), formatUptime(s.Uptime)))

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

	// Round to nearest 10% (55% -> 60% -> 6 notches)
	filled := int((percent + 5) / 10)
	if filled > 10 {
		filled = 10
	}

	// Use block characters for the bar
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

func getTopProcText() string {
	// Execute ps command to get top 10 processes by CPU
	// Output: pid, command, cpu%, mem%
	cmd := exec.Command("ps", "-Ao", "pid,comm,pcpu,pmem", "--sort=-pcpu")
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("❌ Error fetching processes: %v", err)
	}

	lines := strings.Split(string(out), "\n")
	// Skip header (1st line), take next 10
	if len(lines) < 2 {
		return "_No processes found_"
	}

	count := 0
	var b strings.Builder
	b.WriteString("🔥 *Top Processes (by CPU)*\n\n")
	b.WriteString("`PID   CPU%  MEM%  COMMAND`\n")

	// Start from index 1 (skip header)
	for i := 1; i < len(lines) && count < 10; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid := fields[0]
		cmdName := fields[1]
		cpu := fields[2]
		mem := fields[3]

		// If command name is long, truncate
		if len(cmdName) > 15 {
			cmdName = cmdName[:13] + ".."
		}

		// PID   CPU   MEM   CMD
		// 12345 12.3  10.2  python3
		b.WriteString(fmt.Sprintf("`%-5s %-4s %-4s %s`\n",
			pid, cpu, mem, cmdName))

		count++
	}

	return b.String()
}

func getHelpText() string {
	var b strings.Builder
	b.WriteString("👋 *Hey! I'm NASBot*\n\n")
	b.WriteString("Here's what I can do for you:\n\n")

	b.WriteString("*📊 Monitoring*\n")
	b.WriteString("/status — quick system overview\n")
	b.WriteString("/temp — check temperatures\n")
	b.WriteString("/top — top processes by CPU\n")
	b.WriteString("/sysinfo — detailed system info\n")
	b.WriteString("/diskpred — disk space prediction\n\n")

	b.WriteString("*🐳 Docker*\n")
	b.WriteString("/docker — manage containers\n")
	b.WriteString("/dstats — container resources\n")
	b.WriteString("/kill `name` — force kill container\n")
	b.WriteString("/restartdocker — restart Docker service\n\n")

	b.WriteString("*🌐 Network*\n")
	b.WriteString("/net — network info\n")
	b.WriteString("/speedtest — run speed test\n\n")

	b.WriteString("*⚙️ Settings & System*\n")
	b.WriteString("/settings — *configure everything*\n")
	b.WriteString("/report — full detailed report\n")
	b.WriteString("/ping — check if bot is alive\n")
	b.WriteString("/config — show current config\n")
	b.WriteString("/logs — recent system logs\n")
	b.WriteString("/reboot · /shutdown — power control\n\n")

	// Report schedule info
	if reportMode > 0 {
		b.WriteString("_📨 Reports: ")
		if reportMode == 2 {
			b.WriteString(fmt.Sprintf("%02d:%02d & %02d:%02d_\n",
				reportMorningHour, reportMorningMinute,
				reportEveningHour, reportEveningMinute))
		} else {
			b.WriteString(fmt.Sprintf("%02d:%02d_\n", reportMorningHour, reportMorningMinute))
		}
	}

	if quietHoursEnabled {
		b.WriteString(fmt.Sprintf("_🌙 Quiet: %02d:%02d — %02d:%02d_",
			quietStartHour, quietStartMinute,
			quietEndHour, quietEndMinute))
	}

	return b.String()
}

// ═══════════════════════════════════════════════════════════════════
//  NEW COMMANDS: PING, KILL, CONFIG, RESTARTDOCKER
// ═══════════════════════════════════════════════════════════════════

func getPingText() string {
	uptime := time.Since(botStartTime)

	statsMutex.RLock()
	ready := statsReady
	statsMutex.RUnlock()

	status := "✅"
	statusText := "All systems operational"
	if !ready {
		status = "⚠️"
		statusText = "Stats not ready yet"
	}

	return fmt.Sprintf(`%s *Pong!*

%s

🤖 Bot uptime: `+"`%s`"+`
🖥 Collecting stats: %v
📡 Last check: `+"`%s`"+`

_I'm alive and watching!_`,
		status,
		statusText,
		formatDuration(uptime),
		ready,
		time.Now().In(location).Format("15:04:05"))
}

func handleKillCommand(bot *tgbotapi.BotAPI, chatID int64, args string) {
	if args == "" {
		sendMarkdown(bot, chatID, "Usage: `/kill container_name`\n\nThis will forcefully terminate the container (SIGKILL)")
		return
	}

	// Find container
	containers := getContainerList()
	var found *ContainerInfo
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			found = &c
			break
		}
	}

	if found == nil {
		sendMarkdown(bot, chatID, fmt.Sprintf("❓ Container `%s` not found", args))
		return
	}

	if !found.Running {
		sendMarkdown(bot, chatID, fmt.Sprintf("⏸ Container `%s` is not running", args))
		return
	}

	// Execute kill
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "kill", args)
	output, err := cmd.CombinedOutput()

	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		sendMarkdown(bot, chatID, fmt.Sprintf("❌ Failed to kill `%s`:\n`%s`", args, errMsg))
		addReportEvent("warning", fmt.Sprintf("Kill failed: %s - %s", args, errMsg))
	} else {
		sendMarkdown(bot, chatID, fmt.Sprintf("💀 Container `%s` killed", args))
		addReportEvent("action", fmt.Sprintf("Container killed: %s", args))
	}
}

func getConfigText() string {
	var b strings.Builder
	b.WriteString("⚙️ *Current Configuration*\n\n")

	// Reports
	b.WriteString("*Reports:* ")
	if cfg.Reports.Enabled {
		if cfg.Reports.Morning.Enabled && cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf("✅ %02d:%02d & %02d:%02d\n",
				cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute,
				cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else if cfg.Reports.Morning.Enabled {
			b.WriteString(fmt.Sprintf("✅ Morning only (%02d:%02d)\n",
				cfg.Reports.Morning.Hour, cfg.Reports.Morning.Minute))
		} else if cfg.Reports.Evening.Enabled {
			b.WriteString(fmt.Sprintf("✅ Evening only (%02d:%02d)\n",
				cfg.Reports.Evening.Hour, cfg.Reports.Evening.Minute))
		} else {
			b.WriteString("❌ Disabled\n")
		}
	} else {
		b.WriteString("❌ Disabled\n")
	}

	// Quiet hours
	b.WriteString("*Quiet Hours:* ")
	if cfg.QuietHours.Enabled {
		b.WriteString(fmt.Sprintf("✅ %02d:%02d — %02d:%02d\n",
			cfg.QuietHours.StartHour, cfg.QuietHours.StartMinute,
			cfg.QuietHours.EndHour, cfg.QuietHours.EndMinute))
	} else {
		b.WriteString("❌ Disabled\n")
	}

	// Notifications
	b.WriteString("\n*Notifications:*\n")
	if cfg.Notifications.CPU.Enabled {
		b.WriteString(fmt.Sprintf("  CPU: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.CPU.WarningThreshold, cfg.Notifications.CPU.CriticalThreshold))
	} else {
		b.WriteString("  CPU: ❌\n")
	}
	if cfg.Notifications.RAM.Enabled {
		b.WriteString(fmt.Sprintf("  RAM: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.RAM.WarningThreshold, cfg.Notifications.RAM.CriticalThreshold))
	} else {
		b.WriteString("  RAM: ❌\n")
	}
	if cfg.Notifications.Swap.Enabled {
		b.WriteString(fmt.Sprintf("  Swap: ⚠️ >%.0f%%\n", cfg.Notifications.Swap.WarningThreshold))
	} else {
		b.WriteString("  Swap: ❌\n")
	}
	if cfg.Notifications.DiskSSD.Enabled {
		b.WriteString(fmt.Sprintf("  SSD: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.DiskSSD.WarningThreshold, cfg.Notifications.DiskSSD.CriticalThreshold))
	} else {
		b.WriteString("  SSD: ❌\n")
	}
	if cfg.Notifications.DiskHDD.Enabled {
		b.WriteString(fmt.Sprintf("  HDD: ⚠️ >%.0f%% | 🚨 >%.0f%%\n",
			cfg.Notifications.DiskHDD.WarningThreshold, cfg.Notifications.DiskHDD.CriticalThreshold))
	} else {
		b.WriteString("  HDD: ❌\n")
	}
	if cfg.Notifications.DiskIO.Enabled {
		b.WriteString(fmt.Sprintf("  I/O: ⚠️ >%.0f%%\n", cfg.Notifications.DiskIO.WarningThreshold))
	} else {
		b.WriteString("  I/O: ❌\n")
	}
	b.WriteString(fmt.Sprintf("  SMART: %s\n", boolToEmoji(cfg.Notifications.SMART.Enabled)))

	// Docker
	b.WriteString("\n*Docker:*\n")
	if cfg.Docker.Watchdog.Enabled {
		b.WriteString(fmt.Sprintf("  Watchdog: ✅ %dm timeout\n", cfg.Docker.Watchdog.TimeoutMinutes))
	} else {
		b.WriteString("  Watchdog: ❌\n")
	}
	if cfg.Docker.WeeklyPrune.Enabled {
		b.WriteString(fmt.Sprintf("  Prune: ✅ %s @ %02d:00\n",
			strings.Title(cfg.Docker.WeeklyPrune.Day), cfg.Docker.WeeklyPrune.Hour))
	} else {
		b.WriteString("  Prune: ❌\n")
	}
	if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
		b.WriteString(fmt.Sprintf("  Auto-restart: ✅ RAM >%.0f%%\n",
			cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold))
	} else {
		b.WriteString("  Auto-restart: ❌\n")
	}

	// Intervals
	b.WriteString(fmt.Sprintf("\n*Intervals:* Stats %ds · Monitor %ds",
		cfg.Intervals.StatsSeconds, cfg.Intervals.MonitorSeconds))

	return b.String()
}

func boolToEmoji(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

func askDockerRestartConfirmation(bot *tgbotapi.BotAPI, chatID int64) {
	text := "🐳 *Restart Docker Service?*\n\n⚠️ This will restart the Docker daemon.\nAll containers will be temporarily stopped."

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, restart", "confirm_restart_docker"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_restart_docker"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

func executeDockerServiceRestart(bot *tgbotapi.BotAPI, chatID int64, msgID int) {
	editMessage(bot, chatID, msgID, "🔄 Restarting Docker service...", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Try systemctl first, then service
	var cmd *exec.Cmd
	if _, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.CommandContext(ctx, "systemctl", "restart", "docker")
	} else {
		cmd = exec.CommandContext(ctx, "service", "docker", "restart")
	}

	output, err := cmd.CombinedOutput()
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf("❌ Docker restart failed:\n`%s`", errMsg)
		addReportEvent("critical", fmt.Sprintf("Docker restart failed: %s", errMsg))
	} else {
		resultText = "✅ Docker service restarted successfully"
		addReportEvent("action", "Docker service restarted (manual)")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Check Containers", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

// ═══════════════════════════════════════════════════════════════════
//  SYSTEM INFO, SPEEDTEST & DISK PREDICTION
// ═══════════════════════════════════════════════════════════════════

// getSysInfoText returns detailed system information
func getSysInfoText() string {
	var b strings.Builder
	b.WriteString("🖥 *System Information*\n\n")

	// Host info
	h, err := host.Info()
	if err == nil {
		b.WriteString(fmt.Sprintf("*Hostname:* `%s`\n", h.Hostname))
		b.WriteString(fmt.Sprintf("*OS:* %s %s\n", h.Platform, h.PlatformVersion))
		b.WriteString(fmt.Sprintf("*Kernel:* %s\n", h.KernelVersion))
		b.WriteString(fmt.Sprintf("*Architecture:* %s\n", h.KernelArch))
		b.WriteString(fmt.Sprintf("*Uptime:* %s\n", formatUptime(h.Uptime)))
		b.WriteString(fmt.Sprintf("*Boot Time:* %s\n", time.Unix(int64(h.BootTime), 0).In(location).Format("02/01/2006 15:04")))
	}

	// CPU info
	cpuInfo, err := cpu.Info()
	if err == nil && len(cpuInfo) > 0 {
		b.WriteString(fmt.Sprintf("\n*CPU:* %s\n", cpuInfo[0].ModelName))
		b.WriteString(fmt.Sprintf("*Cores:* %d physical, %d logical\n", cpuInfo[0].Cores, len(cpuInfo)))
		if cpuInfo[0].Mhz > 0 {
			b.WriteString(fmt.Sprintf("*Frequency:* %.0f MHz\n", cpuInfo[0].Mhz))
		}
	}

	// Memory info
	v, err := mem.VirtualMemory()
	if err == nil {
		b.WriteString(fmt.Sprintf("\n*Total RAM:* %.1f GB\n", float64(v.Total)/1024/1024/1024))
	}

	// Disk info
	for _, path := range []string{PathSSD, PathHDD} {
		d, err := disk.Usage(path)
		if err == nil {
			name := "SSD"
			if path == PathHDD {
				name = "HDD"
			}
			b.WriteString(fmt.Sprintf("*%s (%s):* %.0f GB total\n", name, path, float64(d.Total)/1024/1024/1024))
		}
	}

	// Go runtime info
	b.WriteString("\n*NASBot Version:* 1.0.0\n")
	if buildInfo, ok := debug.ReadBuildInfo(); ok {
		b.WriteString(fmt.Sprintf("*Go Version:* %s\n", buildInfo.GoVersion))
	}

	return b.String()
}

// handleSpeedtest runs a network speed test
func sendLanguageSelection(bot *tgbotapi.BotAPI, chatID int64) {
	msg := tgbotapi.NewMessage(chatID, tr("lang_select"))
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🇬🇧 English", "set_lang_en"),
			tgbotapi.NewInlineKeyboardButtonData("🇮🇹 Italiano", "set_lang_it"),
		),
	)
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

func sendSettingsMenu(bot *tgbotapi.BotAPI, chatID int64) {
	text, kb := getSettingsMenuText()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

func getSettingsMenuText() (string, tgbotapi.InlineKeyboardMarkup) {
	langName := "English 🇬🇧"
	if currentLanguage == "it" {
		langName = "Italiano 🇮🇹"
	}

	reportText := tr("report_disabled")
	if reportMode == 1 {
		reportText = tr("report_once")
	} else if reportMode == 2 {
		reportText = tr("report_twice")
	}

	quietText := tr("quiet_disabled")
	if quietHoursEnabled {
		quietText = fmt.Sprintf("%02d:%02d - %02d:%02d", quietStartHour, quietStartMinute, quietEndHour, quietEndMinute)
	}

	pruneText := tr("prune_disabled")
	if dockerPruneEnabled {
		pruneText = fmt.Sprintf("%s %02d:00", tr(dockerPruneDay), dockerPruneHour)
	}

	text := fmt.Sprintf("%s\n\n", tr("settings_title"))
	text += fmt.Sprintf("🌐 %s: %s\n", tr("settings_lang"), langName)
	text += fmt.Sprintf("📨 %s: %s\n", tr("settings_reports"), reportText)
	text += fmt.Sprintf("🌙 %s: %s\n", tr("settings_quiet"), quietText)
	text += fmt.Sprintf("🧹 %s: %s\n", tr("settings_prune"), pruneText)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌐 "+tr("settings_lang"), "settings_change_lang"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📨 "+tr("settings_reports"), "settings_change_reports"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌙 "+tr("settings_quiet"), "settings_change_quiet"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🧹 "+tr("settings_prune"), "settings_change_prune"),
		),
	)

	return text, kb
}

func getReportSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "📨 *Report Giornalieri*\n\nSeleziona la frequenza dei report automatici:"
	} else {
		text = "📨 *Daily Reports*\n\nSelect automatic report frequency:"
	}

	checkDisabled := " "
	checkOnce := " "
	checkTwice := " "

	if reportMode == 0 {
		checkDisabled = "✓"
	} else if reportMode == 1 {
		checkOnce = "✓"
	} else if reportMode == 2 {
		checkTwice = "✓"
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkDisabled+" "+tr("report_disabled"), "set_reports_0"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkOnce+" "+tr("report_once"), "set_reports_1"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(checkTwice+" "+tr("report_twice"), "set_reports_2"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
		),
	)

	return text, kb
}

func getQuietHoursSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "🌙 *Ore Silenziose*\n\nDurante questo periodo non riceverai notifiche.\n\n"
	} else {
		text = "🌙 *Quiet Hours*\n\nNo notifications during this period.\n\n"
	}

	if quietHoursEnabled {
		text += fmt.Sprintf("Attualmente: %02d:%02d - %02d:%02d\n", quietStartHour, quietStartMinute, quietEndHour, quietEndMinute)
	} else {
		text += tr("disabled") + "\n"
	}

	toggleText := tr("enable")
	toggleData := "quiet_enable"
	if quietHoursEnabled {
		toggleText = tr("disable")
		toggleData = "quiet_disable"
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
		),
	)

	return text, kb
}

func getDockerPruneSettingsText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "🧹 *Pulizia Docker*\n\nPulizia automatica delle immagini inutilizzate.\n\n"
	} else {
		text = "🧹 *Docker Prune*\n\nAutomatic cleanup of unused images.\n\n"
	}

	if dockerPruneEnabled {
		dayName := tr(dockerPruneDay)
		text += fmt.Sprintf("%s: %s %02d:00\n", tr("schedule"), dayName, dockerPruneHour)
	} else {
		text += tr("disabled") + "\n"
	}

	toggleText := tr("enable")
	toggleData := "prune_enable"
	if dockerPruneEnabled {
		toggleText = tr("disable")
		toggleData = "prune_disable"
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		{tgbotapi.NewInlineKeyboardButtonData(toggleText, toggleData)},
	}

	if dockerPruneEnabled {
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(tr("schedule"), "prune_change_schedule"),
		})
	}

	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "back_settings"),
	})

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)

	return text, kb
}

func getPruneScheduleText() (string, tgbotapi.InlineKeyboardMarkup) {
	var text string
	if currentLanguage == "it" {
		text = "📅 *Programmazione Pulizia*\n\nSeleziona il giorno:"
	} else {
		text = "📅 *Prune Schedule*\n\nSelect day:"
	}

	days := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	var rows [][]tgbotapi.InlineKeyboardButton

	for _, day := range days {
		check := " "
		if dockerPruneDay == day {
			check = "✓"
		}
		rows = append(rows, []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(check+" "+tr(day), "prune_day_"+day),
		})
	}

	rows = append(rows, []tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "settings_change_prune"),
	})

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)

	return text, kb
}

func handleSpeedtest(bot *tgbotapi.BotAPI, chatID int64) {
	// Check if speedtest-cli is available
	if _, err := exec.LookPath("speedtest-cli"); err != nil {
		sendMarkdown(bot, chatID, "❌ `speedtest-cli` not installed.\n\nInstall it with:\n`sudo apt install speedtest-cli`")
		return
	}

	// Send initial message
	msg := tgbotapi.NewMessage(chatID, "🚀 Running speed test... (this may take a minute)")
	sent, _ := bot.Send(msg)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "speedtest-cli", "--simple")
	output, err := cmd.CombinedOutput()

	var resultText string
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			resultText = "⏱ Speed test timed out"
		} else {
			resultText = fmt.Sprintf("❌ Speed test failed:\n`%s`", err.Error())
		}
	} else {
		// Parse output
		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var ping, download, upload string
		for _, line := range lines {
			if strings.HasPrefix(line, "Ping:") {
				ping = strings.TrimPrefix(line, "Ping: ")
			} else if strings.HasPrefix(line, "Download:") {
				download = strings.TrimPrefix(line, "Download: ")
			} else if strings.HasPrefix(line, "Upload:") {
				upload = strings.TrimPrefix(line, "Upload: ")
			}
		}

		resultText = fmt.Sprintf("🚀 *Speed Test Results*\n\n"+
			"📡 Ping: `%s`\n"+
			"⬇️ Download: `%s`\n"+
			"⬆️ Upload: `%s`",
			ping, download, upload)
	}

	// Edit the original message with results
	edit := tgbotapi.NewEditMessageText(chatID, sent.MessageID, resultText)
	edit.ParseMode = "Markdown"
	bot.Send(edit)
}

// getDiskPredictionText estimates when disks will be full
func getDiskPredictionText() string {
	diskUsageHistoryMutex.Lock()
	history := make([]DiskUsagePoint, len(diskUsageHistory))
	copy(history, diskUsageHistory)
	diskUsageHistoryMutex.Unlock()

	var b strings.Builder
	b.WriteString("📊 *Disk Space Prediction*\n\n")

	if len(history) < 12 { // Need at least 1 hour of data
		b.WriteString("_Collecting data... need at least 1 hour of history._\n\n")
		b.WriteString(fmt.Sprintf("_Data points: %d/12_", len(history)))
		return b.String()
	}

	// Calculate trend for SSD
	ssdPred := predictDiskFull(history, true)
	hddPred := predictDiskFull(history, false)

	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	// SSD
	b.WriteString(fmt.Sprintf("💿 *SSD* — %.1f%% used\n", s.VolSSD.Used))
	if ssdPred.DaysUntilFull < 0 {
		b.WriteString("   📈 _Usage decreasing or stable_\n")
	} else if ssdPred.DaysUntilFull > 365 {
		b.WriteString("   ✅ _More than a year until full_\n")
	} else if ssdPred.DaysUntilFull > 30 {
		b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(ssdPred.DaysUntilFull)))
	} else if ssdPred.DaysUntilFull > 7 {
		b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(ssdPred.DaysUntilFull)))
	} else {
		b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(ssdPred.DaysUntilFull)))
	}
	b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n\n", ssdPred.GBPerDay))

	// HDD
	b.WriteString(fmt.Sprintf("🗄 *HDD* — %.1f%% used\n", s.VolHDD.Used))
	if hddPred.DaysUntilFull < 0 {
		b.WriteString("   📈 _Usage decreasing or stable_\n")
	} else if hddPred.DaysUntilFull > 365 {
		b.WriteString("   ✅ _More than a year until full_\n")
	} else if hddPred.DaysUntilFull > 30 {
		b.WriteString(fmt.Sprintf("   ✅ ~%d days until full\n", int(hddPred.DaysUntilFull)))
	} else if hddPred.DaysUntilFull > 7 {
		b.WriteString(fmt.Sprintf("   ⚠️ ~%d days until full\n", int(hddPred.DaysUntilFull)))
	} else {
		b.WriteString(fmt.Sprintf("   🚨 ~%d days until full!\n", int(hddPred.DaysUntilFull)))
	}
	b.WriteString(fmt.Sprintf("   _Rate: %.2f GB/day_\n", hddPred.GBPerDay))

	b.WriteString(fmt.Sprintf("\n_Based on %d data points (%s of data)_",
		len(history),
		formatDuration(time.Since(history[0].Time))))

	return b.String()
}

// DiskPrediction holds prediction results
type DiskPrediction struct {
	DaysUntilFull float64
	GBPerDay      float64
}

// predictDiskFull calculates days until disk is full using linear regression
func predictDiskFull(history []DiskUsagePoint, isSSD bool) DiskPrediction {
	if len(history) < 2 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	// Simple linear regression on free space
	first := history[0]
	last := history[len(history)-1]

	var firstFree, lastFree uint64
	if isSSD {
		firstFree = first.SSDFree
		lastFree = last.SSDFree
	} else {
		firstFree = first.HDDFree
		lastFree = last.HDDFree
	}

	timeDiff := last.Time.Sub(first.Time).Hours() / 24 // Days
	if timeDiff < 0.01 {
		return DiskPrediction{DaysUntilFull: -1}
	}

	// GB change (negative means filling up)
	gbChange := float64(int64(lastFree)-int64(firstFree)) / 1024 / 1024 / 1024
	gbPerDay := gbChange / timeDiff

	if gbPerDay >= 0 {
		// Disk is freeing up or stable
		return DiskPrediction{DaysUntilFull: -1, GBPerDay: -gbPerDay}
	}

	// Days until free space = 0
	currentFreeGB := float64(lastFree) / 1024 / 1024 / 1024
	daysUntilFull := currentFreeGB / (-gbPerDay)

	return DiskPrediction{
		DaysUntilFull: daysUntilFull,
		GBPerDay:      -gbPerDay,
	}
}

// recordDiskUsage adds current disk usage to history
func recordDiskUsage() {
	statsMutex.RLock()
	s := statsCache
	ready := statsReady
	statsMutex.RUnlock()

	if !ready {
		return
	}

	diskUsageHistoryMutex.Lock()
	defer diskUsageHistoryMutex.Unlock()

	point := DiskUsagePoint{
		Time:    time.Now(),
		SSDUsed: s.VolSSD.Used,
		HDDUsed: s.VolHDD.Used,
		SSDFree: s.VolSSD.Free,
		HDDFree: s.VolHDD.Free,
	}

	diskUsageHistory = append(diskUsageHistory, point)

	// Keep max 7 days of data (at 5-min intervals = 2016 points)
	if len(diskUsageHistory) > 2016 {
		diskUsageHistory = diskUsageHistory[1:]
	}
}

// ═══════════════════════════════════════════════════════════════════
//  CONTAINER STATE MONITORING (unexpected stops)
// ═══════════════════════════════════════════════════════════════════

// checkContainerStates monitors for unexpected container stops
func checkContainerStates(bot *tgbotapi.BotAPI) {
	containers := getContainerList()
	if containers == nil {
		return
	}

	containerStateMutex.Lock()
	defer containerStateMutex.Unlock()

	// Build current state map
	currentStates := make(map[string]bool)
	for _, c := range containers {
		currentStates[c.Name] = c.Running
	}

	// Check for containers that stopped unexpectedly
	for name, wasRunning := range lastContainerStates {
		isRunning, exists := currentStates[name]
		if exists && wasRunning && !isRunning {
			// Container was running but now stopped
			if !isQuietHours() {
				msg := fmt.Sprintf("⚠️ *Container Stopped*\n\n`%s` has stopped unexpectedly.", name)
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
			}
			addReportEvent("warning", fmt.Sprintf("Container stopped: %s", name))
		}
	}

	// Update last states
	lastContainerStates = currentStates
}

// ═══════════════════════════════════════════════════════════════════
//  DOCKER CONTAINER MANAGEMENT
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

	// Buttons - 2 per row
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
	case "select", "start", "stop", "restart", "logs", "kill", "cancel":
		// Container name is everything after parts[1]
		containerName := strings.Join(parts[2:], "_")
		switch action {
		case "select":
			showContainerActions(bot, chatID, msgID, containerName)
		case "start", "stop", "restart", "logs", "kill":
			confirmContainerAction(bot, chatID, msgID, containerName, action)
		case "cancel":
			text, kb := getDockerMenuText()
			editMessage(bot, chatID, msgID, text, kb)
		}
	case "confirm":
		// Format: container_confirm_CONTAINERNAME_ACTION
		// Action is the last element, name is everything else
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
			tgbotapi.NewInlineKeyboardButtonData(tr("stop"), "container_stop_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(tr("restart"), "container_restart_"+containerName),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("kill"), "container_kill_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(tr("logs"), "container_logs_"+containerName),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("start"), "container_start_"+containerName),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "show_docker"),
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
		"start":   tr("start"),
		"stop":    tr("stop"),
		"restart": tr("restart"),
		"kill":    tr("kill"),
	}[action]

	text := fmt.Sprintf(tr("confirm_action"), actionText, containerName)
	if action == "kill" {
		text += tr("kill_warn")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("yes"), fmt.Sprintf("container_confirm_%s_%s", containerName, action)),
			tgbotapi.NewInlineKeyboardButtonData(tr("no"), "container_cancel_"+containerName),
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
	case "kill":
		cmd = exec.CommandContext(ctx, "docker", "kill", containerName)
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
		actionPast := map[string]string{"start": "started ▶️", "stop": "stopped ⏹", "restart": "restarted 🔄", "kill": "killed 💀"}[action]
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

	// Search container by name
	containers := getContainerList()
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			// Send info with menu
			msg := tgbotapi.NewMessage(chatID, "")
			msg.ParseMode = "Markdown"
			text, _ := getContainerInfoText(c)
			msg.Text = text
			bot.Send(msg)
			return
		}
	}
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("x Container `%s` not found.", args)))
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
//  POWER MANAGEMENT (with confirmation)
// ═══════════════════════════════════════════════════════════════════

func getPowerMenuText() (string, *tgbotapi.InlineKeyboardMarkup) {
	text := "⚡ *Power Management*\n\nBe careful, these actions affect the physical server."
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Reboot NAS", "pre_confirm_reboot"),
			tgbotapi.NewInlineKeyboardButtonData("🛑 Shutdown NAS", "pre_confirm_shutdown"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "back_main"),
		),
	)
	return text, &kb
}

func askPowerConfirmation(bot *tgbotapi.BotAPI, chatID int64, msgID int, action string) {
	pendingActionMutex.Lock()
	pendingAction = action
	pendingActionMutex.Unlock()

	question := "🔄 *Reboot* the NAS?"
	if action == "shutdown" {
		question = "⚠️ *Shut down* the NAS?"
	}
	question += "\n\n_Are you sure?_"

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, do it", "confirm_"+action),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_power"),
		),
	)

	if msgID > 0 {
		editMessage(bot, chatID, msgID, question, &kb)
	} else {
		msg := tgbotapi.NewMessage(chatID, question)
		msg.ParseMode = "Markdown"
		msg.ReplyMarkup = kb
		bot.Send(msg)
	}
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
//  DAILY REPORTS (configurable times)
// ═══════════════════════════════════════════════════════════════════

// getNextReportTime calculates the next report time based on reportMode
func getNextReportTime() (time.Time, bool) {
	now := time.Now().In(location)

	if reportMode == 0 {
		// No reports enabled, return far future
		return now.Add(24 * 365 * time.Hour), false
	}

	morningReport := time.Date(now.Year(), now.Month(), now.Day(),
		reportMorningHour, reportMorningMinute, 0, 0, location)
	eveningReport := time.Date(now.Year(), now.Month(), now.Day(),
		reportEveningHour, reportEveningMinute, 0, 0, location)

	// Determine next report based on reportMode
	if reportMode == 2 {
		// Twice daily
		if now.Before(morningReport) {
			return morningReport, true
		} else if now.Before(eveningReport) {
			return eveningReport, false
		}
		return morningReport.Add(24 * time.Hour), true
	}

	// Once daily (morning)
	if now.Before(morningReport) {
		return morningReport, true
	}
	return morningReport.Add(24 * time.Hour), true
}

func periodicReport(bot *tgbotapi.BotAPI) {
	// Wait for stats to be ready
	time.Sleep(IntervalStats * 2)

	for {
		// Check if reports are enabled
		if reportMode == 0 {
			// Sleep for a while and check again (mode might change)
			time.Sleep(1 * time.Hour)
			continue
		}

		// Calculate next report time
		nextReport, isMorning := getNextReportTime()
		sleepDuration := time.Until(nextReport)

		greeting := tr("good_morning")
		if !isMorning {
			greeting = tr("good_evening")
		}

		log.Printf("> Next report: %s", nextReport.Format("02/01 15:04"))
		time.Sleep(sleepDuration)

		// Generate and send report
		report := generateDailyReport(greeting, isMorning)
		msg := tgbotapi.NewMessage(AllowedUserID, report)
		msg.ParseMode = "Markdown"
		bot.Send(msg)

		lastReportTime = time.Now()
		saveState()

		// Clean old events (keep last 2 days)
		cleanOldReportEvents()
	}
}

func generateDailyReport(greeting string, isMorning bool) string {
	statsMutex.RLock()
	s := statsCache
	statsMutex.RUnlock()

	var b strings.Builder
	now := time.Now().In(location)

	b.WriteString(fmt.Sprintf("*%s*\n", greeting))
	b.WriteString(fmt.Sprintf("_%s_\n\n", now.Format("Mon 02/01")))

	// Get events for AI summary
	reportEventsMutex.Lock()
	events := filterSignificantEvents(reportEvents)
	reportEventsMutex.Unlock()

	// Try to get AI summary first
	aiSummary := generateAISummary(s, events, isMorning)
	if aiSummary != "" {
		b.WriteString(fmt.Sprintf("🤖 _%s_\n\n", aiSummary))
	} else {
		// Fallback to standard health status
		healthIcon, healthText, _ := getHealthStatus(s)
		b.WriteString(fmt.Sprintf("%s %s\n\n", healthIcon, healthText))
	}

	if len(events) > 0 {
		b.WriteString(fmt.Sprintf("*%s*\n", tr("report_events")))
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

	b.WriteString(fmt.Sprintf("*%s*\n", tr("report_resources")))
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
	containerLabel := tr("containers_running")
	if running == 1 {
		containerLabel = tr("container_running")
	}
	b.WriteString(fmt.Sprintf("\n🐳 %d %s", running, containerLabel))
	if stopped > 0 {
		b.WriteString(fmt.Sprintf(", %d %s", stopped, tr("containers_stopped")))
	}

	stressSummary := getStressSummary()
	if stressSummary != "" {
		b.WriteString(fmt.Sprintf("\n\n💨 *%s*\n", tr("report_stress")))
		b.WriteString(stressSummary)
	}

	b.WriteString(fmt.Sprintf("\n\n_⏱ Up for %s_", formatUptime(s.Uptime)))

	resetStressCounters()
	return b.String()
}

// generateAISummary calls Gemini API to generate a brief summary of the NAS status
func generateAISummary(s Stats, events []ReportEvent, isMorning bool) string {
	if cfg.GeminiAPIKey == "" {
		return ""
	}

	// Build a context for the AI
	var context strings.Builder
	context.WriteString("NAS System Status:\n")
	context.WriteString(fmt.Sprintf("- CPU: %.1f%%\n", s.CPU))
	context.WriteString(fmt.Sprintf("- RAM: %.1f%%\n", s.RAM))
	if s.Swap > 5 {
		context.WriteString(fmt.Sprintf("- Swap: %.1f%%\n", s.Swap))
	}
	context.WriteString(fmt.Sprintf("- SSD: %.1f%% used, %s free\n", s.VolSSD.Used, formatBytes(s.VolSSD.Free)))
	context.WriteString(fmt.Sprintf("- HDD: %.1f%% used, %s free\n", s.VolHDD.Used, formatBytes(s.VolHDD.Free)))
	context.WriteString(fmt.Sprintf("- Uptime: %s\n", formatUptime(s.Uptime)))

	// Add container info
	containers := getContainerList()
	running, stopped := 0, 0
	for _, c := range containers {
		if c.Running {
			running++
		} else {
			stopped++
		}
	}
	context.WriteString(fmt.Sprintf("- Docker: %d running, %d stopped\n", running, stopped))

	// Add events
	if len(events) > 0 {
		context.WriteString("\nRecent Events:\n")
		for _, e := range events {
			context.WriteString(fmt.Sprintf("- [%s] %s: %s\n", e.Time.In(location).Format("15:04"), e.Type, e.Message))
		}
	}

	// Determine time of day for context
	timeOfDay := "morning"
	if !isMorning {
		timeOfDay = "evening"
	}

	// Language for response
	lang := "English"
	if currentLanguage == "it" {
		lang = "Italian"
	}

	prompt := fmt.Sprintf(`You are a friendly NAS monitoring assistant. Based on the following system status, write a brief (1-2 sentences max) %s summary in %s. Be conversational and helpful. If everything is fine, be positive. If there are issues, mention them briefly. Do not use markdown formatting.

%s

Time: %s report`, timeOfDay, lang, context.String(), timeOfDay)

	// Call Gemini API
	summary := callGeminiAPI(prompt)
	return summary
}

// callGeminiAPI makes a request to the Gemini API
func callGeminiAPI(prompt string) string {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-3.0-flash:generateContent?key=%s", cfg.GeminiAPIKey)

	requestBody := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]string{
					{"text": prompt},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"temperature":     0.7,
			"maxOutputTokens": 100,
		},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		log.Printf("[Gemini] Error marshaling request: %v", err)
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Printf("[Gemini] Error creating request: %v", err)
		return ""
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[Gemini] Error calling API: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("[Gemini] API error (status %d): %s", resp.StatusCode, string(body))
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[Gemini] Error reading response: %v", err)
		return ""
	}

	// Parse response
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("[Gemini] Error parsing response: %v", err)
		return ""
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(result.Candidates[0].Content.Parts[0].Text)
	}

	return ""
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

	// Use config thresholds
	cpuCritical := cfg.Notifications.CPU.CriticalThreshold
	ramCritical := cfg.Notifications.RAM.CriticalThreshold
	ssdCritical := cfg.Notifications.DiskSSD.CriticalThreshold
	hddCritical := cfg.Notifications.DiskHDD.CriticalThreshold

	if criticalCount > 0 || s.CPU > cpuCritical || s.RAM > ramCritical || s.VolSSD.Used > ssdCritical || s.VolHDD.Used > hddCritical {
		return "⚠️", tr("health_critical"), true
	}

	cpuWarn := cfg.Notifications.CPU.WarningThreshold
	ramWarn := cfg.Notifications.RAM.WarningThreshold
	ssdWarn := cfg.Notifications.DiskSSD.WarningThreshold
	hddWarn := cfg.Notifications.DiskHDD.WarningThreshold
	ioWarn := cfg.Notifications.DiskIO.WarningThreshold

	if warningCount > 0 || s.CPU > cpuWarn*0.9 || s.RAM > ramWarn*0.95 || s.DiskUtil > ioWarn*0.95 || s.VolSSD.Used > ssdWarn || s.VolHDD.Used > hddWarn {
		return "👀", tr("health_warning"), true
	}
	return "✨", tr("health_ok"), false
}

// generateReport for manual requests (/report)
func generateReport(manual bool) string {
	if !manual {
		return generateDailyReport("> *Report NAS*", true)
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
		b.WriteString("\n*Events*\n")
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

	// Limit to 20 events
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
//  AUTONOMOUS MANAGER (automatic decisions)
// ═══════════════════════════════════════════════════════════════════

func autonomousManager(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(10 * time.Second)
	diskTicker := time.NewTicker(5 * time.Minute) // Record disk usage every 5 minutes
	defer ticker.Stop()
	defer diskTicker.Stop()

	for {
		select {
		case <-ticker.C:
			statsMutex.RLock()
			s := statsCache
			ready := statsReady
			statsMutex.RUnlock()

			if !ready {
				continue
			}

			// Check stress for enabled resources only
			if cfg.StressTracking.Enabled {
				if cfg.Notifications.DiskIO.Enabled {
					checkResourceStress(bot, "HDD", s.DiskUtil, cfg.Notifications.DiskIO.WarningThreshold)
				}
				if cfg.Notifications.CPU.Enabled {
					checkResourceStress(bot, "CPU", s.CPU, cfg.Notifications.CPU.WarningThreshold)
				}
				if cfg.Notifications.RAM.Enabled {
					checkResourceStress(bot, "RAM", s.RAM, cfg.Notifications.RAM.WarningThreshold)
				}
				if cfg.Notifications.Swap.Enabled {
					checkResourceStress(bot, "Swap", s.Swap, cfg.Notifications.Swap.WarningThreshold)
				}
				if cfg.Notifications.DiskSSD.Enabled {
					checkResourceStress(bot, "SSD", s.VolSSD.Used, cfg.Notifications.DiskSSD.WarningThreshold)
				}
			}

			// Check critical RAM for auto-restart
			if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
				if s.RAM >= cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold {
					handleCriticalRAM(bot, s)
				}
			}

			// Clean restart counter (every hour)
			cleanRestartCounter()

			// Docker watchdog
			if cfg.Docker.Watchdog.Enabled {
				checkDockerHealth(bot)
			}

			// Check for unexpected container stops
			checkContainerStates(bot)

			// Weekly prune
			if cfg.Docker.WeeklyPrune.Enabled {
				checkWeeklyPrune(bot)
			}

		case <-diskTicker.C:
			// Record disk usage for prediction
			recordDiskUsage()
		}
	}
}

func checkDockerHealth(bot *tgbotapi.BotAPI) {
	// Check if Docker service responds and has containers
	containers := getContainerList()

	isHealthy := len(containers) > 0

	if isHealthy {
		// All good, reset timer
		if !dockerFailureStart.IsZero() {
			dockerFailureStart = time.Time{}
			log.Println("[Docker] Recovered/populated.")
		}
		return
	}

	// Detected problem (error or 0 containers)
	if dockerFailureStart.IsZero() {
		dockerFailureStart = time.Now()
		log.Printf("[Docker] Warning: 0 containers or service down. Timer started (%dm).", cfg.Docker.Watchdog.TimeoutMinutes)
		return
	}

	timeout := time.Duration(cfg.Docker.Watchdog.TimeoutMinutes) * time.Minute

	// If in failure state for > configured timeout
	if time.Since(dockerFailureStart) > timeout {
		log.Printf("[Docker] ⚠️ Down > %dm.", cfg.Docker.Watchdog.TimeoutMinutes)

		// Reset timer to avoid immediate loop
		dockerFailureStart = time.Now()

		// Only restart if configured to do so
		if !cfg.Docker.Watchdog.AutoRestartService {
			if !isQuietHours() {
				bot.Send(tgbotapi.NewMessage(AllowedUserID,
					fmt.Sprintf("⚠️ *Docker Watchdog*\n\nNo containers detected for %d minutes.\n_Auto-restart disabled in config_",
						cfg.Docker.Watchdog.TimeoutMinutes)))
			}
			addReportEvent("warning", "Docker watchdog triggered (restart disabled)")
			return
		}

		if !isQuietHours() {
			bot.Send(tgbotapi.NewMessage(AllowedUserID,
				fmt.Sprintf("⚠️ *Docker Watchdog*\n\nNo containers detected for %d minutes.\nRestarting Docker service...",
					cfg.Docker.Watchdog.TimeoutMinutes)))
		}

		addReportEvent("action", "Docker watchdog restart triggered")

		// Execute restart command
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		var cmd *exec.Cmd
		if _, err := exec.LookPath("systemctl"); err == nil {
			cmd = exec.CommandContext(ctx, "systemctl", "restart", "docker")
		} else {
			cmd = exec.CommandContext(ctx, "service", "docker", "restart")
		}

		out, err := cmd.CombinedOutput()
		if err != nil {
			if !isQuietHours() {
				bot.Send(tgbotapi.NewMessage(AllowedUserID, fmt.Sprintf("❌ Docker restart error:\n`%v`", err)))
			}
			log.Printf("[!] Docker restart fail: %v\n%s", err, string(out))
		} else {
			if !isQuietHours() {
				bot.Send(tgbotapi.NewMessage(AllowedUserID, "✅ Docker restart command sent."))
			}
		}
	}
}

func checkWeeklyPrune(bot *tgbotapi.BotAPI) {
	if !dockerPruneEnabled {
		return
	}

	now := time.Now().In(location)

	// Get target day from config
	targetDay := time.Sunday
	switch strings.ToLower(dockerPruneDay) {
	case "monday":
		targetDay = time.Monday
	case "tuesday":
		targetDay = time.Tuesday
	case "wednesday":
		targetDay = time.Wednesday
	case "thursday":
		targetDay = time.Thursday
	case "friday":
		targetDay = time.Friday
	case "saturday":
		targetDay = time.Saturday
	case "sunday":
		targetDay = time.Sunday
	}

	isTime := now.Weekday() == targetDay && now.Hour() == dockerPruneHour

	if isTime {
		if !pruneDoneToday {
			log.Println("[Docker] Running Weekly Prune...")
			pruneDoneToday = true

			go func() {
				cmd := exec.Command("docker", "system", "prune", "-a", "-f")
				out, err := cmd.CombinedOutput()

				var msg string
				if err != nil {
					msg = fmt.Sprintf("🧹 *Weekly Prune Error*\n\n`%v`", err)
				} else {
					output := string(out)
					lines := strings.Split(output, "\n")
					lastLine := ""
					for i := len(lines) - 1; i >= 0; i-- {
						if strings.TrimSpace(lines[i]) != "" {
							lastLine = lines[i]
							break
						}
					}
					msg = fmt.Sprintf("🧹 *Weekly Prune*\n\nUnused images removed.\n`%s`", lastLine)
					addReportEvent("info", "Weekly docker prune completed")
				}

				if !isQuietHours() {
					m := tgbotapi.NewMessage(AllowedUserID, msg)
					m.ParseMode = "Markdown"
					bot.Send(m)
				}
			}()
		}
	} else {
		// Reset flag when not in target hour anymore
		if now.Hour() != cfg.Docker.WeeklyPrune.Hour {
			pruneDoneToday = false
		}
	}
}

// checkResourceStress tracks stress for a resource and notifies if necessary
func checkResourceStress(bot *tgbotapi.BotAPI, resource string, currentValue, threshold float64) {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	tracker := resourceStress[resource]
	if tracker == nil {
		tracker = &StressTracker{}
		resourceStress[resource] = tracker
	}

	isStressed := currentValue >= threshold
	stressDurationThreshold := time.Duration(cfg.StressTracking.DurationThresholdMinutes) * time.Minute

	if isStressed {
		// Start new stress period
		if tracker.CurrentStart.IsZero() {
			tracker.CurrentStart = time.Now()
			tracker.StressCount++
			tracker.Notified = false
		}

		// Notify if prolonged stress and not already notified and not in quiet hours
		stressDuration := time.Since(tracker.CurrentStart)
		if stressDuration >= stressDurationThreshold && !tracker.Notified && !isQuietHours() {
			var emoji, unit string
			switch resource {
			case "HDD":
				emoji = "💾"
				unit = "I/O"
			case "SSD":
				emoji = "💿"
				unit = "Usage"
			case "CPU":
				emoji = "🧠"
				unit = "Usage"
			case "RAM":
				emoji = "💾"
				unit = "Usage"
			case "Swap":
				emoji = "🔄"
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
		// End stress
		if !tracker.CurrentStart.IsZero() {
			stressDuration := time.Since(tracker.CurrentStart)
			tracker.TotalStress += stressDuration

			// Update max duration
			if stressDuration > tracker.LongestStress {
				tracker.LongestStress = stressDuration
			}

			// Notify stress end if it was notified and not in quiet hours
			if tracker.Notified && !isQuietHours() {
				msg := fmt.Sprintf("✅ *%s back to normal* after `%s`", resource, stressDuration.Round(time.Second))
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

// resetStressCounters resets stress counters for new report period
func resetStressCounters() {
	resourceStressMutex.Lock()
	defer resourceStressMutex.Unlock()

	for _, tracker := range resourceStress {
		tracker.StressCount = 0
		tracker.LongestStress = 0
		tracker.TotalStress = 0
	}
}

// formatDuration formats a duration readably
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
	// Find containers with high I/O (potential causes)
	containers := getContainerList()
	for _, c := range containers {
		if !c.Running {
			continue
		}

		// Check if container uses lots of resources
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.BlockIO}}", c.Name)
		out, _ := cmd.Output()
		cancel()

		blockIO := strings.TrimSpace(string(out))
		// If container has high I/O, could be restart candidate
		if strings.Contains(blockIO, "GB") {
			log.Printf("🔍 Container %s high BlockIO: %s", c.Name, blockIO)
		}
	}
}

func handleCriticalRAM(bot *tgbotapi.BotAPI, s Stats) {
	// Critical RAM - find heavy processes/containers
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

	// If RAM exceeds threshold and we have heavy containers, consider restart
	ramThreshold := cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold
	if s.RAM >= ramThreshold && len(heavyContainers) > 0 {
		// Sort by consumption
		sort.Slice(heavyContainers, func(i, j int) bool {
			return heavyContainers[i].memPct > heavyContainers[j].memPct
		})

		// Try to restart heaviest container (if not done recently)
		target := heavyContainers[0]
		if canAutoRestart(target.name) {
			log.Printf("> RAM critical (%.1f%%), auto-restart: %s (%.1f%%)", s.RAM, target.name, target.memPct)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "docker", "restart", target.name)
			err := cmd.Run()
			cancel()

			recordAutoRestart(target.name)

			var msgText string
			if err != nil {
				msgText = fmt.Sprintf("❌ *Auto-restart failed*\n\n"+
					"RAM critical: `%.1f%%`\n"+
					"Container: `%s`\n"+
					"Error: %v", s.RAM, target.name, err)
				addReportEvent("critical", fmt.Sprintf("Auto-restart failed: %s (%v)", target.name, err))
			} else {
				msgText = fmt.Sprintf("🔄 *Auto-restart done*\n\n"+
					"RAM was critical: `%.1f%%`\n"+
					"Restarted: `%s` (`%.1f%%` mem)\n\n"+
					"_Watching..._", s.RAM, target.name, target.memPct)
				addReportEvent("action", fmt.Sprintf("Auto-restart: %s (RAM %.1f%%)", target.name, s.RAM))
			}

			if !isQuietHours() {
				msg := tgbotapi.NewMessage(AllowedUserID, msgText)
				msg.ParseMode = "Markdown"
				bot.Send(msg)
			}
		}
	}
}

func canAutoRestart(containerName string) bool {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	restarts := autoRestarts[containerName]
	cutoff := time.Now().Add(-1 * time.Hour)

	// Count restarts in last hour
	count := 0
	for _, t := range restarts {
		if t.After(cutoff) {
			count++
		}
	}

	maxRestarts := cfg.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour
	if maxRestarts <= 0 {
		maxRestarts = 3
	}

	return count < maxRestarts
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
//  BACKGROUND STATS COLLECTOR (optimized for slow NAS)
// ═══════════════════════════════════════════════════════════════════

func statsCollector() {
	var lastIO map[string]disk.IOCountersStat
	var lastIOTime time.Time

	ticker := time.NewTicker(IntervalStats)
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
//  MONITOR ALERTS (only critical, no spam)
// ═══════════════════════════════════════════════════════════════════

var lastCriticalAlert time.Time

func monitorAlerts(bot *tgbotapi.BotAPI) {
	ticker := time.NewTicker(IntervalMonitor)
	defer ticker.Stop()

	for range ticker.C {
		statsMutex.RLock()
		s := statsCache
		ready := statsReady
		statsMutex.RUnlock()

		if !ready {
			continue
		}

		// Only immediate CRITICAL alerts (disk full, SMART failure)
		var criticalAlerts []string

		// Disk almost full (using config thresholds)
		if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("💿 SSD critical: `%.1f%%`", s.VolSSD.Used))
		}
		if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🗄 HDD critical: `%.1f%%`", s.VolHDD.Used))
		}

		// Check SMART
		if cfg.Notifications.SMART.Enabled {
			for _, dev := range []string{"sda", "sdb"} {
				_, health := readDiskSMART(dev)
				if strings.Contains(strings.ToUpper(health), "FAIL") {
					criticalAlerts = append(criticalAlerts, fmt.Sprintf("🚨 Disk %s FAILING — backup now!", dev))
				}
			}
		}

		// Critical CPU/RAM
		if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("🧠 CPU critical: `%.1f%%`", s.CPU))
		}
		if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.CriticalThreshold {
			criticalAlerts = append(criticalAlerts, fmt.Sprintf("💾 RAM critical: `%.1f%%`", s.RAM))
		}

		// Send critical alerts with configurable cooldown
		cooldown := time.Duration(cfg.Intervals.CriticalAlertCooldownMins) * time.Minute
		if len(criticalAlerts) > 0 && time.Since(lastCriticalAlert) >= cooldown && !isQuietHours() {
			msg := "🚨 *Critical*\n\n" + strings.Join(criticalAlerts, "\n")
			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
			lastCriticalAlert = time.Now()
		}

		// Always record critical events for report
		if len(criticalAlerts) > 0 {
			for _, alert := range criticalAlerts {
				addReportEvent("critical", alert)
			}
		}

		// Record warnings for the report (only if notifications enabled for that resource)
		if cfg.Notifications.CPU.Enabled && s.CPU >= cfg.Notifications.CPU.WarningThreshold && s.CPU < cfg.Notifications.CPU.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("CPU high: %.1f%%", s.CPU))
		}
		if cfg.Notifications.RAM.Enabled && s.RAM >= cfg.Notifications.RAM.WarningThreshold && s.RAM < cfg.Notifications.RAM.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("RAM high: %.1f%%", s.RAM))
		}
		if cfg.Notifications.Swap.Enabled && s.Swap >= cfg.Notifications.Swap.WarningThreshold {
			addReportEvent("warning", fmt.Sprintf("Swap high: %.1f%%", s.Swap))
		}
		if cfg.Notifications.DiskSSD.Enabled && s.VolSSD.Used >= cfg.Notifications.DiskSSD.WarningThreshold && s.VolSSD.Used < cfg.Notifications.DiskSSD.CriticalThreshold {
			addReportEvent("warning", fmt.Sprintf("SSD at %.1f%%", s.VolSSD.Used))
		}
		if cfg.Notifications.DiskHDD.Enabled && s.VolHDD.Used >= cfg.Notifications.DiskHDD.WarningThreshold && s.VolHDD.Used < cfg.Notifications.DiskHDD.CriticalThreshold {
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
			tgbotapi.NewInlineKeyboardButtonData("📊 D-Stats", "show_dstats"),
			tgbotapi.NewInlineKeyboardButtonData("🔥 Top Proc", "show_top"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚡ Power Actions", "show_power"),
		),
	)
}
