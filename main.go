package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Global app context (required for handlers.go compatibility)
var app *AppContext

func init() {
	setupLogger()
	loadConfig() // Populates global 'cfg'
}

func main() {
	slog.Info("NASBot boot sequence started", "pid", os.Getpid())

	defer func() {
		if r := recover(); r != nil {
			slog.Error("PANIC recovered", "err", r, "stack", string(debug.Stack()))
			if app != nil {
				saveState(app)
			}
			closeLogger()
		}
	}()

	acquirePIDLock()
	defer releasePIDLock()

	// Initialize AppContext with global cfg
	app = InitApp(&cfg)

	// Set Timezone
	tz := app.Config.Timezone
	if tz == "" {
		tz = "Europe/Rome"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		slog.Warn("Timezone not found, using UTC", "tz", tz)
		loc = time.UTC
	}
	app.State.TimeLocation = loc

	// Load persistent state
	loadState(app)

	// Start Bot
	bot, err := tgbotapi.NewBotAPI(app.Config.BotToken)
	if err != nil {
		slog.Error("Failed to start bot", "err", err)
		closeLogger()
		os.Exit(1)
	}
	slog.Info("NASBot started", "user", bot.Self.UserName)

	// Clear webhook
	if _, err := bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true}); err != nil {
		slog.Error("Delete webhook failed", "err", err)
	}

	// Startup Notification
	sendStartupNotification(app, bot)

	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal Handling
	sigChan := make(chan os.Signal, 1)
	shutdownDone := make(chan struct{})
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	goSafe("signal-handler", func() {
		<-sigChan
		slog.Info("Shutdown signal received")
		cancel()
		bot.StopReceivingUpdates()
		close(shutdownDone)
	})

	// Start Background Tasks
	goSafe("stats-collector", func() { statsCollector(app, rootCtx) })
	goSafe("monitor-alerts", func() { monitorAlerts(app, bot, rootCtx) })
	goSafe("autonomous-manager", func() { autonomousManager(app, bot, rootCtx) })
	goSafe("periodic-report", func() { periodicReport(app, bot, rootCtx) })
	goSafe("healthchecks-pinger", func() { startHealthchecksPinger(app, bot, rootCtx) })

	// Send /start signal to healthchecks.io
	goSafe("healthchecks-start-ping", func() { pingHealthchecksStart(app) })

	// Wait for stats to be ready
	time.Sleep(2 * time.Second)

	// Update Loop
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		// Panic recovery for update loop
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic in update loop", "err", r)
				}
			}()

			if update.CallbackQuery != nil {
				goSafe("callback-handler", func() { handleCallback(bot, update.CallbackQuery) })
				return
			}

			if update.Message == nil {
				return
			}

			if update.Message.Chat.ID != int64(app.Config.AllowedUserID) {
				return
			}

			if update.Message.IsCommand() {
				// handlers.go handleCommand
				goSafe("command-handler", func() { handleCommand(bot, update.Message) })
			}
		}()
	}

	saveState(app)
	select {
	case <-shutdownDone:
		slog.Info("NASBot shutdown complete")
	default:
		slog.Warn("Updates channel closed unexpectedly")
	}
	closeLogger()
}

func goSafe(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic recovered in goroutine", "goroutine", name, "err", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

func sendStartupNotification(ctx *AppContext, bot BotAPI) {
	// Wait a bit for report time to be calculated if needed,
	// but getNextReportDescription typically calculates it on the fly.
	nextReportStr := getNextReportDescription(ctx)

	var quietInfo string
	ctx.Settings.mu.RLock()
	quietEnabled := ctx.Settings.QuietHours.Enabled
	qStartH := ctx.Settings.QuietHours.Start.Hour
	qStartM := ctx.Settings.QuietHours.Start.Minute
	qEndH := ctx.Settings.QuietHours.End.Hour
	qEndM := ctx.Settings.QuietHours.End.Minute
	ctx.Settings.mu.RUnlock()

	if quietEnabled {
		quietInfo = fmt.Sprintf(ctx.Tr("boot_quiet_fmt"), qStartH, qStartM, qEndH, qEndM)
	}

	crashInfo := checkPreviousBootCrash(ctx)
	startupText := fmt.Sprintf(ctx.Tr("boot_online"), nextReportStr, quietInfo) + crashInfo

	msg := tgbotapi.NewMessage(int64(ctx.Config.AllowedUserID), startupText)
	msg.ParseMode = "Markdown"
	safeSend(bot, msg)
}

// Global PID file variable
var pidFile *os.File

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
		_, _ = f.WriteString(fmt.Sprintf("%d", os.Getpid()))
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
