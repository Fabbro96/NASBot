package main

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"nasbot/internal/cmdexec"
	"nasbot/internal/format"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Wrappers for format package (kept for backward compatibility)
func formatUptime(seconds uint64) string           { return format.FormatUptime(seconds) }
func formatBytes(bytes uint64) string              { return format.FormatBytes(bytes) }
func formatRAM(mb uint64) string                   { return format.FormatRAM(mb) }
func formatDuration(d time.Duration) string        { return format.FormatDuration(d) }
func formatPeriod(seconds int) string              { return format.FormatPeriod(seconds) }
func truncate(s string, max int) string            { return format.Truncate(s, max) }
func safeFloat(arr []float64, def float64) float64 { return format.SafeFloat(arr, def) }
func boolToEmoji(b bool) string                    { return format.BoolToEmoji(b) }
func makeProgressBar(percent float64) string       { return format.MakeProgressBar(percent) }
func titleCaseWord(s string) string                { return format.TitleCaseWord(s) }

// readCPUTemp reads CPU temperature from thermal zone
func readCPUTemp() float64 {
	raw, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	val, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return float64(val) / 1000.0
}

// readDiskSMART reads disk SMART data
func readDiskSMART(device string) (temp int, health string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	health = "UNKNOWN"

	out, attrErr := runCommandStdout(ctx, "smartctl", "-A", "/dev/"+device)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "Temperature_Celsius") || strings.Contains(line, "Temperature_Internal") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				temp, _ = strconv.Atoi(fields[9])
			}
		}
	}

	out, healthErr := runCommandStdout(ctx, "smartctl", "-H", "/dev/"+device)
	if healthErr == nil {
		health = "OK"
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "PASSED") {
			health = "PASSED"
		} else if strings.Contains(line, "FAILED") {
			health = "FAILED!"
		}
	}

	if attrErr != nil {
		slog.Warn("smartctl attribute read failed", "device", device, "err", attrErr)
	}
	if healthErr != nil {
		slog.Warn("smartctl health read failed", "device", device, "err", healthErr)
	}

	return temp, health
}

// parseUptime parses Docker container uptime
func parseUptime(status string) string {
	if !strings.Contains(status, "Up") {
		return "stopped"
	}
	parts := strings.Fields(status)
	if len(parts) >= 2 {
		result := parts[1]
		if len(parts) >= 3 {
			result += " " + parts[2]
		}
		return result
	}
	return "running"
}

// getSmartDevices returns configured devices or defaults to sda/sdb
func getSmartDevices(ctx *AppContext) []string {
	if ctx != nil && ctx.Config != nil && len(ctx.Config.Notifications.SMART.Devices) > 0 {
		return ctx.Config.Notifications.SMART.Devices
	}
	return []string{"sda", "sdb"}
}

// safeSend sends a Telegram message and logs any error
func safeSend(bot BotAPI, msg tgbotapi.Chattable) {
	if bot == nil {
		return
	}
	if _, err := bot.Send(msg); err != nil {
		slog.Error("Telegram send failed", "err", err)
	}
}

func setCommandRunner(r cmdexec.Runner) (restore func()) {
	return cmdexec.SetRunner(r)
}

func commandExists(name string) bool {
	return cmdexec.Exists(name)
}

func runCommandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return cmdexec.CombinedOutput(ctx, name, args...)
}

func runCommandStdout(ctx context.Context, name string, args ...string) ([]byte, error) {
	return cmdexec.Output(ctx, name, args...)
}

func runCommand(ctx context.Context, name string, args ...string) error {
	return cmdexec.Run(ctx, name, args...)
}
