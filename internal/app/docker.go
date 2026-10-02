package app

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"nasbot/pkg/model"

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

// dockerErrLogInterval throttles repeated "cannot list containers" errors.
//
// The listing is retried by the watchdog tick (10s), by every Docker menu, by
// the report and by the critical-container check: an unreachable daemon used to
// produce two log lines every 10 seconds, roughly 17k lines a day, all
// identical. Consecutive identical errors are now collapsed into one line per
// interval; a change in the message always gets through.
const dockerErrLogInterval = 5 * time.Minute

var (
	dockerErrLogMu   model.Mutex
	dockerErrLastLog time.Time
	dockerErrLastMsg string
)

// logDockerListError reports a failed container listing, throttled as described
// on dockerErrLogInterval.
func logDockerListError(err error, output string) {
	msg := err.Error()

	dockerErrLogMu.Lock()
	recent := !dockerErrLastLog.IsZero() && time.Since(dockerErrLastLog) < dockerErrLogInterval
	repeated := msg == dockerErrLastMsg
	if !recent || !repeated {
		dockerErrLastLog = time.Now()
		dockerErrLastMsg = msg
	}
	dockerErrLogMu.Unlock()

	if recent && repeated {
		return
	}
	slog.Error("Docker error", "err", sanitizeErr(err), "output", output)
}

// getContainerList gets list of all Docker containers.
//
// Contract for the caller (getCachedContainerList in monitors_runtime.go): a
// failure returns a nil slice, never an empty one, and the cache must not be
// re-stamped in that case. Serving a nil list under a fresh LastUpdate turns
// "Docker is unreachable" into "there are no containers" for the whole TTL,
// which is what the Docker watchdog and the menu then report to the user.
//
// This wrapper cannot propagate the error without changing the signature its
// caller (another lane) uses; getContainerListWithError is the error-returning
// entry point and is what new code should call.
func getContainerList() []ContainerInfo {
	list, err := getContainerListWithError()
	if err != nil {
		return nil
	}
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
		logDockerListError(err, string(out))
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
			slog.Warn("Failed to unmarshal docker line", "line", line, "err", sanitizeErr(err))
			continue
		}

		// A missing field yields the zero value rather than dropping the row:
		// the contract of this parser is "one ContainerInfo per parsable line",
		// and docker_parsing_test.go pins it. A nameless row is inert downstream
		// (its token matches nothing, so it can never be acted on).
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

// --- short, stable identifiers for callback payloads -------------------------
//
// Telegram rejects a whole inline keyboard with BUTTON_DATA_INVALID as soon as
// one callback_data exceeds 64 bytes, and the previous payloads embedded the raw
// container name: Compose derives names from the project directory, so a name
// longer than ~46 characters killed the entire Docker menu and /docker never
// updated again. Truncating the *label* was irrelevant, the data is what counts.
//
// A payload therefore carries a token instead of a name. The token is derived
// from data that is already in the listing, so it needs no side table, no TTL and
// no lock, and it survives a bot restart: old keyboards keep resolving to the
// container they were rendered for.
const (
	// dockerTokenIDPrefix marks a token built from the container ID.
	dockerTokenIDPrefix = "i"
	// dockerTokenNamePrefix marks a token built from a hash of the name, used
	// when a listing carries no usable ID.
	dockerTokenNamePrefix = "n"
	// dockerTokenIDLen is how much of the 64-hex-char Docker ID is kept. 12 hex
	// chars is what `docker ps` itself shows and leaves 48 bits of entropy.
	//
	// A token is at most 13 bytes whatever the input is: a longer ID is cut, and
	// an ID too short to be usable falls back to the fixed-width name hash.
	dockerTokenIDLen = 12
)

// dockerContainerRef is a container as a rendered menu refers to it: the real
// name for the CLI and for display, the short token for callback payloads.
type dockerContainerRef struct {
	name  string
	token string
}

// newDockerContainerRef builds the reference for a container. The token is a
// pure function of the container, so resolving a token and re-deriving it is
// idempotent: a menu rendered earlier keeps the same token after a refresh.
func newDockerContainerRef(c *ContainerInfo) dockerContainerRef {
	if c == nil {
		return dockerContainerRef{}
	}
	return dockerContainerRef{name: c.Name, token: dockerContainerToken(*c)}
}

// dockerContainerToken returns the short identifier used in callback payloads
// for a container. Never longer than 13 bytes.
func dockerContainerToken(c ContainerInfo) string {
	if id := strings.TrimSpace(c.ID); len(id) >= 6 {
		if len(id) > dockerTokenIDLen {
			id = id[:dockerTokenIDLen]
		}
		return dockerTokenIDPrefix + strings.ToLower(id)
	}
	return dockerTokenNamePrefix + dockerNameToken(c.Name)
}

// dockerNameToken hashes a container name into a fixed-width token, for the
// listings that carry no ID.
func dockerNameToken(name string) string {
	h := fnv.New32a()
	// FNV-1a never fails on writes to the hash state.
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%08x", h.Sum32())
}

// containerCallbackData builds the payload of a simple Docker container button:
// "container_<action>_<token>". The token is bounded to 13 bytes, so the payload
// is at most 10+7+1+13 = 31 bytes, inside Telegram's 64-byte limit whatever the
// container is called.
func containerCallbackData(action string, ref dockerContainerRef) string {
	return "container_" + action + "_" + ref.token
}

// containerConfirmData builds the payload of a confirmation button. The action
// is appended last so the parser can take the token from between the markers:
// "container_confirm_<token>_<action>", 18+13+8 = 39 bytes worst case.
func containerConfirmData(ref dockerContainerRef, action string) string {
	return "container_confirm_" + ref.token + "_" + action
}

// resolveDockerContainer maps a callback payload back to the container it names.
//
// Resolution goes against the *current* listing, which is what makes a stale
// menu safe: a token that no longer matches anything reports not-found instead
// of silently hitting whatever now occupies that slot.
//
// A payload that resolves as no token is retried as a container name, so
// keyboards built by an older release — where the payload was the name — keep
// working. The name check runs unconditionally, because container names
// routinely start with "i" or "n" ("nginx") and would otherwise be mistaken for
// a malformed token.
func resolveDockerContainer(ctx *AppContext, token string) (*ContainerInfo, bool) {
	if token == "" {
		return nil, false
	}

	containers := getCachedContainerList(ctx)

	if rest, ok := strings.CutPrefix(token, dockerTokenIDPrefix); ok {
		if c := matchDockerContainerID(containers, rest); c != nil {
			return c, true
		}
	}

	if rest, ok := strings.CutPrefix(token, dockerTokenNamePrefix); ok {
		for i := range containers {
			if dockerNameToken(containers[i].Name) == rest {
				return &containers[i], true
			}
		}
	}

	for i := range containers {
		if strings.EqualFold(containers[i].Name, token) {
			return &containers[i], true
		}
	}
	return nil, false
}

// matchDockerContainerID resolves an ID token, i.e. the hex prefix docker ps
// showed. The comparison is a case-insensitive prefix match so a shortened ID
// still resolves.
func matchDockerContainerID(containers []ContainerInfo, idPrefix string) *ContainerInfo {
	if idPrefix == "" {
		return nil
	}
	for i := range containers {
		id := containers[i].ID
		if len(id) >= len(idPrefix) && strings.EqualFold(id[:len(idPrefix)], idPrefix) {
			return &containers[i]
		}
	}
	return nil
}

// resolveDockerRef resolves a callback payload to a usable reference. A payload
// that is not a token (a legacy name) gets a token minted here, so the next
// keyboard in the chain stays short too.
func resolveDockerRef(ctx *AppContext, token string) (dockerContainerRef, bool) {
	c, ok := resolveDockerContainer(ctx, token)
	if !ok {
		return dockerContainerRef{}, false
	}
	return newDockerContainerRef(c), true
}

// getDockerMenuText generates the Docker menu text and keyboard
func getDockerMenuText(ctx *AppContext) (string, *tgbotapi.InlineKeyboardMarkup) {
	containers := getCachedContainerList(ctx)
	if len(containers) == 0 {
		mainKb := getMainKeyboard(ctx)
		return ctx.Tr("docker_no_containers"), &mainKb
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
		fmt.Fprintf(&b, "%s *%s* — %s\n", icon, c.Name, statusText)
	}

	fmt.Fprintf(&b, ctx.Tr("docker_running"), running, stopped)

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := 0; i < len(containers); i += 2 {
		var row []tgbotapi.InlineKeyboardButton
		for j := 0; j < 2 && i+j < len(containers); j++ {
			c := &containers[i+j]
			icon := "⏸"
			if c.Running {
				icon = "▶"
			}
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("%s %s", icon, truncate(c.Name, 10)),
				containerCallbackData("select", newDockerContainerRef(c)),
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

// --- parsing of `docker stats` output ----------------------------------------
//
// The `--format` string is ours, but the *output* is not: Docker changes it
// between versions, prints "--" for values it cannot compute (a container that
// just started, a cgroup it cannot read), pads with spaces and may emit a name
// containing spaces. Every parser below therefore returns an explicit ok flag
// and never guesses: a short row, a blank field or an unparsable number is
// reported as "not a usable row", never rendered as a zero.

// dockerMemUnits lists the memory suffixes `docker stats` can emit, longest
// first so that "GiB" is never mistaken for "B".
var dockerMemUnits = []string{"TiB", "GiB", "MiB", "KiB", "TB", "GB", "MB", "KB", "B"}

// dockerMemUnitShort maps each unit to the one-letter form used in the table.
// Plain "B" shortens to nothing: "0B" is already as short as it gets.
var dockerMemUnitShort = map[string]string{
	"TiB": "T", "GiB": "G", "MiB": "M", "KiB": "K",
	"TB": "T", "GB": "G", "MB": "M", "KB": "K", "B": "",
}

// splitNonEmptyLines splits command output into non-blank, trimmed lines.
func splitNonEmptyLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// splitStatsFields splits one `docker stats` row into the first want
// pipe-separated fields, each trimmed. It reports ok=false when the row is
// blank or carries fewer fields than the format string asked for: a missing
// field means the row is unusable, and the caller must skip it rather than read
// past the end of the slice.
//
// Names may contain spaces but not pipes, so "|" is a safe separator and a
// multi-space run inside a value survives untouched.
func splitStatsFields(line string, want int) ([]string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || want <= 0 {
		return nil, false
	}
	parts := strings.Split(line, "|")
	if len(parts) < want {
		return nil, false
	}
	fields := make([]string, want)
	for i := 0; i < want; i++ {
		fields[i] = strings.TrimSpace(parts[i])
	}
	return fields, true
}

// parsePercentValue converts a percentage field ("12.34%", "12.34", "--") to a
// number. ok is false for anything Docker could not fill in, and for a
// non-finite value: those are neither 0% nor comparable, so they must never take
// part in a threshold decision.
func parsePercentValue(field string) (float64, bool) {
	s := strings.TrimSuffix(strings.TrimSpace(field), "%")
	if s == "" || s == "--" || s == "-" || s == "N/A" || s == "n/a" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	if v != v || v > 1e18 || v < -1e18 { // NaN or absurd magnitude
		return 0, false
	}
	return v, true
}

// shortenDockerMemUsage compacts the "used / limit" pair of `docker stats` down
// to the used half with a compact unit ("1.2G"). An empty or unrecognised value
// is returned trimmed and unchanged instead of being mangled.
func shortenDockerMemUsage(raw string) string {
	value, unit := splitMemToken(strings.TrimSpace(raw))
	return value + dockerMemUnitShort[unit]
}

// splitMemToken extracts the "used" half of a memory value and its unit.
//
// Docker writes the value and the unit together ("1.234GiB") but the unit may
// be separated by a space ("1.5 TiB"), and a full cell is "used / limit". The
// limit half is dropped, the unit is only recognised when it is a real Docker
// unit: an unknown trailing word is left as the value rather than guessed at.
func splitMemToken(raw string) (value, unit string) {
	// The used half comes first; the "/ limit" half is dropped, and a cell with
	// no used value stays empty rather than falling back to the limit.
	if used, _, found := strings.Cut(raw, "/"); found {
		raw = used
	}

	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return "", ""
	}

	// A bare unit as a later field means the previous one is the value: this
	// tolerates both "1.5TiB" and "1.5 TiB".
	for i, f := range fields {
		if _, isUnit := dockerMemUnitShort[f]; isUnit && i > 0 {
			return fields[i-1], f
		}
	}
	return fields[0], unitOf(fields[0])
}

// unitOf returns the Docker memory unit a value ends with, or "".
func unitOf(value string) string {
	for _, unit := range dockerMemUnits {
		if strings.HasSuffix(value, unit) && len(value) > len(unit) {
			return unit
		}
	}
	return ""
}

// dockerStatsRow is one row of the all-containers stats table.
type dockerStatsRow struct {
	Name     string
	CPUPerc  string
	MemUsage string
	MemPerc  string
}

// parseDockerStatsRow parses "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}".
// ok is false for a blank row and for a row with fewer than the four fields the
// format string requests.
func parseDockerStatsRow(line string) (dockerStatsRow, bool) {
	fields, ok := splitStatsFields(line, 4)
	if !ok || fields[0] == "" {
		return dockerStatsRow{}, false
	}
	return dockerStatsRow{
		Name:     fields[0],
		CPUPerc:  fields[1],
		MemUsage: fields[2],
		MemPerc:  fields[3],
	}, true
}

// containerStatsRow is one row of the single-container stats query.
type containerStatsRow struct {
	CPUPerc  string
	MemUsage string
	MemPerc  string
	NetIO    string
}

// parseContainerStatsRow parses
// "{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}|{{.NetIO}}", the layout used for the
// detail view of a single container. ok is false for a blank row and for a row
// with fewer than the four fields the format string requests.
func parseContainerStatsRow(line string) (containerStatsRow, bool) {
	fields, ok := splitStatsFields(line, 4)
	if !ok {
		return containerStatsRow{}, false
	}
	return containerStatsRow{
		CPUPerc:  fields[0],
		MemUsage: fields[1],
		MemPerc:  fields[2],
		NetIO:    fields[3],
	}, true
}

// parseDockerMemPercent parses "{{.Name}}|{{.MemPerc}}" and returns the
// container name with its memory share. ok is false for a blank row, a row
// without both fields, a blank name, and any percentage Docker reported as
// unavailable or unparsable.
func parseDockerMemPercent(line string) (string, float64, bool) {
	fields, ok := splitStatsFields(line, 2)
	if !ok || fields[0] == "" {
		return "", 0, false
	}
	pct, ok := parsePercentValue(fields[1])
	if !ok {
		return "", 0, false
	}
	return fields[0], pct, true
}

// getDockerStatsText returns container resource usage stats
func getDockerStatsText(ctx *AppContext) string {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := runCommandStdout(timeoutCtx, "docker", "stats", "--no-stream", "--format", "{{.Name}}|{{.CPUPerc}}|{{.MemUsage}}|{{.MemPerc}}")
	if err != nil {
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return "*timeout*"
		}
		return "*stats n/a*"
	}

	var b strings.Builder
	b.WriteString("📊 *" + ctx.Tr("docker_stats_title") + "*\n```\n")
	fmt.Fprintf(&b, "%-12s %5s %5s %s\n", "NAME", "CPU", "MEM%", "MEM")
	b.WriteString("─────────────────────────────\n")

	rows := 0
	for _, line := range splitNonEmptyLines(string(out)) {
		row, ok := parseDockerStatsRow(line)
		if !ok {
			// Short or malformed row: skip it instead of reading past the end of
			// the split, and do not pretend it was a container with 0%.
			slog.Debug("Skipping unparsable docker stats row", "row", line)
			continue
		}
		rows++
		fmt.Fprintf(&b, "%-12s %5s %5s %s\n",
			truncate(row.Name, 12), row.CPUPerc, row.MemPerc, shortenDockerMemUsage(row.MemUsage))
	}

	if rows == 0 {
		// Either Docker listed nothing, or it printed a format this build does
		// not understand. Both are "nothing to show", not an empty table.
		return ctx.Tr("docker_stats_none")
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

	// Only the first row describes the container asked for; `docker stats` with
	// an argument still emits one line per running container on some versions.
	for _, line := range splitNonEmptyLines(string(out)) {
		row, ok := parseContainerStatsRow(line)
		if !ok {
			return ""
		}
		return fmt.Sprintf("   CPU: `%s` │ RAM: `%s` (`%s`)\n   Net: `%s`",
			row.CPUPerc, row.MemUsage, row.MemPerc, row.NetIO)
	}
	return ""
}

// handleContainerCallback handles container-related callbacks
func handleContainerCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	parts := strings.Split(data, "_")
	if len(parts) < 3 {
		return
	}

	action := parts[1]

	// Cancel only re-renders the menu: it must keep working even if the
	// container it referred to is gone, so it is handled before resolving.
	if action == "cancel" {
		text, kb := getDockerMenuText(ctx)
		editMessage(bot, chatID, msgID, text, kb)
		return
	}

	switch action {
	case "select", "start", "stop", "restart", "logs", "kill", "ailog":
		ref, ok := resolveDockerRef(ctx, strings.Join(parts[2:], "_"))
		if !ok {
			editMessage(bot, chatID, msgID, ctx.Tr("docker_not_found"), nil)
			return
		}
		switch action {
		case "select":
			showContainerActions(ctx, bot, chatID, msgID, ref)
		case "start", "stop", "restart", "logs", "kill":
			confirmContainerAction(ctx, bot, chatID, msgID, ref, action)
		case "ailog":
			showContainerAIAnalysis(ctx, bot, chatID, msgID, ref)
		}
	case "confirm":
		if len(parts) < 4 {
			return
		}
		containerAction := parts[len(parts)-1]
		ref, ok := resolveDockerRef(ctx, strings.Join(parts[2:len(parts)-1], "_"))
		if !ok {
			editMessage(bot, chatID, msgID, ctx.Tr("docker_not_found"), nil)
			return
		}
		executeContainerAction(ctx, bot, chatID, msgID, ref, containerAction)
	}
}

// showContainerActions shows actions for a specific container
func showContainerActions(ctx *AppContext, bot BotAPI, chatID int64, msgID int, ref dockerContainerRef) {
	// Re-resolve through the token: the keyboard may be older than the listing,
	// and the status shown must be the current one. The token itself is stable,
	// so the buttons below still address the container that was selected.
	container, ok := resolveDockerContainer(ctx, ref.token)
	if !ok {
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

	fmt.Fprintf(&b, "%s *%s*\n\n", icon, container.Name)
	fmt.Fprintf(&b, ctx.Tr("docker_status"), statusText)
	fmt.Fprintf(&b, ctx.Tr("docker_image"), truncate(container.Image, 20))
	cID := container.ID
	if len(cID) > 12 {
		cID = cID[:12]
	}
	fmt.Fprintf(&b, ctx.Tr("docker_id"), cID)

	if container.Running {
		stats := getContainerStats(container.Name)
		if stats != "" {
			b.WriteString("\n" + stats)
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	if container.Running {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("stop"), containerCallbackData("stop", ref)),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("restart"), containerCallbackData("restart", ref)),
		))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("kill"), containerCallbackData("kill", ref)),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("logs"), containerCallbackData("logs", ref)),
		))
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("start"), containerCallbackData("start", ref)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("back"), "show_docker"),
	))

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

// containerConfirmPrompt builds the text and the confirmation keyboard for a
// destructive container action. Shared by the button flow (which edits the
// message it came from) and by the /kill command (which sends a new message), so
// both go through the same warning.
func containerConfirmPrompt(ctx *AppContext, ref dockerContainerRef, action string) (string, *tgbotapi.InlineKeyboardMarkup) {
	actionText := map[string]string{
		"start":   ctx.Tr("start"),
		"stop":    ctx.Tr("stop"),
		"restart": ctx.Tr("restart"),
		"kill":    ctx.Tr("kill"),
	}[action]

	text := fmt.Sprintf(ctx.Tr("confirm_action"), actionText, ref.name)
	if action == "kill" {
		text += ctx.Tr("kill_warn")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("yes"), containerConfirmData(ref, action)),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("no"), containerCallbackData("cancel", ref)),
		),
	)
	return text, &kb
}

// confirmContainerAction asks for confirmation before container action
func confirmContainerAction(ctx *AppContext, bot BotAPI, chatID int64, msgID int, ref dockerContainerRef, action string) {
	if action == "logs" {
		showContainerLogs(ctx, bot, chatID, msgID, ref)
		return
	}

	text, kb := containerConfirmPrompt(ctx, ref, action)
	editMessage(bot, chatID, msgID, text, kb)
}

// executeContainerAction executes a container action
func executeContainerAction(ctx *AppContext, bot BotAPI, chatID int64, msgID int, ref dockerContainerRef, action string) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The token is resolved again here, immediately before the CLI call: a
	// keyboard pressed after the container was removed and recreated must not hit
	// the new one. The resolved name is the only thing handed to Docker.
	container, ok := resolveDockerContainer(ctx, ref.token)
	if !ok {
		editMessage(bot, chatID, msgID, ctx.Tr("docker_not_found"), nil)
		return
	}
	containerName := container.Name

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
			errMsg = sanitizeErr(err).Error()
		}
		resultText = fmt.Sprintf(ctx.Tr("docker_action_err"), action, containerName, errMsg)
		ctx.State.AddEvent("warning", fmt.Sprintf("Error %s container %s: %s", action, containerName, errMsg))
	} else {
		actionPast := map[string]string{
			"start":   ctx.Tr("docker_started"),
			"stop":    ctx.Tr("docker_stopped"),
			"restart": ctx.Tr("docker_restarted"),
			"kill":    ctx.Tr("docker_killed"),
		}[action]
		resultText = fmt.Sprintf(ctx.Tr("docker_action_ok"), containerName, actionPast)
		ctx.State.AddEvent("info", fmt.Sprintf("Container %s: %s (manual)", containerName, action))
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 "+ctx.Tr("docker_menu_home_containers"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 "+ctx.Tr("docker_menu_home"), "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}

// showContainerLogs shows container logs
func showContainerLogs(ctx *AppContext, bot BotAPI, chatID int64, msgID int, ref dockerContainerRef) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCommandOutput(timeoutCtx, "docker", "logs", "--tail", "30", ref.name)

	var text string
	if err != nil {
		text = fmt.Sprintf(ctx.Tr("docker_logs_err"), sanitizeErr(err))
	} else {
		logs := string(out)
		if len(logs) > 3500 {
			logs = logs[len(logs)-3500:]
		}
		if logs == "" {
			logs = ctx.Tr("docker_logs_empty")
		}
		text = fmt.Sprintf("\n📜 "+ctx.Tr("docker_logs_title")+"\n```\n%s\n```", ref.name, logs)
	}

	rows := [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("docker_menu_refresh"), containerCallbackData("logs", ref)),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), containerCallbackData("select", ref)),
		),
	}
	// Add AI analysis button if Gemini is configured
	if ctx.Cfg().GeminiAPIKey != "" {
		rows = append([][]tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤖 "+ctx.Tr("docker_ai_analyze"), containerCallbackData("ailog", ref)),
			),
		}, rows...)
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
	editMessage(bot, chatID, msgID, text, &kb)
}

// showContainerAIAnalysis sends container logs to Gemini for AI analysis
func showContainerAIAnalysis(ctx *AppContext, bot BotAPI, chatID int64, msgID int, ref dockerContainerRef) {
	if ctx.Cfg().GeminiAPIKey == "" {
		editMessage(bot, chatID, msgID, "❌ "+ctx.Tr("health_no_gemini"), nil)
		return
	}

	// Show loading
	modelName := "gemini-3.1-flash-lite"
	loadingText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("docker_ai_analyzing"), modelName)
	edit := tgbotapi.NewEditMessageText(chatID, msgID, loadingText)
	edit.ParseMode = "Markdown"
	safeSend(bot, edit)

	// Get container logs (last 100 lines for more context)
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	out, err := runCommandOutput(timeoutCtx, "docker", "logs", "--tail", "100", ref.name)
	if err != nil {
		errText := fmt.Sprintf("❌ %s: %v", ctx.Tr("docker_logs_err_short"), sanitizeErr(err))
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), containerCallbackData("logs", ref)),
			),
		)
		editMessage(bot, chatID, msgID, errText, &kb)
		return
	}

	logs := string(out)
	if strings.TrimSpace(logs) == "" {
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), containerCallbackData("logs", ref)),
			),
		)
		editMessage(bot, chatID, msgID, "📭 "+ctx.Tr("docker_logs_empty"), &kb)
		return
	}

	// Truncate logs if too long for prompt
	if len(logs) > 6000 {
		logs = logs[len(logs)-6000:]
	}

	prompt := fmt.Sprintf(ctx.Tr("docker_ai_prompt"), ref.name, logs)

	analysis, err := callGeminiWithFallback(ctx, prompt, func(model string) {
		newText := fmt.Sprintf("⏳ %s\n_(%s)_", ctx.Tr("docker_ai_analyzing"), model)
		edit := tgbotapi.NewEditMessageText(chatID, msgID, newText)
		edit.ParseMode = "Markdown"
		safeSend(bot, edit)
	})

	if err != nil {
		slog.Error("Docker AI log analysis error", "container", ref.name, "err", err)
		errText := fmt.Sprintf("❌ %s\n\n_Error: %v_", ctx.Tr("docker_ai_error"), sanitizeErr(err))
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("docker_ai_analyze"), containerCallbackData("ailog", ref)),
				tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), containerCallbackData("logs", ref)),
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

	result := fmt.Sprintf("🤖 *%s — %s*\n\n%s", ctx.Tr("docker_ai_title"), ref.name, analysis)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📜 "+ctx.Tr("logs"), containerCallbackData("logs", ref)),
			tgbotapi.NewInlineKeyboardButtonData("⬅️ "+ctx.Tr("back"), containerCallbackData("select", ref)),
		),
	)
	finalEdit := tgbotapi.NewEditMessageText(chatID, msgID, result)
	finalEdit.ParseMode = "Markdown"
	finalEdit.ReplyMarkup = &kb
	if _, sendErr := bot.Send(finalEdit); sendErr != nil {
		slog.Error("Error sending Docker AI analysis (Markdown)", "err", sanitizeErr(sendErr))
		finalEdit.ParseMode = ""
		safeSend(bot, finalEdit)
	}
}

// getContainerInfoText gets container info text
func getContainerInfoText(c ContainerInfo) string {
	var b strings.Builder
	icon := "⏸"
	if c.Running {
		icon = "▶️"
	}
	fmt.Fprintf(&b, "%s *%s*\n", icon, c.Name)
	fmt.Fprintf(&b, "`%s`\n", c.Image)
	fmt.Fprintf(&b, "%s\n", c.Status)

	if c.Running {
		stats := getContainerStats(c.Name)
		if stats != "" {
			b.WriteString("\n" + stats)
		}
	}

	return b.String()
}

// handleContainerCommand handles the /container command
func handleContainerCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
	if args == "" {
		sendDockerMenu(ctx, bot, chatID)
		return
	}

	containers := getCachedContainerList(ctx)
	for i := range containers {
		if strings.EqualFold(containers[i].Name, args) {
			msg := tgbotapi.NewMessage(chatID, getContainerInfoText(containers[i]))
			msg.ParseMode = "Markdown"
			safeSend(bot, msg)
			return
		}
	}
	safeSend(bot, tgbotapi.NewMessage(chatID, fmt.Sprintf(ctx.Tr("docker_container_not_found"), args)))
}

// handleKillCommand handles the /kill command
func handleKillCommand(ctx *AppContext, bot BotAPI, chatID int64, args string) {
	if args == "" {
		sendMarkdown(bot, chatID, ctx.Tr("kill_usage"))
		return
	}

	containers := getCachedContainerList(ctx)
	var found *ContainerInfo
	for i := range containers {
		if strings.EqualFold(containers[i].Name, args) {
			found = &containers[i]
			break
		}
	}

	if found == nil {
		sendMarkdown(bot, chatID, fmt.Sprintf(ctx.Tr("docker_container_not_found"), args))
		return
	}

	if !found.Running {
		sendMarkdown(bot, chatID, fmt.Sprintf("⏸ Container `%s` %s", found.Name, ctx.Tr("status_not_running")))
		return
	}

	// `docker kill` is SIGKILL: irreversible, and the message that carries it can
	// be as old as the user wants. It used to run straight from the command, with
	// no confirmation and with the raw argument as the target, so a /kill typed
	// while the listing was different could hit another container. Go through
	// the same confirmation the kill button uses, bound to found.Name.
	ref := newDockerContainerRef(found)
	text, kb := containerConfirmPrompt(ctx, ref, "kill")

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	safeSend(bot, msg)
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
	containers := getCachedContainerList(ctx)
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
	containers := getCachedContainerList(ctx)
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
			slog.Error("Failed to restart container", "container", name, "err", sanitizeErr(err))
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

	ctx.State.AddEvent("action", fmt.Sprintf("Restart all containers: %d ok, %d failed", len(succeeded), len(failed)))

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 "+ctx.Tr("docker_menu_home_containers"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 "+ctx.Tr("docker_menu_home"), "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, b.String(), &kb)
}

// executeDockerServiceRestart restarts the Docker service
func executeDockerServiceRestart(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	editMessage(bot, chatID, msgID, "🔄 "+ctx.Tr("wd_restarting"), nil)

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
			errMsg = sanitizeErr(err).Error()
		}
		resultText = fmt.Sprintf(ctx.Tr("docker_restart_err"), errMsg)
		ctx.State.AddEvent("critical", fmt.Sprintf("Docker restart failed: %s", errMsg))
	} else {
		resultText = ctx.Tr("docker_restart_sent")
		ctx.State.AddEvent("action", "Docker service restarted (manual)")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 "+ctx.Tr("docker_menu_home_containers"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 "+ctx.Tr("docker_menu_home"), "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, resultText, &kb)
}
