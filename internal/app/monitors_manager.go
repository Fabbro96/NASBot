package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ═══════════════════════════════════════════════════════════════════
//  AUTONOMOUS MONITOR MANAGER
//
//  Every watchdog runs in its own goroutine with its own timer and its own
//  child context. The previous single select ran the checks inline on shared
//  tickers, so one slow probe froze all of them:
//    - checkNetworkHealth can take ~18s (2 ping x 2 attempts x 2s + 1s sleep,
//      plus 2 DNS lookups of 3s + 1s) on a 60s ticker;
//    - checkDiskMounts calls disk.Usage (statfs) on every partition with no
//      timeout, and a CIFS/NFS mount whose server is gone blocks for minutes;
//    - checkDockerHealth runs `docker ps` with a 5-15s timeout.
//
//  The interval is re-read from the config at every tick, so /config takes
//  effect without a restart (it used to be read once, before the loop, and the
//  tickers were never rebuilt).
// ═══════════════════════════════════════════════════════════════════

// watchdog is one autonomous check with its own cadence.
type watchdog struct {
	name string
	// interval re-reads the cadence from the current config on every tick, so
	// a /config change is picked up without a restart.
	interval func(cfg *Config) time.Duration
	// initialDelay overrides how long the loop waits before its first tick.
	// Only for lanes that want an early first check; the rest leave it nil and
	// wait a full interval, which is the behaviour they always had.
	initialDelay func(cfg *Config) time.Duration
	run          func(ctx *AppContext, bot BotAPI, runCtx context.Context)
}

// fastWatchdogInterval is the cadence of the three fast lanes. It is a
// deliberate constant, not Intervals.MonitorSeconds: that field already drives
// monitorAlerts (the CPU/RAM/Swap/SMART threshold checks, default 30s) and
// reusing it here would have halved the check frequency from the previous hard
// coded 10s.
const fastWatchdogInterval = 10 * time.Second

// resolveInterval applies a floor to a watchdog cadence read from the config.
func resolveInterval(seconds int, min, fallback time.Duration) time.Duration {
	d := time.Duration(seconds) * time.Second
	if d < min {
		return fallback
	}
	return d
}

func watchdogsFor(ctx *AppContext) []watchdog {
	return []watchdog{
		{
			// Fast lane: pure state plus one /sys sensor read. Never blocks.
			name:     "stress",
			interval: func(_ *Config) time.Duration { return fastWatchdogInterval },
			run:      checkResourceAlerts,
		},
		{
			// Docker lane: several `docker ps` calls, up to 15s each.
			name:     "docker",
			interval: func(_ *Config) time.Duration { return fastWatchdogInterval },
			run:      checkDockerAlerts,
		},
		{
			// Disk lane: statfs on every partition, no timeout available.
			name:     "disks",
			interval: func(_ *Config) time.Duration { return fastWatchdogInterval },
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				checkDiskMounts(ctx, bot)
			},
		},
		{
			name: "network",
			interval: func(cfg *Config) time.Duration {
				return resolveInterval(cfg.NetworkWatchdog.CheckIntervalSecs, 10*time.Second, 60*time.Second)
			},
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				if ctx.Cfg().NetworkWatchdog.Enabled {
					checkNetworkHealth(ctx, bot, runCtx)
				}
			},
		},
		{
			name: "kernel",
			interval: func(cfg *Config) time.Duration {
				return resolveInterval(cfg.KernelWatchdog.CheckIntervalSecs, 10*time.Second, 60*time.Second)
			},
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				if ctx.Cfg().KernelWatchdog.Enabled {
					checkKernelEvents(ctx, bot)
				}
			},
		},
		{
			name: "raid",
			interval: func(cfg *Config) time.Duration {
				return resolveInterval(cfg.RaidWatchdog.CheckIntervalSecs, 30*time.Second, 5*time.Minute)
			},
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				if ctx.Cfg().RaidWatchdog.Enabled {
					checkRaidHealth(ctx, bot)
				}
			},
		},
		{
			// Filesystem lane: statfs sui volumi monitorati, O(1) e senza I/O,
			// ma oltre la soglia critical scatta anche una deep scan ricorsiva
			// del volume intero. Per questo la cadenza è di decine di minuti e
			// non le 10s delle lane veloci, e il primo giro arriva dopo un
			// minuto invece che dopo un intervallo pieno.
			name:         "fs-space",
			initialDelay: func(_ *Config) time.Duration { return fsWatchdogFirstCheckDelay },
			interval: func(cfg *Config) time.Duration {
				return resolveInterval(cfg.FSWatchdog.CheckIntervalMins*60, time.Minute, fsWatchdogDefaultInterval)
			},
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				if ctx.Cfg().FSWatchdog.Enabled {
					checkFSWatchdogPaths(ctx, bot, runCtx)
				}
			},
		},
		{
			name:     "disk-usage",
			interval: func(_ *Config) time.Duration { return 5 * time.Minute },
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				recordDiskUsage(ctx)
			},
		},
		{
			name:     "trend",
			interval: func(_ *Config) time.Duration { return 5 * time.Minute },
			run: func(ctx *AppContext, bot BotAPI, runCtx context.Context) {
				recordTrendPoint(ctx)
			},
		},
	}
}

// checkResourceAlerts runs the state-only checks: resource stress, CPU
// temperature and the RAM-critical auto-restart decision.
func checkResourceAlerts(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	s, ready := ctx.Stats.Get()
	if !ready {
		return
	}
	cfg := ctx.Cfg()

	if cfg.StressTracking.Enabled {
		if cfg.Notifications.DiskIO.Enabled {
			checkResourceStress(ctx, bot, "HDD", s.DiskUtil, cfg.Notifications.DiskIO.WarningThreshold)
		}
		if cfg.Notifications.CPU.Enabled {
			checkResourceStress(ctx, bot, "CPU", s.CPU, cfg.Notifications.CPU.WarningThreshold)
		}
		if cfg.Notifications.RAM.Enabled {
			checkResourceStress(ctx, bot, "RAM", s.RAM, cfg.Notifications.RAM.WarningThreshold)
		}
		if cfg.Notifications.Swap.Enabled {
			checkResourceStress(ctx, bot, "Swap", s.Swap, cfg.Notifications.Swap.WarningThreshold)
		}
		if cfg.Notifications.DiskSSD.Enabled {
			checkResourceStress(ctx, bot, "SSD", s.VolSSD.Used, cfg.Notifications.DiskSSD.WarningThreshold)
		}
	}

	if cfg.Temperature.Enabled {
		checkTemperatureAlert(ctx, bot)
	}

	if cfg.Docker.AutoRestartOnRAMCritical.Enabled {
		if s.RAM >= cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold {
			handleCriticalRAM(ctx, bot, s)
		}
	}

	cleanRestartCounter(ctx)
}

// checkDockerAlerts runs the Docker-dependent checks. Every one of them shells
// out to the docker CLI, so they share a lane and a 10s cadence.
func checkDockerAlerts(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	cfg := ctx.Cfg()

	if cfg.Docker.Watchdog.Enabled {
		checkDockerHealth(ctx, bot)
	}
	checkContainerStates(ctx, bot)
	checkCriticalContainers(ctx, bot)

	// No gate here on purpose. ctx.Settings.DockerPrune.Enabled is the single
	// authority for the weekly prune: it is the only one writable at runtime and
	// the only one restored from state.json. A second gate on the config file
	// (read once at boot) silently ignored the settings toggle, so a user who
	// enabled the prune from /settings never saw it run.
	checkWeeklyPrune(ctx, bot)
}

// runWatchdogLoop ticks one watchdog until runCtx is done. A Timer is used
// instead of a Ticker on purpose: the next deadline is computed after the check
// returns, so a slow probe delays only itself and no tick is ever lost.
func runWatchdogLoop(runCtx context.Context, ctx *AppContext, bot BotAPI, wd watchdog) {
	interval := wd.interval(ctx.Cfg())
	if interval <= 0 {
		interval = time.Minute
	}
	first := interval
	if wd.initialDelay != nil {
		if d := wd.initialDelay(ctx.Cfg()); d > 0 {
			first = d
		}
	}
	timer := time.NewTimer(first)
	defer timer.Stop()

	for {
		select {
		case <-runCtx.Done():
			return
		case <-timer.C:
			wd.run(ctx, bot, runCtx)

			select {
			case <-runCtx.Done():
				return
			default:
			}

			// Re-read the cadence: /config must change the watchdog period
			// without a restart.
			next := wd.interval(ctx.Cfg())
			if next <= 0 {
				next = time.Minute
			}
			timer.Reset(next)
		}
	}
}

// autonomousManager performs automatic monitoring decisions.
func autonomousManager(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	cfg := ctx.Cfg()

	if cfg.KernelWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("kw_started"), cfg.KernelWatchdog.CheckIntervalSecs))
	}
	if cfg.NetworkWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("netwd_started"), cfg.NetworkWatchdog.CheckIntervalSecs))
	}
	if cfg.RaidWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("raidwd_started"), cfg.RaidWatchdog.CheckIntervalSecs))
	}

	// Child context: cancelling it stops every watchdog, including the ones
	// still blocked inside a probe. defer cancel() also runs if this function
	// panics, so no watchdog can outlive its parent.
	childCtx, cancel := context.WithCancel(runCtx)
	defer cancel()

	var wg sync.WaitGroup
	for _, wd := range watchdogsFor(ctx) {
		wg.Add(1)
		goSafe("watchdog-"+wd.name, func() {
			defer wg.Done()
			runWatchdogLoop(childCtx, ctx, bot, wd)
		})
	}

	// Stay alive for the whole run so the deferred cancel() only fires on
	// shutdown or panic, never when the last watchdog happens to return.
	<-runCtx.Done()
	wg.Wait()
}
