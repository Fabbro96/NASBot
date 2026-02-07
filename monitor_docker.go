package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func checkDockerHealth(ctx *AppContext, bot BotAPI) {
	containers, err := getContainerListWithError()

	// If Docker CLI was killed, it's a strong sign of OOM. Check immediately.
	if err != nil && (strings.Contains(err.Error(), "killed") || strings.Contains(err.Error(), "signal: 9")) {
		slog.Warn("Docker CLI killed, triggering kernel event check...")
		checkKernelEvents(ctx, bot)
	}

	isHealthy := len(containers) > 0

	ctx.State.mu.Lock()
	failureStart := ctx.State.DockerFailure
	ctx.State.mu.Unlock()

	if isHealthy {
		if !failureStart.IsZero() {
			ctx.State.mu.Lock()
			ctx.State.DockerFailure = time.Time{}
			ctx.State.mu.Unlock()
			slog.Info("Docker recovered/populated.")
		}
		return
	}

	cfg := ctx.Config

	if failureStart.IsZero() {
		ctx.State.mu.Lock()
		ctx.State.DockerFailure = time.Now()
		ctx.State.mu.Unlock()
		slog.Warn("Docker: 0 containers or service down. Timer started.", "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)
		return
	}

	timeout := time.Duration(cfg.Docker.Watchdog.TimeoutMinutes) * time.Minute

	if time.Since(failureStart) > timeout {
		slog.Error("Docker down longer than timeout", "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)

		ctx.State.mu.Lock()
		ctx.State.DockerFailure = time.Now()
		ctx.State.mu.Unlock()

		if !cfg.Docker.Watchdog.AutoRestartService {
			if !ctx.IsQuietHours() {
				title := ctx.Tr("wd_title")
				body := fmt.Sprintf(ctx.Tr("wd_no_containers"), cfg.Docker.Watchdog.TimeoutMinutes)
				footer := ctx.Tr("wd_disabled")
				safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, title+body+footer))
			}
			ctx.State.AddEvent("warning", "Docker watchdog triggered (restart disabled)")
			return
		}

		if !ctx.IsQuietHours() {
			title := ctx.Tr("wd_title")
			body := fmt.Sprintf(ctx.Tr("wd_no_containers"), cfg.Docker.Watchdog.TimeoutMinutes)
			footer := ctx.Tr("wd_restarting")
			safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, title+body+footer))
		}

		ctx.State.AddEvent("action", "Docker watchdog restart triggered")

		c, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		defer cancel()

		var out []byte
		var err error
		if commandExists("systemctl") {
			out, err = runCommandOutput(c, "systemctl", "restart", "docker")
		} else {
			out, err = runCommandOutput(c, "service", "docker", "restart")
		}
		if err != nil {
			if !ctx.IsQuietHours() {
				safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, fmt.Sprintf(ctx.Tr("docker_restart_err"), err)))
			}
			slog.Error("Docker restart fail", "err", err, "output", string(out))
		} else {
			if !ctx.IsQuietHours() {
				safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, ctx.Tr("docker_restart_sent")))
			}
		}
	}
}

func checkWeeklyPrune(ctx *AppContext, bot BotAPI) {
	// Use ctx.Settings.DockerPrune from settings if available, or config fallback
	// For now using user settings as per app_context
	ctx.Settings.mu.RLock()
	enabled := ctx.Settings.DockerPrune.Enabled
	day := ctx.Settings.DockerPrune.Day
	hour := ctx.Settings.DockerPrune.Hour
	ctx.Settings.mu.RUnlock()

	if !enabled {
		return
	}

	now := time.Now().In(ctx.State.TimeLocation)

	targetDay := time.Sunday
	switch strings.ToLower(day) {
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

	isTime := now.Weekday() == targetDay && now.Hour() == hour

	ctx.Docker.mu.Lock()
	pruneDone := ctx.Docker.PruneDoneToday
	ctx.Docker.mu.Unlock()

	if isTime {
		if !pruneDone {
			slog.Info("Docker: Running Weekly Prune...")

			ctx.Docker.mu.Lock()
			ctx.Docker.PruneDoneToday = true
			ctx.Docker.mu.Unlock()

			go func() {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()

				out, err := runCommandOutput(c, "docker", "system", "prune", "-a", "-f")

				var msg string
				if err != nil {
					if c.Err() == context.DeadlineExceeded {
						msg = "🧹 *Weekly Prune Error*\n\n`timeout after 5m`"
					} else {
						msg = fmt.Sprintf("🧹 *Weekly Prune Error*\n\n`%v`", err)
					}
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
					ctx.State.AddEvent("info", "Weekly docker prune completed")
				}

				if !ctx.IsQuietHours() {
					m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
					m.ParseMode = "Markdown"
					safeSend(bot, m)
				}
			}()
		}
	} else {
		// Reset flag if hour passed
		if now.Hour() != hour {
			ctx.Docker.mu.Lock()
			ctx.Docker.PruneDoneToday = false
			ctx.Docker.mu.Unlock()
		}
	}
}
