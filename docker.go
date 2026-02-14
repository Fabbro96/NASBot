package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// dockerContainerRaw maps the raw JSON output from docker CLI
type dockerContainerRaw struct {
	Names  string `json:"Names"`
	Status string `json:"Status"`
	State  string `json:"State"` // running, exited, etc.
	Image  string `json:"Image"`
	ID     string `json:"ID"`
}

// getContainerList gets list of all Docker containers
func getContainerList() []ContainerInfo {
	list, _ := getContainerListWithError()
	return list
}

// getContainerListWithError gets list of all Docker containers and returns error
// Uses JSON formatting for robust parsing
func getContainerListWithError() ([]ContainerInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Use --format json for clear object parsing.
	// We use {{json .}} to get a JSON object per line.
	out, err := runCommandOutput(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	if err != nil {
		slog.Error("Docker error", "err", err, "output", string(out))
		return nil, err
	}

	return parseDockerJSON(string(out))
}

// parseDockerJSON parses the raw output from docker ps --format "{{json .}}"
// Extracted for testability
func parseDockerJSON(output string) ([]ContainerInfo, error) {
	var containers []ContainerInfo
	lines := strings.Split(strings.TrimSpace(output), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var raw dockerContainerRaw
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			slog.Warn("Failed to unmarshal docker line", "line", line, "err", err)
			continue
		}

		containers = append(containers, ContainerInfo{
			Name:    raw.Names,
			Status:  raw.Status,
			Image:   raw.Image,
			ID:      raw.ID,
			Running: strings.ToLower(raw.State) == "running",
		})
	}
	return containers, nil
}

// sendDockerMenu sends the Docker container menu
func sendDockerMenu(ctx *AppContext, bot BotAPI, chatID int64) {
	text, kb := getDockerMenuText(ctx)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if kb != nil {
		msg.ReplyMarkup = kb
	}
	safeSend(bot, msg)
}

// getDockerMenuText generates the Docker menu text and keyboard
func getDockerMenuText(ctx *AppContext) (string, *tgbotapi.InlineKeyboardMarkup) {
	containers := getContainerList()
	if len(containers) == 0 {
		mainKb := getMainKeyboard(ctx)
		return "_No containers found. Is Docker running?_", &mainKb
	}

	var b strings.Builder
	b.WriteString(ctx.Tr("docker_title"))

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

	b.WriteString(fmt.Sprintf(ctx.Tr("docker_running"), running, stopped))

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
		tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("docker_menu_restart_all"), "docker_restart_all"),
		tgbotapi.NewInlineKeyboardButtonData("🐳 "+ctx.Tr("docker_menu_restart_service"), "docker_restart_service"),
	))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("docker_menu_refresh"), "show_docker"),
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("docker_menu_home"), "back_main"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	return b.String(), &kb
}

// getDockerStatsText returns container resource usage stats
func getDockerStatsText(_ *AppContext) string {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runCommandStdout(timeoutCtx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}")
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
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

	out, err := runCommandStdout(ctx, "docker", "stats", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}", containerName)
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
func handleContainerCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	parts := strings.Split(data, "_")
	if len(parts) < 3 {
		return
	}

	action := parts[1]

	switch action {
	case "select", "start", "stop", "restart", "logs", "kill", "cancel", "ailog":
		containerName := strings.Join(parts[2:], "_")
		switch action {
		case "select":
			showContainerActions(ctx, bot, chatID, msgID, containerName)
		case "start", "stop", "restart", "logs", "kill":
			confirmContainerAction(ctx, bot, chatID, msgID, containerName, action)
		case "ailog":
			showContainerAIAnalysis(ctx, bot, chatID, msgID, containerName)
		case "cancel":
			text, kb := getDockerMenuText(ctx)
			editMessage(bot, chatID, msgID, text, kb)
		}
	case "confirm":
		if len(parts) < 4 {
			return
		}
		containerAction := parts[len(parts)-1]
		containerName := strings.Join(parts[2:len(parts)-1], "_")
		executeContainerAction(ctx, bot, chatID, msgID, containerName, containerAction)
	}
}

// showContainerActions shows actions for a specific container
func showContainerActions(ctx *AppContext, bot BotAPI, chatID int64, msgID int, containerName string) {
	containers := getContainerList()
	var container *ContainerInfo
	for _, c := range containers {
		if c.Name == containerName {
			container = &c
			break
		}
	}

	if container == nil {
		editMessage(bot, chatID, msgID, ctx.Tr("docker_not_found"), nil)
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
	b.WriteString(fmt.Sprintf(ctx.Tr("docker_status"), statusText))
	b.WriteString(fmt.Sprintf(ctx.Tr("docker_image"), truncate(container.Image, 20)))
	b.WriteString(fmt.Sprintf(ctx.Tr("docker_id"), container.ID[:12]))

	if container.Running {
		stats := getContainerStats(containerName)
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n%s", stats))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if container.Running {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("stop"), "container_stop_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("restart"), "container_restart_"+containerName),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("kill"), "container_kill_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("logs"), "container_logs_"+containerName),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("start"), "container_start_"+containerName),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "show_docker"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

// confirmContainerAction asks for confirmation before container action
func confirmContainerAction(ctx *AppContext, bot BotAPI, chatID int64, msgID int, containerName, action string) {
	if action == "logs" {
		showContainerLogs(ctx, bot, chatID, msgID, containerName)
		return
	}

	actionText := map[string]string{
		"start":   ctx.Tr("start"),
		"stop":    ctx.Tr("stop"),
		"restart": ctx.Tr("restart"),
		"kill":    ctx.Tr("kill"),
	}[action]

	text := fmt.Sprintf(ctx.Tr("confirm_action"), actionText, containerName)
	if action == "kill" {
		text += ctx.Tr("kill_warn")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("yes"), fmt.Sprintf("container_confirm_%s_%s", containerName, action)),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("no"), "container_cancel_"+containerName),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

// executeContainerAction executes a container action
func executeContainerAction(ctx *AppContext, bot BotAPI, chatID int64, msgID int, containerName, action string) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	editMessage(bot, chatID, msgID, fmt.Sprintf("... `%s` %s", containerName, action), nil)

	var output []byte
	var err error
	switch action {
	case "start":
		output, err = runCommandOutput(timeoutCtx, "docker", "start", containerName)
	case "stop":
		output, err = runCommandOutput(timeoutCtx, "docker", "stop", containerName)
	case "restart":
		output, err = runCommandOutput(timeoutCtx, "docker", "restart", containerName)
	case "kill":
		output, err = runCommandOutput(timeoutCtx, "docker", "kill", containerName)
	default:
		return
	}
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf(ctx.Tr("docker_action_err"), action, containerName, errMsg)
		ctx.State.AddReportEvent("warning", fmt.Sprintf("Error %s container %s: %s", action, containerName, errMsg))
	} else {
		actionPast := map[string]string{
			"start":   ctx.Tr("docker_started"),
			"stop":    ctx.Tr("docker_stopped"),
			"restart": ctx.Tr("docker_restarted"),
			"kill":    ctx.Tr("docker_killed"),
		}[action]
		resultText = fmt.Sprintf(ctx.Tr("docker_action_ok"), containerName, actionPast)
		ctx.State.AddReportEvent("info", fmt.Sprintf("Container %s: %s (manual)", containerName, action))
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
func showContainerLogs(ctx *AppContext, bot BotAPI, chatID int64, msgID int, containerName string) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCommandOutput(timeoutCtx, "docker", "logs", "--tail", "30", containerName)

	var text string
	if err != nil {
		text = fmt.Sprintf(ctx.Tr("docker_logs_err"), err)
	} else {
		logs := string(out)
		if len(logs) > 3500 {
			logs = logs[len(logs)-3500:]
		}
		if logs == "" {
			logs = ctx.Tr("docker_logs_empty")
		}
		text = fmt.Sprintf(ctx.Tr("docker_logs_title")+"```\n%s\n```", containerName, logs)
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 Refresh", "container_logs_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Back", "container_select_"+containerName),
		),
	}
	// Add AI analysis button if Gemini is configured
	if ctx.Config.GeminiAPIKey != "" {
		rows = append([][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("docker_ai_analyze"), "container_ailog_"+containerName),
			),
		}, rows...)
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, text, &kb)
}

// showContainerAIAnalysis sends container logs to Gemini for AI analysis
func showContainerAIAnalysis(ctx *AppContext, bot BotAPI, chatID int64, msgID int, containerName string) {
	if ctx.Config.GeminiAPIKey == "" {
		editMessage(bot, chatID, msgID, "❌ "+ctx.Tr("health_no_gemini"), nil)
		return
	}

	// Show loading
	modelName := "gemini-2.5-flash"
	loadingText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("docker_ai_analyzing"), modelName)
	edit := tgbotapi.NewEditMessageText(chatID, msgID, loadingText)
	edit.ParseMode = "Markdown"
	safeSend(bot, edit)

	// Get container logs (last 100 lines for more context)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := runCommandOutput(timeoutCtx, "docker", "logs", "--tail", "100", containerName)
	if err != nil {
		errText := fmt.Sprintf("❌ %s: %v", ctx.Tr("docker_logs_err_short"), err)
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "container_logs_"+containerName),
			),
		)
		editMessage(bot, chatID, msgID, errText, &kb)
		return
	}

	logs := string(out)
	if strings.TrimSpace(logs) == "" {
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "container_logs_"+containerName),
			),
		)
		editMessage(bot, chatID, msgID, "📭 "+ctx.Tr("docker_logs_empty"), &kb)
		return
	}

	// Truncate logs if too long for prompt
	if len(logs) > 6000 {
		logs = logs[len(logs)-6000:]
	}

	prompt := fmt.Sprintf(ctx.Tr("docker_ai_prompt"), containerName, logs)

	analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
		newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("docker_ai_analyzing"), model)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, newText)
		edit.ParseMode = "Markdown"
		safeSend(bot, edit)
	})

	if err != nil {
		slog.Error("Docker AI log analysis error", "container", containerName, "err", err)
		errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("docker_ai_error"), err)
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("docker_ai_analyze"), "container_ailog_"+containerName),
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "container_logs_"+containerName),
			),
		)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, errText)
		edit.ParseMode = "Markdown"
		edit.ReplyMarkup = &kb
		if _, sendErr := bot.Send(edit); sendErr != nil {
			edit.ParseMode = ""
			safeSend(bot, edit)
		}
		return
	}

	result := fmt.Sprintf("🤖 *%s — %s*\n\n%s", ctx.Tr("docker_ai_title"), containerName, analysis)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📜 "+ctx.Tr("logs"), "container_logs_"+containerName),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), "container_select_"+containerName),
		),
	)
	finalEdit := tgbotapi.NewEditMessageText(chatID, msgID, result)
	finalEdit.ParseMode = "Markdown"
	finalEdit.ReplyMarkup = &kb
	if _, sendErr := bot.Send(finalEdit); sendErr != nil {
		slog.Error("Error sending Docker AI analysis (Markdown)", "err", sendErr)
		finalEdit.ParseMode = ""
		safeSend(bot, finalEdit)
	}
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
func handleContainerCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
	if args == "" {
		sendDockerMenu(ctx, bot, chatID)
		return
	}

	containers := getContainerList()
	for _, c := range containers {
		if strings.EqualFold(c.Name, args) {
			msg := tgbotapi.NewMessage(chatID, "")
			msg.ParseMode = "Markdown"
			text, _ := getContainerInfoText(c)
			msg.Text = text
			safeSend(bot, msg)
			return
		}
	}
	safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf("x Container `%s` not found.", args)))
}

// handleKillCommand handles the /kill command
func handleKillCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
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

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := runCommandOutput(timeoutCtx, "docker", "kill", args)

	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		sendMarkdown(bot, chatID, fmt.Sprintf("❌ Failed to kill `%s`:\n`%s`", args, errMsg))
		ctx.State.AddReportEvent("warning", fmt.Sprintf("Kill failed: %s - %s", args, errMsg))
	} else {
		sendMarkdown(bot, chatID, fmt.Sprintf("💀 Container `%s` killed", args))
		ctx.State.AddReportEvent("action", fmt.Sprintf("Container killed: %s", args))
	}
}

// askDockerRestartConfirmation asks for confirmation to restart Docker (new message)
func askDockerRestartConfirmation(ctx *AppContext, bot BotAPI, chatID int64) {
	text := fmt.Sprintf("🐳 *%s*\n\n⚠️ %s", ctx.Tr("docker_restart_service_title"), ctx.Tr("docker_restart_service_warn"))

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ "+ctx.Tr("yes"), "confirm_restart_docker"),
			tgbotapi.NewInlineKeyboardButtonData("❌ "+ctx.Tr("no"), "cancel_restart_docker"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	safeSend(bot, msg)
}

// askDockerRestartConfirmationEdit asks for confirmation to restart Docker (edit existing message)
func askDockerRestartConfirmationEdit(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	text := fmt.Sprintf("🐳 *%s*\n\n⚠️ %s", ctx.Tr("docker_restart_service_title"), ctx.Tr("docker_restart_service_warn"))

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ "+ctx.Tr("yes"), "confirm_restart_docker"),
			tgbotapi.NewInlineKeyboardButtonData("❌ "+ctx.Tr("no"), "cancel_restart_docker"),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

// askRestartAllContainersConfirmation asks for confirmation to restart all containers
func askRestartAllContainersConfirmation(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	containers := getContainerList()
	running := 0
	for _, c := range containers {
		if c.Running {
			running++
		}
	}

	text := fmt.Sprintf("🔄 *%s*\n\n⚠️ %s\n\n📦 %s: *%d*",
		ctx.Tr("docker_restart_all_title"),
		ctx.Tr("docker_restart_all_warn"),
		ctx.Tr("docker_restart_all_count"),
		running)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ "+ctx.Tr("yes"), "confirm_restart_all"),
			tgbotapi.NewInlineKeyboardButtonData("❌ "+ctx.Tr("no"), "cancel_restart_all"),
		),
	)
	editMessage(bot, chatID, msgID, text, &kb)
}

// executeRestartAllContainers restarts all running containers
func executeRestartAllContainers(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	containers := getContainerList()
	var running []string
	for _, c := range containers {
		if c.Running {
			running = append(running, c.Name)
		}
	}

	if len(running) == 0 {
		editMessage(bot, chatID, msgID, "📭 "+ctx.Tr("docker_restart_all_none"), nil)
		return
	}

	editMessage(bot, chatID, msgID, fmt.Sprintf("🔄 %s (%d)...", ctx.Tr("docker_restart_all_running"), len(running)), nil)

	var succeeded, failed []string
	for _, name := range running {
		timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := runCommand(timeoutCtx, "docker", "restart", name)
		cancel()

		if err != nil {
			slog.Error("Failed to restart container", "container", name, "err", err)
			failed = append(failed, name)
		} else {
			succeeded = append(succeeded, name)
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🔄 *%s*\n\n", ctx.Tr("docker_restart_all_result")))

	if len(succeeded) > 0 {
		b.WriteString(fmt.Sprintf("✅ %s: *%d*\n", ctx.Tr("docker_restart_all_ok"), len(succeeded)))
		for _, name := range succeeded {
			b.WriteString(fmt.Sprintf("  • `%s`\n", name))
		}
	}
	if len(failed) > 0 {
		b.WriteString(fmt.Sprintf("\n❌ %s: *%d*\n", ctx.Tr("docker_restart_all_fail"), len(failed)))
		for _, name := range failed {
			b.WriteString(fmt.Sprintf("  • `%s`\n", name))
		}
	}

	ctx.State.AddReportEvent("action", fmt.Sprintf("Restart all containers: %d ok, %d failed", len(succeeded), len(failed)))

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Containers", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

// executeDockerServiceRestart restarts the Docker service
func executeDockerServiceRestart(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	editMessage(bot, chatID, msgID, "🔄 Restarting Docker service...", nil)

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var output []byte
	var err error
	if commandExists("systemctl") {
		output, err = runCommandOutput(timeoutCtx, "systemctl", "restart", "docker")
	} else {
		output, err = runCommandOutput(timeoutCtx, "service", "docker", "restart")
	}
	var resultText string
	if err != nil {
		errMsg := strings.TrimSpace(string(output))
		if errMsg == "" {
			errMsg = err.Error()
		}
		resultText = fmt.Sprintf("❌ Docker restart failed:\n`%s`", errMsg)
		ctx.State.AddReportEvent("critical", fmt.Sprintf("Docker restart failed: %s", errMsg))
	} else {
		resultText = "✅ Docker service restarted successfully"
		ctx.State.AddReportEvent("action", "Docker service restarted (manual)")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 Check Containers", "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 Home", "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

