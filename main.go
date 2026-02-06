//go:build !fswatchdog
// +build !fswatchdog

package main

import (
"fmt"
"log/slog"
"net/http"
"os"
"os/signal"
"runtime/debug"
"strconv"
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
// Global cache with mutex
statsCache Stats
statsMutex sync.RWMutex
statsReady bool

// Stress tracking
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

// PID file lock
pidFile *os.File

// Container state tracking
lastContainerStates    map[string]bool
containerDowntimeStart map[string]time.Time
containerStateMutex    sync.Mutex

// Disk usage history
diskUsageHistory      []DiskUsagePoint
diskUsageHistoryMutex sync.Mutex

// Trend history
cpuTrend       []TrendPoint
ramTrend       []TrendPoint
trendMutex     sync.Mutex
maxTrendPoints = 72

// Docker container cache
dockerCache      DockerCache
dockerCacheMutex sync.RWMutex

// Shared HTTP client
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
	healthchecksInDowntime bool

	// User settings
	currentLanguage = "en"
	reportMode      = 2

	// Runtime config
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

func init() {
	setupLogger()

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
		slog.Warn("Timezone not found, using UTC", "tz", tz)
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

	loadState()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("PANIC recovered", "err", r, "stack", string(debug.Stack()))
			saveState()
		}
	}()

	acquirePIDLock()

	bot, err := tgbotapi.NewBotAPI(BotToken)
	if err != nil {
		slog.Error("Failed to start bot", "err", err)
		os.Exit(1)
	}
	slog.Info("NASBot started", "user", bot.Self.UserName)

	if _, err := bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true}); err != nil {
		slog.Error("Delete webhook failed", "err", err)
	}

	nextReportStr := getNextReportDescription()
	var quietInfo string
	if quietHoursEnabled {
		quietInfo = fmt.Sprintf(tr("boot_quiet_fmt"), quietStartHour, quietStartMinute, quietEndHour, quietEndMinute)
	}
	startupText := fmt.Sprintf(tr("boot_online"), nextReportStr, quietInfo)

	startupMsg := tgbotapi.NewMessage(AllowedUserID, startupText)
	startupMsg.ParseMode = "Markdown"
	bot.Send(startupMsg)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Shutdown signal received")
		saveState()
		releasePIDLock()
		os.Exit(0)
	}()

	go statsCollector()
	go monitorAlerts(bot)
	go periodicReport(bot)
	go autonomousManager(bot)
	go startHealthchecksPinger(bot)

	time.Sleep(IntervalStats + 500*time.Millisecond)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.CallbackQuery != nil {
			go handleCallback(bot, update.CallbackQuery)
			continue
		}
		if update.Message == nil || update.Message.Chat.ID != AllowedUserID {
			continue
		}
		if update.Message.IsCommand() {
			go handleCommand(bot, update.Message)
		}
	}
}

func acquirePIDLock() {
	const pidPath = "nasbot.pid"
	f, err := os.OpenFile(pidPath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		slog.Error("PID lock failed", "err", err)
		os.Exit(1)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		slog.Error("Another instance is already running", "pid_file", pidPath)
		os.Exit(1)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.Seek(0, 0)
		_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
	}
	pidFile = f
}

func releasePIDLock() {
	if pidFile == nil {
		return
	}
	_ = pidFile.Close()
	pidFile = nil
}
