package app

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// dockerHeavyMemPercent is the fallback per-container memory share above which a
// container becomes a candidate for auto-restart once system RAM is critical.
//
// It is only the fallback now: DockerAutoRestartConfig.HeavyContainerMemPercent
// carries the policy and is what the settings screen and the auto-restart alerts
// print. The constant stays for the two cases where there is no configuration to
// read: a value left at 0 by an old config file, and a Config built by hand (the
// tests, and the watchdog binary before its first SIGHUP).
const dockerHeavyMemPercent = 20.0

// dockerHeavyContainerThreshold returns the per-container memory share above
// which a container is considered heavy, read from the published configuration
// and falling back to dockerHeavyMemPercent when it is unset.
func dockerHeavyContainerThreshold(ctx *AppContext) float64 {
	if ctx != nil {
		if snapshot := ctx.Cfg(); snapshot != nil {
			if pct := snapshot.Docker.AutoRestartOnRAMCritical.HeavyContainerMemPercent; pct > 0 {
				return pct
			}
		}
	}
	return dockerHeavyMemPercent
}

// containerTransition is one observed container state change, carrying the raw
// facts needed to render the alert.
//
// The diff that produces these runs under Docker.Mu, the rendering does not:
// ctx.Tr takes the settings lock and safeSend performs a Telegram HTTP call, so
// neither may happen while the Docker lock is held.
type containerTransition struct {
	name      string
	up        bool
	downtime  time.Duration
	recovered bool // a downtime window was being tracked for this container
}

// checkContainerStates monitors for container state changes (down/up)
func checkContainerStates(ctx *AppContext, bot BotAPI) {
	containers := getCachedContainerList(ctx)
	if containers == nil {
		return
	}

	// Only the map diff runs under Docker.Mu. It used to be the whole function,
	// with two Telegram sends inside the critical section: a slow or stalled Bot
	// API then held the Docker *write* lock across the network call, blocking the
	// 10s autonomousManager tick, getCachedContainerList (RLock, so every Docker
	// menu and the periodic report) and canAutoRestart.
	changes := diffContainerStates(ctx, containers)
	if len(changes) == 0 {
		return
	}

	quiet := ctx.IsQuietHours()
	for _, ch := range changes {
		var text, event, level string
		if ch.up {
			level = "info"
			// downtimeText is appended to the "up" alert, so it is empty when no
			// downtime window was being tracked.
			downtimeText := ""
			if ch.recovered {
				downtimeText = fmt.Sprintf(ctx.Tr("container_downtime_fmt"),
					format.FormatDuration(ch.downtime))
				event = fmt.Sprintf("🟢 Container recovered: %s (down for %s)",
					ch.name, format.FormatDuration(ch.downtime))
			} else {
				event = fmt.Sprintf("🟢 Container started: %s", ch.name)
			}
			text = fmt.Sprintf(ctx.Tr("container_up_alert"), ch.name, downtimeText)
		} else {
			level = "warning"
			text = fmt.Sprintf(ctx.Tr("container_down_alert"), ch.name)
			event = fmt.Sprintf("🔴 Container stopped: %s", ch.name)
		}

		ctx.State.AddEvent(level, event)

		if quiet || text == "" {
			continue
		}
		m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, text)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
	}
}

// diffContainerStates compares the current container list against the previous
// snapshot, records the transition times and returns the transitions to report.
//
// It is the only part of checkContainerStates that touches Docker.Mu, and it
// performs no I/O and no translation while holding it. The caller must not hold
// Docker.Mu.
func diffContainerStates(ctx *AppContext, containers []ContainerInfo) []containerTransition {
	currentStates := make(map[string]bool, len(containers))
	for _, c := range containers {
		currentStates[c.Name] = c.Running
	}

	var changes []containerTransition

	ctx.Docker.Mu.Lock()
	defer ctx.Docker.Mu.Unlock()

	if ctx.Docker.LastStates == nil {
		ctx.Docker.LastStates = make(map[string]bool, len(currentStates))
	}
	if ctx.Docker.ContainerDowntime == nil {
		ctx.Docker.ContainerDowntime = make(map[string]time.Time)
	}

	for name, wasRunning := range ctx.Docker.LastStates {
		isRunning, exists := currentStates[name]
		// A container missing from `docker ps -a` was removed, not stopped:
		// there is no down transition to report and no downtime to time.
		if exists && wasRunning && !isRunning {
			ctx.Docker.ContainerDowntime[name] = time.Now()
			changes = append(changes, containerTransition{name: name})
		}
	}

	for name, isRunning := range currentStates {
		wasRunning, wasTracked := ctx.Docker.LastStates[name]
		// First sighting of a running container is not a recovery.
		if !wasTracked || wasRunning || !isRunning {
			continue
		}
		downStart, hadDowntime := ctx.Docker.ContainerDowntime[name]
		downtime := time.Since(downStart)
		if hadDowntime {
			delete(ctx.Docker.ContainerDowntime, name)
		}
		changes = append(changes, containerTransition{
			name:      name,
			up:        true,
			downtime:  downtime,
			recovered: hadDowntime,
		})
	}

	// Removed containers must not keep a downtime entry alive forever.
	for name := range ctx.Docker.ContainerDowntime {
		if _, exists := currentStates[name]; !exists {
			delete(ctx.Docker.ContainerDowntime, name)
		}
	}

	ctx.Docker.LastStates = currentStates
	return changes
}

// handleCriticalRAM handles critical RAM situations.
//
// Two gates guard the restart, and the caller applies the outer one: the system
// RAM threshold, and the per-container share above dockerHeavyContainerThreshold.
// Keeping the RAM check here as well costs nothing and keeps the function
// correct if it is ever called from a new site.
func handleCriticalRAM(ctx *AppContext, bot BotAPI, s Stats) {
	type containerMem struct {
		name   string
		memPct float64
	}

	threshold := dockerHeavyContainerThreshold(ctx)
	ramThreshold := ctx.Cfg().Docker.AutoRestartOnRAMCritical.RAMThreshold
	if s.RAM < ramThreshold {
		return
	}

	// Single batch call instead of N sequential calls (one per container).
	// This reduces O(N × 2s) to O(1 × 5s) for the docker stats query.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	out, err := runCommandStdout(timeoutCtx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.MemPerc}}")
	cancel()

	if err != nil {
		slog.Warn("handleCriticalRAM: docker stats failed", "err", err)
		return
	}

	var heavyContainers []containerMem
	for _, line := range splitNonEmptyLines(string(out)) {
		// A row Docker could not fill in ("--", "N/A", a missing field) is not a
		// 0% reading and not a heavy one: it is skipped, never guessed at.
		name, memPct, ok := parseDockerMemPercent(line)
		if !ok || memPct <= threshold {
			continue
		}
		heavyContainers = append(heavyContainers, containerMem{name, memPct})
	}

	if len(heavyContainers) == 0 {
		return
	}

	sort.Slice(heavyContainers, func(i, j int) bool {
		return heavyContainers[i].memPct > heavyContainers[j].memPct
	})

	var restarted bool
	for _, target := range heavyContainers {
		if !canAutoRestart(ctx, target.name) {
			continue
		}

		slog.Warn("RAM critical, auto-restart", "ram", s.RAM, "container", target.name,
			"mem_pct", target.memPct, "container_threshold", threshold)

		restartCtx, cancelRestart := context.WithTimeout(context.Background(), 30*time.Second)
		err := runCommand(restartCtx, "docker", "restart", target.name)
		cancelRestart()

		recordAutoRestart(ctx, target.name)

		var msgText string
		if err != nil {
			// The threshold is appended rather than passed as an argument: the
			// two alert templates are already translated in six languages and
			// adding a verb to them is the i18n lane's job, not this one.
			msgText = fmt.Sprintf(ctx.Tr("docker_autorestart_fail"), s.RAM, target.name, sanitizeErr(err)) +
				fmt.Sprintf("\n_Container threshold: >%.0f%% of its own memory_", threshold)
			ctx.State.AddEvent("critical", fmt.Sprintf("Auto-restart failed: %s (%v, container threshold >%.0f%%)", target.name, err, threshold))
		} else {
			msgText = fmt.Sprintf(ctx.Tr("docker_autorestart_done"), s.RAM, target.name, target.memPct) +
				fmt.Sprintf("\n_Container threshold: >%.0f%% of its own memory_", threshold)
			ctx.State.AddEvent("action", fmt.Sprintf("Auto-restart: %s (RAM %.1f%%, container threshold >%.0f%%)", target.name, s.RAM, threshold))
		}

		if !ctx.IsQuietHours() {
			msg := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msgText)
			msg.ParseMode = "Markdown"
			safeSend(bot, msg)
		}
		restarted = true
		break // Only restart one container per tick
	}

	if !restarted {
		slog.Warn("RAM critical, but all heavy containers are throttled from auto-restarting",
			"heavy", len(heavyContainers), "container_threshold", threshold)
	}
}

// canAutoRestart checks if container can be auto-restarted.
//
// Read-only: it takes the Docker *read* lock. It used to take the write lock
// (and to lazily create the map) even though it only counts timestamps, which
// made every heavy container of every tick contend with the readers of the
// container cache.
func canAutoRestart(ctx *AppContext, containerName string) bool {
	ctx.Docker.Mu.RLock()
	defer ctx.Docker.Mu.RUnlock()

	cutoff := time.Now().Add(-1 * time.Hour)

	count := 0
	for _, t := range ctx.Docker.AutoRestarts[containerName] {
		if t.After(cutoff) {
			count++
		}
	}

	maxRestarts := ctx.Cfg().Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour
	if maxRestarts <= 0 {
		maxRestarts = 3
	}

	return count < maxRestarts
}

// recordAutoRestart records an auto-restart
func recordAutoRestart(ctx *AppContext, containerName string) {
	ctx.Docker.Mu.Lock()
	if ctx.Docker.AutoRestarts == nil {
		ctx.Docker.AutoRestarts = make(map[string][]time.Time)
	}
	ctx.Docker.AutoRestarts[containerName] = append(ctx.Docker.AutoRestarts[containerName], time.Now())
	ctx.Docker.Mu.Unlock()

	// saveState acquires Docker.Mu.RLock internally — must be called outside the lock
	saveState(ctx)
}

// cleanRestartCounter cleans old restart records
func cleanRestartCounter(ctx *AppContext) {
	ctx.Docker.Mu.Lock()
	defer ctx.Docker.Mu.Unlock()

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
