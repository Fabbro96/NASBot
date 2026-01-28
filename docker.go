package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// getContainerList gets list of all Docker containers
func getContainerList() []ContainerInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}|{{.ID}}")
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("x Docker error: %v - Output: %s", err, string(out))
		return nil
	}

	var containers []ContainerInfo
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) >= 4 {
			containers = append(containers, ContainerInfo{
				Name:    parts[0],
				Status:  parts[1],
				Image:   parts[2],
				ID:      parts[3],
				Running: strings.Contains(parts[1], "Up"),
			})
		}
	}
	return containers
}

// sendDockerMenu sends the Docker container menu
func sendDockerMenu(bot *tgbotapi.BotAPI, chatID int64) {
	text, kb := getDockerMenuText()
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if kb != nil {
		msg.ReplyMarkup = kb
	}
	bot.Send(msg)
}

// getDockerMenuText generates the Docker menu text and keyboard
func getDockerMenuText() (string, *tgbotapi.InlineKeyboardMarkup) {
	containers := getContainerList()
	if len(containers) == 0 {
		mainKb := getMainKeyboard()
		return "_No containers found. Is Docker running?_", &mainKb
	}

	var b strings.Builder
	b.WriteString("🐳 *Containers*\n\n")

	running, stopped := 0, 0
	for _, c := range containers {
		icon := "⏸"
		statusText := "stopped"
		if c.Running {
			icon = "▶️"
			statusText = parseUptime(c.Status)
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("%s *%s* — %s\n", icon, c.Name, statusText))
	}

	b.WriteString(fmt.Sprintf("\n_%d running, %d stopped_", running, stopped))

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(containers); i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		for j := 0; j < 2 && i+j < len(containers); j++ {
			c := containers[i+j]
			icon := "⏸"
			if c.Running {
				icon = "▶"
			}
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s %s", icon, truncate(c.Name, 10)),
				"container_select_"+c.Name,
			))
		}
		rows = append(rows, row)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "show_docker"),
		tgbotapi.NewInlineKeyboardButtonData("🏠 Menu", "back_main"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.String(), &kb
}

// getDockerText returns Docker containers text
func getDockerText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "ps", "-a", "--format", "{{.Names}}|{{.Status}}|{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "*timeout*"
		}
		return "*docker n/a*"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "_No containers found_"
	}

	var b strings.Builder
	b.WriteString("*Container*\n────────────────────\n")

	running, stopped := 0, 0
	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		name := truncate(parts[0], 14)
		status := parts[1]

		icon := "-"
		statusShort := "off"
		if strings.Contains(status, "Up") {
			icon = "+"
			statusParts := strings.Fields(status)
			if len(statusParts) >= 2 {
				statusShort = statusParts[1]
				if len(statusParts) >= 3 && len(statusParts[2]) > 0 {
					statusShort += string(statusParts[2][0])
				}
			}
			running++
		} else {
			stopped++
		}
		b.WriteString(fmt.Sprintf("\n%s %-14s %s", icon, name, statusShort))
	}

	b.WriteString(fmt.Sprintf("\n\nContainers: %d running · %d stopped", running, stopped))
	return b.String()
}

// getDockerStatsText returns container resource usage stats
func getDockerStatsText() string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "*timeout*"
		}
		return "*stats n/a*"
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return "_No containers running_"
	}

	var b strings.Builder
	b.WriteString("*Container resources*\n```\n")
	b.WriteString(fmt.Sprintf("%-12s %5s %5s %s\n", "NAME", "CPU", "MEM%", "MEM"))

	for _, line := range lines {
		parts := strings.Split(line, "|")
		if len(parts) < 4 {
			continue
		}
		name := truncate(parts[0], 12)
		cpuP := strings.TrimSpace(parts[1])
		memUsage := strings.TrimSpace(parts[2])
		memP := strings.TrimSpace(parts[3])

		memShort := strings.Split(memUsage, " ")[0]
		memShort = strings.Replace(memShort, "MiB", "M", 1)
		memShort = strings.Replace(memShort, "GiB", "G", 1)

		b.WriteString(fmt.Sprintf("%-12s %5s %5s %s\n", name, cpuP, memP, memShort))
	}
	b.WriteString("```")

	return b.String()
}

// getContainerStats gets stats for a specific container
func getContainerStats(containerName string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) < 4 {
		return ""
	}

	return fmt.Sprintf("   CPU: `%s` │ RAM: `%s` (`%s`)\n   Net: `%s`",
		strings.TrimSpace(parts[0]),
		strings.TrimSpace(parts[1]),
		strings.TrimSpace(parts[2]),
		strings.TrimSpace(parts[3]))
}

// handleContainerCallback handles container-related callbacks
func handleContainerCallback(bot *tgbotapi.BotAPI, chatID int64, msgID int, data string) {
	parts := strings.Split(data, "_")
	if len(parts) < 3 {
		return
	}

	action := parts[1]

	switch action {
	case "select", "start", "stop", "restart", "logs", "kill", "cancel":
		containerName := strings.Join(parts[2:], "_")
		switch action {
		case "select":
			showContainerActions(bot, chatID, msgID, containerName)
		case "start", "stop", "restart", "logs", "kill":
			confirmContainerAction(bot, chatID, msgID, containerName, action)
		case "cancel":
			text, kb := getDockerMenuText()
			editMessage(bot, chatID, msgID, text, kb)
		}
	case "confirm":
		if len(parts) < 4 {
			return
		}
		containerAction := parts[len(parts)-1]
		containerName := strings.Join(parts[2:len(parts)-1], "_")
		executeContainerAction(bot, chatID, msgID, containerName, containerAction)
	}
}

// showContainerActions shows actions for a specific container
func showContainerActions(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName string) {
	containers := getContainerList()
	var container *ContainerInfo
	for _, c := range containers {
		if c.Name == containerName {
			container = &c
			break
		}
	}

	if container == nil {
		editMessage(bot, chatID, msgID, "❓ Container not found", nil)
		return
	}

	var b strings.Builder
	icon := "⏸"
	statusText := "stopped"
	if container.Running {
		icon = "▶️"
		statusText = parseUptime(container.Status)
	}

	b.WriteString(fmt.Sprintf("%s *%s*\n\n", icon, container.Name))
	b.WriteString(fmt.Sprintf("Status: %s\n", statusText))
	b.WriteString(fmt.Sprintf("Image: `%s`\n", truncate(container.Image, 20)))
	b.WriteString(fmt.Sprintf("ID: `%s`\n", container.ID[:12]))

	if container.Running {
		stats := getContainerStats(containerName)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n%s", stats))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if container.Running {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("stop"), "container_stop_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(tr("restart"), "container_restart_"+containerName),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("kill"), "container_kill_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(tr("logs"), "container_logs_"+containerName),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("start"), "container_start_"+containerName),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(tr("back"), "show_docker"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

// confirmContainerAction asks for confirmation before container action
func confirmContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	if action == "logs" {
		showContainerLogs(bot, chatID, msgID, containerName)
		return
	}

	actionText := map[string]string{
		"start":   tr("start"),
		"stop":    tr("stop"),
		"restart": tr("restart"),
		"kill":    tr("kill"),
	}[action]

	text := fmt.Sprintf(tr("confirm_action"), actionText, containerName)
	if action == "kill" {
		text += tr("kill_warn")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(tr("yes"), fmt.Sprintf("container_confirm_%s_%s", containerName, action)),
			tgbotapi.NewInlineKeyboardButtonData(tr("no"), "container_cancel_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

// executeContainerAction executes a container action
func executeContainerAction(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName, action string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	editMessage(bot, chatID, msgID, fmt.Sprintf("... `%s` %s", containerName, action), nil)

	var cmd *exec.Cmd
	switch action {
	case "start":
		cmd = exec.CommandContext(ctx, "docker", "start", containerName)
	case "stop":
		cmd = exec.CommandContext(ctx, "docker", "stop", containerName)
	case "restart":
		cmd = exec.CommandContext(ctx, "docker", "restart", containerName)
	case "kill":
		cmd = exec.CommandContext(ctx, "docker", "kill", containerName)
	default:
		return
	}

	output, err := cmd.CombinedOutput()
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf("❌ Couldn't %s *%s*\n`%s`", action, containerName, errMsg)
		addReportEvent("warning", fmt.Sprintf("Error %s container %s: %s", action, containerName, errMsg))
	} else {
		actionPast := map[string]string{"start": "started ▶️", "stop": "stopped ⏹", "restart": "restarted 🔄", "kill": "killed 💀"}[action]
		resultText = fmt.Sprintf("✅ *%s* %s", containerName, actionPast)
		addReportEvent("info", fmt.Sprintf("Container %s: %s (manual)", containerName, action))
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Containers", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

// showContainerLogs shows container logs
func showContainerLogs(bot *tgbotapi.BotAPI, chatID int64, msgID int, containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "30", containerName)
	out, err := cmd.CombinedOutput()

	var text string
	if err != nil {
		text = fmt.Sprintf("Couldn't read logs: %v", err)
	} else {
		logs := string(out)
		if len(logs) > 3500 {
			logs = logs[len(logs)-3500:]
		}
		if logs == "" {
			logs = "(no logs available)"
		}
		text = fmt.Sprintf("*Logs for %s*\n```\n%s\n```", containerName, logs)
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "container_logs_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "container_select_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

// getContainerInfoText gets container info text
func getContainerInfoText(c ContainerInfo) (string, *tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	icon := "x"
	if c.Running {
		icon = ">"
	}
	b.WriteString(fmt.Sprintf("> *%s* %s\n", c.Name, icon))
	b.WriteString(fmt.Sprintf("> `%s`\n", c.Image))
	b.WriteString(fmt.Sprintf("> %s\n", c.Status))

	if c.Running {
		stats := getContainerStats(c.Name)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n%s", stats))
		}
	}

	return b.String(), nil
}

// handleContainerCommand handles the /container command
func handleContainerCommand(bot *tgbotapi.BotAPI, chatID int64, args string) {
	if args == "" {
		sendDockerMenu(bot, chatID)
		return
	}

	containers := getContainerList()
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			msg := tgbotapi.NewMessage(chatID, "")
			msg.ParseMode = "Markdown"
			text, _ := getContainerInfoText(c)
			msg.Text = text
			bot.Send(msg)
			return
		}
	}
	bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("x Container `%s` not found.", args)))
}

// handleKillCommand handles the /kill command
func handleKillCommand(bot *tgbotapi.BotAPI, chatID int64, args string) {
	if args == "" {
		sendMarkdown(bot, chatID, "Usage: `/kill container_name`\n\nThis will forcefully terminate the container (SIGKILL)")
		return
	}

	containers := getContainerList()
	var found *ContainerInfo
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			found = &c
			break
		}
	}

	if found == nil {
		sendMarkdown(bot, chatID, fmt.Sprintf("❓ Container `%s` not found", args))
		return
	}

	if !found.Running {
		sendMarkdown(bot, chatID, fmt.Sprintf("⏸ Container `%s` is not running", args))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "kill", args)
	output, err := cmd.CombinedOutput()

	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		sendMarkdown(bot, chatID, fmt.Sprintf("❌ Failed to kill `%s`:\n`%s`", args, errMsg))
		addReportEvent("warning", fmt.Sprintf("Kill failed: %s - %s", args, errMsg))
	} else {
		sendMarkdown(bot, chatID, fmt.Sprintf("💀 Container `%s` killed", args))
		addReportEvent("action", fmt.Sprintf("Container killed: %s", args))
	}
}

// askDockerRestartConfirmation asks for confirmation to restart Docker
func askDockerRestartConfirmation(bot *tgbotapi.BotAPI, chatID int64) {
	text := "🐳 *Restart Docker Service?*\n\n⚠️ This will restart the Docker daemon.\nAll containers will be temporarily stopped."

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Yes, restart", "confirm_restart_docker"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Cancel", "cancel_restart_docker"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	bot.Send(msg)
}

// executeDockerServiceRestart restarts the Docker service
func executeDockerServiceRestart(bot *tgbotapi.BotAPI, chatID int64, msgID int) {
	editMessage(bot, chatID, msgID, "🔄 Restarting Docker service...", nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var cmd *exec.Cmd
	if _, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.CommandContext(ctx, "systemctl", "restart", "docker")
	} else {
		cmd = exec.CommandContext(ctx, "service", "docker", "restart")
	}

	output, err := cmd.CombinedOutput()
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf("❌ Docker restart failed:\n`%s`", errMsg)
		addReportEvent("critical", fmt.Sprintf("Docker restart failed: %s", errMsg))
	} else {
		resultText = "✅ Docker service restarted successfully"
		addReportEvent("action", "Docker service restarted (manual)")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Check Containers", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

// checkContainerStates monitors for unexpected container stops
func checkContainerStates(bot *tgbotapi.BotAPI) {
	containers := getContainerList()
	if containers == nil {
		return
	}

	containerStateMutex.Lock()
	defer containerStateMutex.Unlock()

	currentStates := make(map[string]bool)
	for _, c := range containers {
		currentStates[c.Name] = c.Running
	}

	for name, wasRunning := range lastContainerStates {
		isRunning, exists := currentStates[name]
		if exists && wasRunning && !isRunning {
			if !isQuietHours() {
				msg := fmt.Sprintf("⚠️ *Container Stopped*\n\n`%s` has stopped unexpectedly.", name)
				m := tgbotapi.NewMessage(AllowedUserID, msg)
				m.ParseMode = "Markdown"
				bot.Send(m)
			}
			addReportEvent("warning", fmt.Sprintf("Container stopped: %s", name))
		}
	}

	lastContainerStates = currentStates
}

// handleCriticalRAM handles critical RAM situations
func handleCriticalRAM(bot *tgbotapi.BotAPI, s Stats) {
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{.MemPerc}}", c.Name)
		out, _ := cmd.Output()
		cancel()

		memStr := strings.TrimSuffix(strings.TrimSpace(string(out)), "%")
		if memPct, err := strconv.ParseFloat(memStr, 64); err == nil && memPct > 20 {
			heavyContainers = append(heavyContainers, containerMem{c.Name, memPct})
		}
	}

	ramThreshold := cfg.Docker.AutoRestartOnRAMCritical.RAMThreshold
	if s.RAM >= ramThreshold && len(heavyContainers) > 0 {
		sort.Slice(heavyContainers, func(i, j int) bool {
			return heavyContainers[i].memPct > heavyContainers[j].memPct
		})

		target := heavyContainers[0]
		if canAutoRestart(target.name) {
			log.Printf("> RAM critical (%.1f%%), auto-restart: %s (%.1f%%)", s.RAM, target.name, target.memPct)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "docker", "restart", target.name)
			err := cmd.Run()
			cancel()

			recordAutoRestart(target.name)

			var msgText string
			if err != nil {
				msgText = fmt.Sprintf("❌ *Auto-restart failed*\n\n"+
					"RAM critical: `%.1f%%`\n"+
					"Container: `%s`\n"+
					"Error: %v", s.RAM, target.name, err)
				addReportEvent("critical", fmt.Sprintf("Auto-restart failed: %s (%v)", target.name, err))
			} else {
				msgText = fmt.Sprintf("🔄 *Auto-restart done*\n\n"+
					"RAM was critical: `%.1f%%`\n"+
					"Restarted: `%s` (`%.1f%%` mem)\n\n"+
					"_Watching..._", s.RAM, target.name, target.memPct)
				addReportEvent("action", fmt.Sprintf("Auto-restart: %s (RAM %.1f%%)", target.name, s.RAM))
			}

			if !isQuietHours() {
				msg := tgbotapi.NewMessage(AllowedUserID, msgText)
				msg.ParseMode = "Markdown"
				bot.Send(msg)
			}
		}
	}
}

// canAutoRestart checks if container can be auto-restarted
func canAutoRestart(containerName string) bool {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	restarts := autoRestarts[containerName]
	cutoff := time.Now().Add(-1 * time.Hour)

	count := 0
	for _, t := range restarts {
		if t.After(cutoff) {
			count++
		}
	}

	maxRestarts := cfg.Docker.AutoRestartOnRAMCritical.MaxRestartsPerHour
	if maxRestarts <= 0 {
		maxRestarts = 3
	}

	return count < maxRestarts
}

// recordAutoRestart records an auto-restart
func recordAutoRestart(containerName string) {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	autoRestarts[containerName] = append(autoRestarts[containerName], time.Now())
	saveState()
}

// cleanRestartCounter cleans old restart records
func cleanRestartCounter() {
	autoRestartsMutex.Lock()
	defer autoRestartsMutex.Unlock()

	cutoff := time.Now().Add(-2 * time.Hour)
	for name, times := range autoRestarts {
		var newTimes []time.Time
		for _, t := range times {
			if t.After(cutoff) {
				newTimes = append(newTimes, t)
			}
		}
		if len(newTimes) == 0 {
			delete(autoRestarts, name)
		} else {
			autoRestarts[name] = newTimes
		}
	}
}
