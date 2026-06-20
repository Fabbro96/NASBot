package app

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

// truncate is a convenience alias kept for readability in call sites (e.g. docker.go).
func truncate(s string, max int) string { return format.Truncate(s, max) }

// readCPUTemp reads CPU temperature from thermal zone or hwmon
func readCPUTemp() float64 {
	candidates := []string{
		"/sys/class/thermal/thermal_zone0/temp",
		"/sys/class/thermal/thermal_zone1/temp",
		"/sys/class/hwmon/hwmon0/temp1_input",
		"/sys/class/hwmon/hwmon1/temp1_input",
		"/sys/class/hwmon/hwmon2/temp1_input",
	}

	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err == nil {
			val, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err == nil {
				temp := float64(val) / 1000.0
				if temp > -50 && temp < 150 {
					return temp
				}
			}
		}
	}
	return 0
}

// readDiskSMART reads disk SMART data
func readDiskSMART(device string) (temp int, health string) {
	temp = -1
	health = "UNKNOWN"

	// Use separate contexts for sequential commands to avoid timeout overlaps
	ctxA, cancelA := context.WithTimeout(context.Background(), 2*time.Second)
	outA, attrErr := runCommandStdout(ctxA, "sudo", "-n", "smartctl", "-A", "/dev/"+device)
	cancelA()

	for _, line := range strings.Split(string(outA), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "Temperature_Celsius") || strings.Contains(line, "Temperature_Internal") {
			fields := strings.Fields(line)
			if len(fields) >= 10 {
				t, _ := strconv.Atoi(fields[9])
				if t > 0 {
					temp = t
				}
			}
		} else if strings.HasPrefix(trimmed, "Temperature:") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				t, _ := strconv.Atoi(fields[1])
				if t > 0 {
					temp = t
				}
			}
		} else if strings.HasPrefix(trimmed, "Temperature Sensor") { // NVMe extended sensors
			fields := strings.Fields(trimmed)
			// Example: "Temperature Sensor 1:   35 Celsius"
			for i, f := range fields {
				if f == "Celsius" && i > 0 {
					t, _ := strconv.Atoi(fields[i-1])
					if t > 0 {
						temp = t
					}
				}
			}
		}
	}

	ctxH, cancelH := context.WithTimeout(context.Background(), 2*time.Second)
	outH, healthErr := runCommandStdout(ctxH, "sudo", "-n", "smartctl", "-H", "/dev/"+device)
	cancelH()

	passed := false
	failed := false
	for _, line := range strings.Split(string(outH), "\n") {
		if strings.Contains(line, "PASSED") {
			passed = true
		} else if strings.Contains(line, "FAILED") {
			failed = true
		}
	}

	if failed {
		health = "FAILED!"
	} else if passed {
		health = "PASSED"
	} else if healthErr == nil {
		health = "OK"
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
