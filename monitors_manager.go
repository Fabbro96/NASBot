package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// autonomousManager performs automatic monitoring decisions.
func autonomousManager(ctx *AppContext, bot BotAPI, runCtx ...context.Context) {
	cfg := ctx.Config
	rc := context.Background()
	if len(runCtx) > 0 && runCtx[0] != nil {
		rc = runCtx[0]
	}

	ticker := time.NewTicker(10 * time.Second)
	diskTicker := time.NewTicker(5 * time.Minute)
	trendTicker := time.NewTicker(5 * time.Minute)

	netInterval := time.Duration(cfg.NetworkWatchdog.CheckIntervalSecs) * time.Second
	if netInterval < 10*time.Second {
		netInterval = 60 * time.Second
	}
	netTicker := time.NewTicker(netInterval)

	raidInterval := time.Duration(cfg.RaidWatchdog.CheckIntervalSecs) * time.Second
	if raidInterval < 30*time.Second {
		raidInterval = 5 * time.Minute
	}
	raidTicker := time.NewTicker(raidInterval)

	kwInterval := time.Duration(cfg.KernelWatchdog.CheckIntervalSecs) * time.Second
	if kwInterval < 10*time.Second {
		kwInterval = 60 * time.Second
	}
	kwTicker := time.NewTicker(kwInterval)

	defer ticker.Stop()
	defer diskTicker.Stop()
	defer trendTicker.Stop()
	defer kwTicker.Stop()
	defer netTicker.Stop()
	defer raidTicker.Stop()

	if cfg.KernelWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("kw_started"), cfg.KernelWatchdog.CheckIntervalSecs))
	}
	if cfg.NetworkWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("netwd_started"), cfg.NetworkWatchdog.CheckIntervalSecs))
	}
	if cfg.RaidWatchdog.Enabled {
		slog.Info(fmt.Sprintf(ctx.Tr("raidwd_started"), cfg.RaidWatchdog.CheckIntervalSecs))
	}

	for {
		select {
		case <-rc.Done():
			return
		case <-ticker.C:
			s, ready := ctx.Stats.Get()
			if !ready {
				continue
			}

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

			if cfg.Docker.Watchdog.Enabled {
				checkDockerHealth(ctx, bot)
			}

			checkContainerStates(ctx, bot)
			checkCriticalContainers(ctx, bot)

			if cfg.Docker.WeeklyPrune.Enabled {
				checkWeeklyPrune(ctx, bot)
			}

		case <-diskTicker.C:
			recordDiskUsage(ctx)
		case <-trendTicker.C:
			recordTrendPoint(ctx)
		case <-kwTicker.C:
			if cfg.KernelWatchdog.Enabled {
				checkKernelEvents(ctx, bot)
			}
		case <-netTicker.C:
			if cfg.NetworkWatchdog.Enabled {
				checkNetworkHealth(ctx, bot)
			}
		case <-raidTicker.C:
			if cfg.RaidWatchdog.Enabled {
				checkRaidHealth(ctx, bot)
			}
		}
	}
}
