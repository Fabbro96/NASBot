package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func checkRaidHealth(ctx *AppContext, bot BotAPI) {
	cfg := ctx.Config
	issues := getRaidIssues()

	if len(issues) == 0 {
		var shouldNotify bool
		var downSince time.Time
		ctx.Monitor.mu.Lock()
		if !ctx.Monitor.RaidDownSince.IsZero() {
			shouldNotify = cfg.RaidWatchdog.RecoveryNotify
			downSince = ctx.Monitor.RaidDownSince
			ctx.Monitor.RaidDownSince = time.Time{}
			ctx.Monitor.RaidLastSignature = ""
		}
		ctx.Monitor.mu.Unlock()

		if shouldNotify && !ctx.IsQuietHours() {
			msg := fmt.Sprintf(ctx.Tr("raid_recovered"), format.FormatDuration(time.Since(downSince)))
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		return
	}

	signature := strings.Join(issues, " | ")
	cooldown := time.Duration(cfg.RaidWatchdog.CooldownMins) * time.Minute
	if cooldown <= 0 {
		cooldown = 30 * time.Minute
	}

	shouldAlert := false
	ctx.Monitor.mu.Lock()
	if ctx.Monitor.RaidDownSince.IsZero() {
		ctx.Monitor.RaidDownSince = time.Now()
	}
	if signature != ctx.Monitor.RaidLastSignature || time.Since(ctx.Monitor.RaidAlertTime) >= cooldown {
		shouldAlert = true
		ctx.Monitor.RaidLastSignature = signature
		ctx.Monitor.RaidAlertTime = time.Now()
	}
	ctx.Monitor.mu.Unlock()

	if shouldAlert {
		msg := fmt.Sprintf(ctx.Tr("raid_alert"), strings.Join(issues, "\n"))
		m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
		ctx.State.AddEvent("critical", "RAID issue detected")
	}
}

func getRaidIssues() []string {
	var issues []string
	if data, err := os.ReadFile("/proc/mdstat"); err == nil {
		text := string(data)
		if strings.Contains(text, "md") {
			lines := strings.Split(text, "\n")
			for _, line := range lines {
				if strings.Contains(line, "md") && strings.Contains(line, "[") && strings.Contains(line, "]") {
					if strings.Contains(line, "_") {
						issues = append(issues, fmt.Sprintf("mdadm degraded: %s", strings.TrimSpace(line)))
					}
					if strings.Contains(line, "recovery") || strings.Contains(line, "resync") || strings.Contains(line, "reshape") {
						issues = append(issues, fmt.Sprintf("mdadm sync: %s", strings.TrimSpace(line)))
					}
				}
			}
		}
	}
	if commandExists("zpool") {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := runCommandStdout(c, "zpool", "status", "-x")
		if err == nil {
			output := strings.TrimSpace(string(out))
			if output != "" && !strings.Contains(strings.ToLower(output), "all pools are healthy") {
				issues = append(issues, fmt.Sprintf("zpool: %s", output))
			}
		}
	}
	return issues
}
