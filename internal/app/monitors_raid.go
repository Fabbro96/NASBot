package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func checkRaidHealth(ctx *AppContext, bot BotAPI) {
	cfg := ctx.Cfg()
	issues := getRaidIssues()

	if len(issues) == 0 {
		var shouldNotify bool
		var downSince time.Time
		ctx.Monitor.Mu.Lock()
		if !ctx.Monitor.RaidDownSince.IsZero() {
			shouldNotify = cfg.RaidWatchdog.RecoveryNotify
			downSince = ctx.Monitor.RaidDownSince
			ctx.Monitor.RaidDownSince = time.Time{}
			ctx.Monitor.RaidLastSignature = ""
		}
		ctx.Monitor.Mu.Unlock()

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
	ctx.Monitor.Mu.Lock()
	if ctx.Monitor.RaidDownSince.IsZero() {
		ctx.Monitor.RaidDownSince = time.Now()
	}
	// The cooldown is a hard floor. The previous code ORed it with a signature
	// change, so a resync line whose progress percentage changes on every cycle
	// produced a new signature and re-alerted every cycle, cooldown or not.
	// A genuinely new condition therefore waits at most one cooldown, except
	// for the very first detection which must not wait at all.
	if ctx.Monitor.RaidLastSignature == "" || time.Since(ctx.Monitor.RaidAlertTime) >= cooldown {
		shouldAlert = true
		ctx.Monitor.RaidAlertTime = time.Now()
	}
	ctx.Monitor.RaidLastSignature = signature
	ctx.Monitor.Mu.Unlock()

	if shouldAlert {
		msg := fmt.Sprintf(ctx.Tr("raid_alert"), strings.Join(issues, "\n"))
		m := tgbotapi.NewMessage(cfg.AllowedUserID, msg)
		m.ParseMode = "Markdown"
		safeSend(bot, m)
		ctx.State.AddEvent("critical", "RAID issue detected")
	}
}

// ═══════════════════════════════════════════════════════════════════
//  /proc/mdstat parsing
//
//  Layout of the file (Linux 5.x md):
//
//	Personalities : [raid1] [raid6]
//	md1 : active raid1 sda2[0] sdb2[1](F)
//	      976630336 blocks super 1.2 [2/1] [U_]
//
//	md0 : active raid1 sdb1[1] sda1[0]
//	      976630336 blocks super 1.2 [2/2] [UU]
//	      [==>..................]  recovery = 12.3% (123456/976630336) finish=10.0min
//
//  The degraded/failed/resync information lives on the SECOND (and third) line
//  of each array, which is exactly what the old `strings.Contains(line, "md")`
//  guard filtered out: /status stayed silent while the array was broken.
// ═══════════════════════════════════════════════════════════════════

// reMdArrayHeader matches the per-array header, capturing the name and the
// device list: "md1 : active raid1 sda2[0] sdb2[1](F)".
var reMdArrayHeader = regexp.MustCompile(`^(md\d+)\s*:\s*(\S.*)$`)

// reMdDeviceFlag matches a device slot with a state flag, e.g. "sdb2[1](F)".
// F = failed, S = spare.
var reMdDeviceFlag = regexp.MustCompile(`(\S+)\[(\d+)\]\(([A-Z])\)`)

// reMdStatusGroup matches the "[2/1] [U_]" summary field: total/usable devices
// followed by one character per device (U up, _ down).
var reMdStatusGroup = regexp.MustCompile(`\[(\d+)/(\d+)\]\s*\[([U_]+)\]`)

// mdSyncKeywords are the resync-like operations reported on their own line.
var mdSyncKeywords = []string{"recovery", "resync", "reshape", "check", "scrub"}

// parseMdstatIssues extracts every problem found in the text of /proc/mdstat.
// It is pure so the test table can drive it without touching the filesystem.
func parseMdstatIssues(text string) []string {
	var issues []string
	array := ""

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}

		// Header line: opens a new array context and carries the device slots.
		if m := reMdArrayHeader.FindStringSubmatch(line); m != nil {
			array = m[1]
			for _, d := range reMdDeviceFlag.FindAllStringSubmatch(m[2], -1) {
				if d[3] == "F" {
					issues = append(issues, fmt.Sprintf("mdadm failed device: %s %s[%s](F)", array, d[1], d[2]))
				}
			}
			continue
		}

		// Everything below needs an array context, otherwise "Personalities :
		// [raid1] [raid6]" and "unused devices: <none>" would be scanned.
		if array == "" {
			continue
		}

		// "976630336 blocks super 1.2 [2/1] [U_]" — degraded when a slot is down.
		if m := reMdStatusGroup.FindStringSubmatch(line); m != nil {
			usable, pattern := m[2], m[3]
			if usable != "0" && strings.Contains(pattern, "_") {
				issues = append(issues, fmt.Sprintf("mdadm degraded: %s [%s/%s] [%s]", array, m[1], usable, pattern))
			}
			continue
		}

		// A bare "(F)" outside a header (older kernels) is still a failure.
		if strings.Contains(line, "(F)") {
			issues = append(issues, fmt.Sprintf("mdadm failed device: %s %s", array, line))
			continue
		}

		// Resync-like progress line. The message intentionally omits the
		// percentage and the byte counters: they change on every read and would
		// make the alert signature unstable (see checkRaidHealth).
		low := strings.ToLower(line)
		for _, kw := range mdSyncKeywords {
			if strings.Contains(low, kw) {
				issues = append(issues, fmt.Sprintf("mdadm sync: %s %s in progress", array, kw))
				break
			}
		}
	}

	return issues
}

func getRaidIssues() []string {
	var issues []string
	if data, err := os.ReadFile("/proc/mdstat"); err == nil {
		issues = append(issues, parseMdstatIssues(string(data))...)
	} else {
		slog.Debug("RAID: /proc/mdstat unreadable", "err", err)
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
