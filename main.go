package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

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
	containerDowntimeStart map[string]time.Time // When container went down
	containerStateMutex sync.Mutex

	// Disk usage history for prediction
	diskUsageHistory      []DiskUsagePoint
	diskUsageHistoryMutex sync.Mutex

	// Trend history (last 6 hours, sampled every 5 min = 72 points max)
	cpuTrend       []TrendPoint
	ramTrend       []TrendPoint
	trendMutex     sync.Mutex
	maxTrendPoints = 72

	// Docker container cache with TTL
	dockerCache      DockerCache
	dockerCacheMutex sync.RWMutex

	// Shared HTTP client (reuse connections)
	httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        5,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Temperature alert tracking
	lastTempAlert time.Time

	// Healthchecks.io tracking
	healthchecksState      HealthchecksState
	healthchecksMutex      sync.Mutex
	healthchecksInDowntime bool // True if we are tracking a failure period

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
	containerDowntimeStart = make(map[string]time.Time)
	diskUsageHistory = make([]DiskUsagePoint, 0, 288)
	cpuTrend = make([]TrendPoint, 0, maxTrendPoints)
	ramTrend = make([]TrendPoint, 0, maxTrendPoints)
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		resourceStress[res] = &StressTracker{}
	}

	// Load persistent state
	loadState()
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
	go startHealthchecksPinger(bot)
	go RunFSWatchdog(bot) // Filesystem watchdog (lazy evaluation)

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
