//go:build !fswatchdog
// +build !fswatchdog

package main

import (
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var app *AppContext

func init() {
	setupLogger()
	loadConfig()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("PANIC recovered", "err", r)
			if app != nil {
				saveState(app)
			}
		}
	}()

	acquirePIDLock()
	defer releasePIDLock()

	app = InitApp(&cfg)

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

	loadState(app)

	bot, err := tgbotapi.NewBotAPI(app.Config.BotToken)
	if err != nil {
		slog.Error("Failed to start bot", "err", err)
		os.Exit(1)
	}
	slog.Info("NASBot started", "user", bot.Self.UserName)

	if _, err := bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: true}); err != nil {
		slog.Error("Delete webhook failed", "err", err)
	}

	sendStartupNotification(app, bot)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("Shutdown signal received")
		saveState(app)
		releasePIDLock()
		os.Exit(0)
	}()

	go statsCollector(app)
	go monitorAlerts(app, bot)
	go autonomousManager(app, bot)
	go periodicReport(app, bot)
	go startHealthchecksPinger(app, bot)
	go pingHealthchecksStart(app)

	time.Sleep(2 * time.Second)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic in update loop", "err", r)
				}
			}()

			if update.CallbackQuery != nil {
				go handleCallback(bot, update.CallbackQuery)
				return
			}

			if update.Message == nil {
				return
			}

			if update.Message.Chat.ID != int64(app.Config.AllowedUserID) {
				return
			}

			if update.Message.IsCommand() {
				go handleCommand(bot, update.Message)
			}
		}()
	}
}

func sendStartupNotification(ctx *AppContext, bot *tgbotapi.BotAPI) {
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
	bot.Send(msg)
}

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
