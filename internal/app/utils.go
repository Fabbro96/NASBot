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
	pmodel "nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// truncate is a convenience alias kept for readability in call sites (e.g. docker.go).
func truncate(s string, max int) string { return format.Truncate(s, max) }

// telegramMaxTextRunes is the per-message character budget Telegram enforces.
// Anything longer is rejected with "message is too long".
const telegramMaxTextRunes = 4096

// sanitizeSecrets masks the credentials embedded in an error or a text.
//
// It is applied to every Telegram error before it reaches slog and before it
// reaches a Telegram message: the same error object is used for both, so
// sanitizing only the log would still leak the token into the chat.
//
// The pattern lives in pkg/model (model.SanitizeSecrets): pkg/commands logs and
// sends Telegram errors too and cannot import internal/app, so a second copy of
// the pattern here would be one more thing to keep in sync.
func sanitizeSecrets(text string) string { return pmodel.SanitizeSecrets(text) }

// sanitizeErr is sanitizeSecrets for an error, keeping the original in the chain
// so errors.Is/errors.As keep working.
func sanitizeErr(err error) error { return pmodel.SanitizeErr(err) }

// splitTextChunks slices text into chunks of at most max runes, so a message
// longer than Telegram's limit is split instead of rejected outright.
func splitTextChunks(text string, max int) []string {
	if max <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= max {
		return []string{text}
	}
	chunks := make([]string, 0, (len(runes)/max)+1)
	for i := 0; i < len(runes); i += max {
		end := i + max
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

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
	if ctx != nil && ctx.Cfg() != nil && len(ctx.Cfg().Notifications.SMART.Devices) > 0 {
		return ctx.Cfg().Notifications.SMART.Devices
	}
	return []string{"sda", "sdb"}
}

// sendWithParseFallback sends msg and, when the send fails on the parse mode,
// retries once with it cleared, so a single unbalanced Markdown marker costs the
// formatting of one message instead of the whole text.
//
// It returns the error of the last attempt, or nil when something was delivered.
func sendWithParseFallback(bot BotAPI, msg tgbotapi.Chattable) error {
	if bot == nil {
		return nil
	}
	_, err := bot.Send(msg)
	if err == nil {
		return nil
	}

	switch typed := msg.(type) {
	case tgbotapi.MessageConfig:
		if typed.ParseMode == "" {
			return err
		}
		slog.Warn("Telegram markdown send failed, retrying without parse mode", "err", sanitizeErr(err))
		typed.ParseMode = ""
		if _, retryErr := bot.Send(typed); retryErr != nil {
			return retryErr
		}
		return nil
	case *tgbotapi.MessageConfig:
		if typed == nil || typed.ParseMode == "" {
			return err
		}
		slog.Warn("Telegram markdown send failed, retrying without parse mode", "err", sanitizeErr(err))
		typed.ParseMode = ""
		if _, retryErr := bot.Send(typed); retryErr != nil {
			return retryErr
		}
		return nil
	case tgbotapi.EditMessageTextConfig:
		if typed.ParseMode == "" {
			return err
		}
		slog.Warn("Telegram markdown edit failed, retrying without parse mode", "err", sanitizeErr(err))
		typed.ParseMode = ""
		if _, retryErr := bot.Send(typed); retryErr != nil {
			return retryErr
		}
		return nil
	case *tgbotapi.EditMessageTextConfig:
		if typed == nil || typed.ParseMode == "" {
			return err
		}
		slog.Warn("Telegram markdown edit failed, retrying without parse mode", "err", sanitizeErr(err))
		typed.ParseMode = ""
		if _, retryErr := bot.Send(typed); retryErr != nil {
			return retryErr
		}
		return nil
	}
	return err
}

// safeSend sends a Telegram message and logs any error, falling back to plain
// text if Markdown fails.
func safeSend(bot BotAPI, msg tgbotapi.Chattable) {
	if err := sendWithParseFallback(bot, msg); err != nil {
		slog.Error("Telegram send failed", "err", sanitizeErr(err))
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
