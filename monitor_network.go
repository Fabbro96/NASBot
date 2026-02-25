package main

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  NETWORK WATCHDOG — gateway, internet reachability, DNS
// ═══════════════════════════════════════════════════════════════════

func checkNetworkHealth(ctx *AppContext, bot BotAPI) {
	cfg := ctx.Config
	forceRebootAfter := networkForceRebootAfter(cfg)

	ctx.Monitor.mu.Lock()
	ctx.Monitor.NetLastCheckTime = time.Now()
	ctx.Monitor.mu.Unlock()

	targets := cfg.NetworkWatchdog.Targets
	if len(targets) == 0 {
		targets = []string{"1.1.1.1", "8.8.8.8"}
	}
	dnsHost := cfg.NetworkWatchdog.DNSHost
	if dnsHost == "" {
		dnsHost = "google.com"
	}
	threshold := cfg.NetworkWatchdog.FailureThreshold
	if threshold <= 0 {
		threshold = 3
	}
	cooldown := time.Duration(cfg.NetworkWatchdog.CooldownMins) * time.Minute
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}

	pingOk := false
	var reasons []string

	if cfg.NetworkWatchdog.Gateway != "" {
		if pingHost(cfg.NetworkWatchdog.Gateway) {
			pingOk = true
		} else {
			reasons = append(reasons, fmt.Sprintf("Gateway %s unreachable", cfg.NetworkWatchdog.Gateway))
		}
	}

	for _, target := range targets {
		if pingHost(target) {
			pingOk = true
			break
		}
	}
	if !pingOk {
		reasons = append(reasons, "No ping targets reachable")
	}

	dnsOk := checkDNS(dnsHost)
	if !dnsOk {
		reasons = append(reasons, fmt.Sprintf("DNS lookup failed: %s", dnsHost))
	}

	// Healthy network
	if pingOk && dnsOk {
		var shouldNotify bool
		var downSince time.Time
		ctx.Monitor.mu.Lock()
		ctx.Monitor.NetFailCount = 0
		ctx.Monitor.NetConsecutiveDegraded = 0
		if !ctx.Monitor.NetDownSince.IsZero() {
			shouldNotify = cfg.NetworkWatchdog.RecoveryNotify
			downSince = ctx.Monitor.NetDownSince
			ctx.Monitor.NetDownSince = time.Time{}
			ctx.Monitor.NetForceRebootTriggered = false
		}
		ctx.Monitor.mu.Unlock()

		if shouldNotify && !ctx.IsQuietHours() {
			msg := fmt.Sprintf(ctx.Tr("net_recovered"), format.FormatDuration(time.Since(downSince)))
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			safeSend(bot, m)
		}
		return
	}

	// DNS-only issue
	if pingOk && !dnsOk {
		shouldNotify := false
		ctx.Monitor.mu.Lock()
		ctx.Monitor.NetConsecutiveDegraded++
		if time.Since(ctx.Monitor.NetDNSAlertTime) >= cooldown {
			ctx.Monitor.NetDNSAlertTime = time.Now()
			shouldNotify = true
		}
		ctx.Monitor.mu.Unlock()

		if shouldNotify {
			msg := fmt.Sprintf(ctx.Tr("net_dns_fail"), dnsHost)
			m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
			m.ParseMode = "Markdown"
			if !ctx.IsQuietHours() {
				safeSend(bot, m)
			}
			ctx.State.AddEvent("warning", "DNS lookup failure")
		}
		return
	}

	// Full network failure
	var shouldAlert bool
	var shouldForceReboot bool
	var downFor time.Duration
	ctx.Monitor.mu.Lock()
	ctx.Monitor.NetFailCount++
	ctx.Monitor.NetConsecutiveDegraded++
	if ctx.Monitor.NetFailCount >= threshold {
		if ctx.Monitor.NetDownSince.IsZero() {
			ctx.Monitor.NetDownSince = time.Now()
		}
		downFor = time.Since(ctx.Monitor.NetDownSince)
		if time.Since(ctx.Monitor.NetDownAlertTime) >= cooldown {
			ctx.Monitor.NetDownAlertTime = time.Now()
			shouldAlert = true
		}
		if forceRebootAfter > 0 && downFor >= forceRebootAfter && !ctx.Monitor.NetForceRebootTriggered {
			ctx.Monitor.NetForceRebootTriggered = true
			shouldForceReboot = true
		}
	}
	ctx.Monitor.mu.Unlock()

	if shouldAlert {
		msg := fmt.Sprintf(ctx.Tr("net_down"), strings.Join(reasons, "\n- "))
		m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		if !ctx.IsQuietHours() {
			safeSend(bot, m)
		}
		ctx.State.AddEvent("critical", "Network unreachable")
	}

	if shouldForceReboot {
		msg := fmt.Sprintf(ctx.Tr("net_force_reboot"), format.FormatDuration(downFor))
		m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
		executeForcedReboot(ctx, bot, cfg.AllowedUserID, 0, "network-down-timeout")
	}
}

func networkForceRebootAfter(cfg *Config) time.Duration {
	if cfg == nil || !cfg.NetworkWatchdog.ForceRebootOnDown {
		return 0
	}
	mins := cfg.NetworkWatchdog.ForceRebootAfterMins
	if mins <= 0 {
		mins = 3
	}
	return time.Duration(mins) * time.Minute
}

func pingHost(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return runCommand(ctx, "ping", "-c", "1", "-W", "2", host) == nil
}

func checkDNS(host string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	r := &net.Resolver{}
	_, err := r.LookupHost(ctx, host)
	return err == nil
}
