package app

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

// checkNetworkHealth runs the full network probe. It is the slowest watchdog
// (~18s worst case: 2 ping x 2 attempts x 2s + 1s pause, plus 2 DNS lookups of
// 3s + 1s pause), which is why it owns its own goroutine and its own ticker.
// runCtx only shortens shutdown: a probe in flight is abandoned instead of
// holding the goroutine (and the 60s ticker) past the signal.
func checkNetworkHealth(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	cfg := ctx.Cfg()
	forceRebootAfter := networkForceRebootAfter(cfg)

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.NetLastCheckTime = time.Now()
	ctx.Monitor.Mu.Unlock()

	targets := cfg.NetworkWatchdog.Targets
	if len(targets) == 0 {
		targets = []string{"9.9.9.9", "1.1.1.1"}
	}
	dnsHost := cfg.NetworkWatchdog.DNSHost
	if dnsHost == "" {
		dnsHost = "quad9.net"
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
		if pingHost(runCtx, cfg.NetworkWatchdog.Gateway) {
			pingOk = true
		} else {
			reasons = append(reasons, fmt.Sprintf("Gateway %s unreachable", cfg.NetworkWatchdog.Gateway))
		}
	}

	for _, target := range targets {
		if pingHost(runCtx, target) {
			pingOk = true
			break
		}
	}
	if !pingOk {
		reasons = append(reasons, "No ping targets reachable")
	}

	dnsOk := checkDNS(runCtx, dnsHost)
	if !dnsOk {
		reasons = append(reasons, fmt.Sprintf("DNS lookup failed: %s", dnsHost))
	}

	// Healthy network
	if pingOk && dnsOk {
		var shouldNotify bool
		var downSince time.Time
		ctx.Monitor.Mu.Lock()
		ctx.Monitor.NetFailCount = 0
		ctx.Monitor.NetConsecutiveDegraded = 0
		if !ctx.Monitor.NetDownSince.IsZero() {
			shouldNotify = cfg.NetworkWatchdog.RecoveryNotify
			downSince = ctx.Monitor.NetDownSince
			ctx.Monitor.NetDownSince = time.Time{}
			ctx.Monitor.NetForceRebootTriggered = false
		}
		ctx.Monitor.Mu.Unlock()

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
		ctx.Monitor.Mu.Lock()
		ctx.Monitor.NetConsecutiveDegraded++
		if time.Since(ctx.Monitor.NetDNSAlertTime) >= cooldown {
			ctx.Monitor.NetDNSAlertTime = time.Now()
			shouldNotify = true
		}
		ctx.Monitor.Mu.Unlock()

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
	ctx.Monitor.Mu.Lock()
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
	ctx.Monitor.Mu.Unlock()

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

// pingHost tries up to 2 times with a 1 second pause. It returns false as soon
// as runCtx is done, so a shutdown during a probe does not wait out the
// remaining attempts.
func pingHost(runCtx context.Context, host string) bool {
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(runCtx, 2*time.Second)
		err := runCommand(ctx, "ping", "-c", "1", "-W", "2", host)
		cancel()
		if err == nil {
			return true // Success
		}
		if runCtx.Err() != nil {
			return false
		}
		if i < 1 {
			if !sleepWithContext(runCtx, 1*time.Second) {
				return false
			}
		}
	}
	return false
}

func checkDNS(runCtx context.Context, host string) bool {
	if doCheckDNS(runCtx, host) {
		return true
	}
	if !sleepWithContext(runCtx, 1*time.Second) {
		return false
	}
	return doCheckDNS(runCtx, host)
}

func doCheckDNS(runCtx context.Context, host string) bool {
	ctx, cancel := context.WithTimeout(runCtx, 3*time.Second)
	defer cancel()

	r := &net.Resolver{}
	_, err := r.LookupHost(ctx, host)
	return err == nil
}
