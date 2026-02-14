package main

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// checkContainerStates monitors for container state changes (down/up)
func checkContainerStates(ctx *AppContext, bot BotAPI) {
	containers := getContainerList()
	if containers == nil {
		return
	}

	ctx.Docker.mu.Lock()
	defer ctx.Docker.mu.Unlock()

	if ctx.Docker.LastStates == nil {
		ctx.Docker.LastStates = make(map[string]bool)
	}
	if ctx.Docker.ContainerDowntime == nil {
		ctx.Docker.ContainerDowntime = make(map[string]time.Time)
	}

	currentStates := make(map[string]bool)
	for _, c := range containers {
		currentStates[c.Name] = c.Running
	}

	for name, wasRunning := range ctx.Docker.LastStates {
		isRunning, exists := currentStates[name]
		if exists && wasRunning && !isRunning {
			ctx.Docker.ContainerDowntime[name] = time.Now()

			if !ctx.IsQuietHours() {
				msg := fmt.Sprintf("🔴 *Container DOWN*\n\n📦 `%s`\n\n_The container has stopped unexpectedly._", name)
				m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
			}
			ctx.State.AddReportEvent("warning", fmt.Sprintf("🔴 Container stopped: %s", name))
		}
	}

	for name, isRunning := range currentStates {
		wasRunning, wasTracked := ctx.Docker.LastStates[name]
		if wasTracked && !wasRunning && isRunning {
			var downtimeMsg string
			if downStart, hasDowntime := ctx.Docker.ContainerDowntime[name]; hasDowntime {
				duration := time.Since(downStart)
				downtimeMsg = fmt.Sprintf("\n⏱ Downtime: `%s`", format.FormatDuration(duration))
				delete(ctx.Docker.ContainerDowntime, name)
				ctx.State.AddReportEvent("info", fmt.Sprintf("🟢 Container recovered: %s (down for %s)", name, format.FormatDuration(duration)))
			} else {
				ctx.State.AddReportEvent("info", fmt.Sprintf("🟢 Container started: %s", name))
			}

			if !ctx.IsQuietHours() {
				msg := fmt.Sprintf("🟢 *Container UP*\n\n📦 `%s`\n\n_The container is now running._%s", name, downtimeMsg)
				m := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msg)
				m.ParseMode = "Markdown"
				safeSend(bot, m)
			}
		}
	}

	ctx.Docker.LastStates = currentStates
}

// handleCriticalRAM handles critical RAM situations
func handleCriticalRAM(ctx *AppContext, bot BotAPI, s Stats) {
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
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, _ := runCommandStdout(timeoutCtx, "docker", "stats", "--no-stream", "--format", "{{.MemPerc}}", c.Name)
		cancel()

		memStr := strings.TrimSuffix(strings.TrimSpace(string(out)), "%")
		if memPct, err := strconv.ParseFloat(memStr, 64); err == nil && memPct > 20 {
			heavyContainers = append(heavyContainers, containerMem{c.Name, memPct})
		}
	}

	ramThreshold := ctx.Config.Docker.AutoRestartOnRAMCritical.RAMThreshold
	if s.RAM >= ramThreshold && len(heavyContainers) > 0 {
		sort.Slice(heavyContainers, func(i, j int) bool {
			return heavyContainers[i].memPct > heavyContainers[j].memPct
		})

		target := heavyContainers[0]
		if canAutoRestart(ctx, target.name) {
			slog.Warn("RAM critical, auto-restart", "ram", s.RAM, "container", target.name, "mem_pct", target.memPct)

			timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := runCommand(timeoutCtx, "docker", "restart", target.name)
			cancel()

			recordAutoRestart(ctx, target.name)

			var msgText string
			if err != nil {
				msgText = fmt.Sprintf("❌ *Auto-restart failed*\n\nRAM critical: `%.1f%%`\nContainer: `%s`\nError: %v", s.RAM, target.name, err)
				ctx.State.AddReportEvent("critical", fmt.Sprintf("Auto-restart failed: %s (%v)", target.name, err))
			} else {
				msgText = fmt.Sprintf("🔄 *Auto-restart done*\n\nRAM was critical: `%.1f%%`\nRestarted: `%s` (`%.1f%%` mem)\n\n_Watching..._", s.RAM, target.name, target.memPct)
				ctx.State.AddReportEvent("action", fmt.Sprintf("Auto-restart: %s (RAM %.1f%%)", target.name, s.RAM))
			}

			if !ctx.IsQuietHours() {
				msg := tgbotapi.NewMessage(ctx.Config.AllowedUserID, msgText)
				msg.ParseMode = "Markdown"
				safeSend(bot, msg)
			}
		}
	}
}

// canAutoRestart checks if container can be auto-restarted
func canAutoRestart(ctx *AppContext, containerName string) bool {
	ctx.Docker.mu.Lock()
	defer ctx.Docker.mu.Unlock()

	if ctx.Docker.AutoRestarts == nil {
		ctx.Docker.AutoRestarts = make(map[string][]time.Time)
	}

	restarts := ctx.Docker.AutoRestarts[containerName]
	cutoff := time.Now().Add(-1 * time.Hour)

	count := 0
	for _, t := range restarts {
		if t.After(cutoff) {
			count++
		}
	}

	maxRestarts := ctx.Config.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour
	if maxRestarts <= 0 {
		maxRestarts = 3
	}

	return count < maxRestarts
}

// recordAutoRestart records an auto-restart
func recordAutoRestart(ctx *AppContext, containerName string) {
	ctx.Docker.mu.Lock()
	defer ctx.Docker.mu.Unlock()

	if ctx.Docker.AutoRestarts == nil {
		ctx.Docker.AutoRestarts = make(map[string][]time.Time)
	}

	ctx.Docker.AutoRestarts[containerName] = append(ctx.Docker.AutoRestarts[containerName], time.Now())
	saveState(ctx)
}

// cleanRestartCounter cleans old restart records
func cleanRestartCounter(ctx *AppContext) {
	ctx.Docker.mu.Lock()
	defer ctx.Docker.mu.Unlock()

	if ctx.Docker.AutoRestarts == nil {
		return
	}

	cutoff := time.Now().Add(-2 * time.Hour)
	for name, times := range ctx.Docker.AutoRestarts {
		var newTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				newTimes = append(newTimes, t)
			}
		}
		if len(newTimes) == 0 {
			delete(ctx.Docker.AutoRestarts, name)
		} else {
			ctx.Docker.AutoRestarts[name] = newTimes
		}
	}
}
