//go:build fswatchdog
// +build fswatchdog

package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Standalone entrypoint for the filesystem watchdog.
// Build with: go build -tags fswatchdog -o nasbot-fswatchdog
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
	log.Printf("[+] NASBot FS Watchdog @%s", bot.Self.UserName)

	w := GetFSWatchdog()
	if !w.config.Enabled {
		log.Println("[FSWatchdog] Disabled in config; exiting")
		return
	}

	startup := fmt.Sprintf(`*NASBot FS Watchdog is online* 👀

Monitoring filesystem usage only.
Check interval: %d min
Warning: %.0f%% · Critical: %.0f%%`,
		w.config.CheckIntervalMins,
		w.config.WarningThreshold,
		w.config.CriticalThreshold)

	msg := tgbotapi.NewMessage(AllowedUserID, startup)
	msg.ParseMode = "Markdown"
	bot.Send(msg)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("[-] Shutdown")
		saveState()
		os.Exit(0)
	}()

	go RunFSWatchdog(bot)

	select {}
}
