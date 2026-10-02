package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// checkDockerHealth drives the Docker watchdog.
//
// It distinguishes three outcomes instead of two. A reachable daemon with an
// empty container list is a plausible configuration on a NAS — the user stopped
// everything — and must never trigger a service restart; only an unreachable
// daemon is a fault. The old test was `len(containers) > 0`, which conflated the
// two: on a machine with Docker installed and nothing running, the watchdog
// re-armed its own timer on every expiry and told the user it was "restarting
// Docker" forever, once every TimeoutMinutes.
func checkDockerHealth(ctx *AppContext, bot BotAPI) {
	containers, err := getContainerListWithError()

	// If Docker CLI was killed, it's a strong sign of OOM. Check immediately.
	if err != nil && (strings.Contains(err.Error(), "killed") || strings.Contains(err.Error(), "signal: 9")) {
		slog.Warn("Docker CLI killed, triggering kernel event check...")
		checkKernelEvents(ctx, bot)
	}

	cfg := ctx.Cfg()

	switch {
	case err == nil && len(containers) > 0:
		if clearDockerFailure(ctx) {
			slog.Info("Docker recovered/populated.")
		}
		return

	case err == nil:
		// Docker answered and there is nothing to show. Not a failure: drop any
		// window a previous real outage left open, so a genuine failure later
		// starts a fresh and honest timer instead of inheriting a stale one.
		// Debug level on purpose: this runs every 10s and must not fill the log.
		if clearDockerFailure(ctx) {
			slog.Info("Docker reachable with no containers: watchdog window cleared.")
		} else {
			slog.Debug("Docker reachable, no containers: watchdog idle.")
		}
		return
	}

	// The CLI itself failed: the daemon is unreachable, that is a real outage.
	failureStart, armed := dockerFailureWindow(ctx)
	if armed {
		slog.Warn("Docker CLI unreachable, watchdog window started.",
			"err", err, "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)
		return
	}

	timeout := time.Duration(cfg.Docker.Watchdog.TimeoutMinutes) * time.Minute
	if time.Since(failureStart) <= timeout {
		return
	}

	downMinutes := int(time.Since(failureStart).Round(time.Minute).Minutes())
	slog.Error("Docker unreachable longer than timeout",
		"err", err, "down_mins", downMinutes, "timeout_mins", cfg.Docker.Watchdog.TimeoutMinutes)

	// Re-arm before acting: a daemon that stays down is then retried once per
	// TimeoutMinutes instead of on every 10s tick.
	rearmDockerFailure(ctx)

	// The window is now known to be a reachability failure, not an empty
	// container list, so the body reports the actual cause: an unreachable CLI
	// also means no containers could be detected, but the error is the reason.
	body := fmt.Sprintf(ctx.Tr("wd_no_containers"), downMinutes) +
		fmt.Sprintf(ctx.Tr("health_watchdogs_last_error"), err)

	if !cfg.Docker.Watchdog.AutoRestartService {
		if !ctx.IsQuietHours() {
			title := ctx.Tr("wd_title")
			footer := ctx.Tr("wd_disabled")
			safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, title+body+footer))
		}
		ctx.State.AddEvent("warning", "Docker watchdog triggered (restart disabled)")
		return
	}

	if !ctx.IsQuietHours() {
		title := ctx.Tr("wd_title")
		footer := ctx.Tr("wd_restarting")
		safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, title+body+footer))
	}

	ctx.State.AddEvent("action", "Docker watchdog restart triggered")

	c, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	var out []byte
	if commandExists("systemctl") {
		out, err = runCommandOutput(c, "systemctl", "restart", "docker")
	} else {
		out, err = runCommandOutput(c, "service", "docker", "restart")
	}
	if err != nil {
		if !ctx.IsQuietHours() {
			safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, fmt.Sprintf(ctx.Tr("docker_restart_err"), err)))
		}
		slog.Error("Docker restart fail", "err", err, "output", string(out))
		return
	}
	if !ctx.IsQuietHours() {
		safeSend(bot, tgbotapi.NewMessage(cfg.AllowedUserID, ctx.Tr("docker_restart_sent")))
	}
}

// dockerFailureWindow returns the start of the open Docker outage window,
// arming it when none is open. armed is true only on the call that started the
// window, so the caller logs and notifies that transition exactly once.
func dockerFailureWindow(ctx *AppContext) (start time.Time, armed bool) {
	ctx.State.Mu.Lock()
	defer ctx.State.Mu.Unlock()
	if ctx.State.DockerFailure.IsZero() {
		ctx.State.DockerFailure = time.Now()
		return ctx.State.DockerFailure, true
	}
	return ctx.State.DockerFailure, false
}

// rearmDockerFailure restarts the outage window, so the next attempt happens one
// full timeout later instead of on the next tick.
func rearmDockerFailure(ctx *AppContext) {
	ctx.State.Mu.Lock()
	defer ctx.State.Mu.Unlock()
	ctx.State.DockerFailure = time.Now()
}

// clearDockerFailure drops the outage window and reports whether one was open.
func clearDockerFailure(ctx *AppContext) bool {
	ctx.State.Mu.Lock()
	defer ctx.State.Mu.Unlock()
	if ctx.State.DockerFailure.IsZero() {
		return false
	}
	ctx.State.DockerFailure = time.Time{}
	return true
}

// --- weekly prune: preview, confirmation, execution -----------------------
//
// `docker system prune -a -f` was the only destructive action of the bot
// without a confirmation: it removes every image no container refers to — the
// image a rollback needs, the image of a container the user stopped on purpose
// — and it does it unrecoverably. `container kill` goes through
// confirmContainerAction, `reboot` and `shutdown` through
// askPowerConfirmation; the prune was the exception, and the exception is the
// one that costs.
//
// The flow is now the one both of those use: an inventory the user can read, a
// button, and the command only behind the button.
//
// Which Docker command can produce that inventory was verified against the
// shipped CLI, not guessed:
//   - `docker system prune` has NO --dry-run flag. Its registered options are
//     -a/--all, -f/--force, --volumes and --filter (docker/cli
//     cli/command/system/prune.go, and the CLI reference, which lists the same
//     four). Running it with --dry-run only earns "unknown flag".
//   - `docker system df -v` IS the supported way: its Images table carries a
//     CONTAINERS column, and an image with zero containers is exactly what
//     `prune -a` deletes.
//
// So the preview reads `docker system df -v`, and when that command fails or
// its table layout is not the one we know, the user is told that the preview is
// unavailable and the prune does NOT run: a confirmation without an inventory in
// front of it is not a confirmation.
//
// Authority: ctx.Settings.DockerPrune, and nothing else. It is the only one of
// the two that a user can change at runtime (the settings screen writes
// DockerPrune.Enabled/Day/Hour, and state.go restores it from disk), and it is
// seeded from the config in InitApp, so it already reflects the config on a
// fresh install. The caller's gate on cfg.Docker.WeeklyPrune.Enabled is
// therefore both redundant and harmful — it silently ignores the runtime toggle.
// monitors_manager.go must call this unconditionally.

const (
	// pruneConfirmDataPrefix and pruneCancelDataPrefix are the callback payloads
	// of the two buttons. They carry a token minted when the preview was built,
	// never a name and never a path: 20+16 = 36 bytes and 19+16 = 35 bytes, half
	// of Telegram's 64-byte limit, and the token resolves against the pending
	// confirmation instead of against a container listing. A keyboard rendered
	// last week — or before a restart — resolves to nothing and is refused.
	pruneConfirmDataPrefix = "docker_prune_confirm_"
	pruneCancelDataPrefix  = "docker_prune_cancel_"

	// prunePreviewTimeout bounds the inventory queries (df -v, ps -a, network ls)
	// and pruneExecuteTimeout the prune itself, which is the budget the old code
	// already gave it.
	prunePreviewTimeout = 90 * time.Second
	pruneExecuteTimeout = 5 * time.Minute

	// prunePreviewMaxItems and pruneItemMaxRunes keep the confirmation inside
	// Telegram's 4096-character message limit whatever a repository is called:
	// three categories of truncated labels, plus the header and the warning.
	// What was dropped is counted, never silently cut.
	prunePreviewMaxItems = 15
	pruneItemMaxRunes    = 60
)

// prunePending is the single outstanding prune confirmation.
//
// It is process state on purpose, and it is the whole answer to "what if the bot
// restarts between the preview and the press": a confirmation that outlived the
// process would be a confirmation of an inventory nobody is looking at any more,
// spent on the irreversible command. Losing it costs one weekly cleanup;
// keeping it costs the rollback image.
var prunePending struct {
	mu    model.Mutex
	token string
}

// errPrunePreviewUnsupported means Docker answered but not in a layout the
// preview can read, or refused the query outright. It is not an error to hide:
// the caller tells the user and does not prune.
var errPrunePreviewUnsupported = errors.New("docker system df -v unavailable")

// mintPruneToken returns the short token of one confirmation. Hex nanoseconds:
// unique per preview, and bounded whatever the clock does.
func mintPruneToken() string {
	return strconv.FormatUint(uint64(time.Now().UnixNano()), 16)
}

// armPruneConfirmation makes token the one confirmation that may be executed.
func armPruneConfirmation(token string) {
	prunePending.mu.Lock()
	defer prunePending.mu.Unlock()
	prunePending.token = token
}

// claimPruneConfirmation consumes the pending confirmation and reports whether
// it was the one that was armed.
//
// Single-shot on purpose: the token is cleared before the caller runs anything,
// so a second press of the same button (or of a keyboard copied from the chat)
// finds nothing and is refused.
func claimPruneConfirmation(token string) bool {
	if token == "" {
		return false
	}
	prunePending.mu.Lock()
	defer prunePending.mu.Unlock()
	if prunePending.token == "" || prunePending.token != token {
		return false
	}
	prunePending.token = ""
	return true
}

// isPendingPrune reports whether token is the confirmation currently armed. The
// read-only counterpart of claimPruneConfirmation, for the "no" button: a stale
// keyboard must not disarm the confirmation the user is looking at now.
func isPendingPrune(token string) bool {
	if token == "" {
		return false
	}
	prunePending.mu.Lock()
	defer prunePending.mu.Unlock()
	return prunePending.token != "" && prunePending.token == token
}

// clearPruneConfirmation drops the pending confirmation, so a keyboard that
// stays on screen cannot arm anything after the user said no.
func clearPruneConfirmation() {
	prunePending.mu.Lock()
	defer prunePending.mu.Unlock()
	prunePending.token = ""
}

// checkWeeklyPrune runs the weekly prune: inventory, confirmation, command.
//
// The window is the scheduled day from the scheduled hour onwards, not the
// scheduled hour alone, because the confirmation notice respects quiet hours
// (the default prune hour, 4, is inside the default quiet window) and a window
// that closed when the hour rolled over would make the confirmation impossible:
// the user would never be asked, so nothing would ever be pruned. The window is
// still consumed at most once, by PruneDoneToday.
func checkWeeklyPrune(ctx *AppContext, bot BotAPI) {
	settings := ctx.Settings.GetDockerPrune()
	if !settings.Enabled {
		return
	}

	hour := settings.Hour
	if hour < 0 || hour > 23 {
		// A hand-edited state file must not make the window unreachable.
		slog.Warn("Docker prune: hour out of range, clamping", "hour", hour)
		hour = min(max(hour, 0), 23)
	}

	now := time.Now().In(pruneLocation(ctx))
	if now.Weekday() != pruneTargetWeekday(settings.Day) || now.Hour() < hour {
		// Not in the window: re-arm the guard, so tomorrow's window is armed
		// again and a window consumed today does not leak into the next day.
		resetPruneWindow(ctx)
		return
	}

	// Quiet hours gate the notice, not the window: the tick keeps retrying and
	// the inventory goes out as soon as quiet hours end. Nothing is claimed here,
	// so a suppressed notice is retried instead of silently consuming the week.
	if ctx.IsQuietHours() {
		slog.Debug("Docker prune: window open but quiet hours hold the notice")
		return
	}

	// One claim for the whole window, taken before the goroutine starts: the tick
	// fires every 10s and the prune must be asked about once.
	if !claimPruneWindow(ctx) {
		return
	}

	slog.Info("Docker: weekly prune window claimed, building the preview...")

	goSafe("docker-weekly-prune", func() {
		sendPrunePreview(ctx, bot)
	})
}

// pruneLocation returns the location the schedule is expressed in, tolerating a
// context built by hand (the tests, the standalone watchdog) the way IsQuietHours
// does: time.Now().In(nil) panics.
func pruneLocation(ctx *AppContext) *time.Location {
	if loc := ctx.State.TimeLocation; loc != nil {
		return loc
	}
	return time.Local
}

// pruneTargetWeekday maps the stored weekday name to its time.Weekday. An
// unknown name keeps the historical default (Sunday) instead of disabling the
// feature: the guard below still holds.
func pruneTargetWeekday(day string) time.Weekday {
	switch strings.ToLower(strings.TrimSpace(day)) {
	case "monday":
		return time.Monday
	case "tuesday":
		return time.Tuesday
	case "wednesday":
		return time.Wednesday
	case "thursday":
		return time.Thursday
	case "friday":
		return time.Friday
	case "saturday":
		return time.Saturday
	default:
		return time.Sunday
	}
}

// claimPruneWindow consumes PruneDoneToday and reports whether this call was the
// one that consumed it. It is the only writer of the flag besides reset, and it
// does the read and the write under a single lock: a check-then-set from the
// caller would let two ticks of the same 10s lane both proceed.
func claimPruneWindow(ctx *AppContext) bool {
	ctx.Docker.Mu.Lock()
	defer ctx.Docker.Mu.Unlock()
	if ctx.Docker.PruneDoneToday {
		return false
	}
	ctx.Docker.PruneDoneToday = true
	return true
}

// resetPruneWindow re-arms the window. The read is done under the read lock so
// that the write lock, taken on every 10s tick for the whole day, is only
// reached when there is something to clear.
func resetPruneWindow(ctx *AppContext) {
	ctx.Docker.Mu.RLock()
	armed := ctx.Docker.PruneDoneToday
	ctx.Docker.Mu.RUnlock()
	if !armed {
		return
	}
	ctx.Docker.Mu.Lock()
	defer ctx.Docker.Mu.Unlock()
	ctx.Docker.PruneDoneToday = false
}

// prunePreview is what the prune would remove, as the user is shown it.
type prunePreview struct {
	images   []string // images no container refers to: what `prune -a` deletes
	stopped  []string // stopped containers: also removed by the prune
	networks []string // networks no container refers to
}

// empty reports whether there is nothing at all to remove. "Nothing to remove"
// is an answer: the week is skipped and nothing is asked.
func (p prunePreview) empty() bool {
	return len(p.images) == 0 && len(p.stopped) == 0 && len(p.networks) == 0
}

// sendPrunePreview builds the inventory and either asks for confirmation or
// explains why nothing will be asked.
//
// It runs outside every lock: safeSend is an HTTP call and ctx.Tr takes the
// settings lock.
func sendPrunePreview(ctx *AppContext, bot BotAPI) {
	cmdCtx, cancel := context.WithTimeout(context.Background(), prunePreviewTimeout)
	defer cancel()

	preview, err := collectPrunePreview(cmdCtx)
	if err != nil {
		// Not a failure to swallow: the user is told the inventory could not be
		// built, and the prune does not run. Silence here would be the old
		// behaviour with a nicer name.
		sendPruneNotice(ctx, bot, prunePreviewFailureText(ctx, err))
		slog.Warn("Docker: prune preview unavailable, prune not attempted",
			"err", sanitizeErr(err))
		return
	}

	if preview.empty() {
		sendPruneNotice(ctx, bot, ctx.Tr("prune_preview_empty"))
		slog.Info("Docker: nothing to prune")
		return
	}

	token := mintPruneToken()
	armPruneConfirmation(token)

	msg := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, prunePreviewText(ctx, preview))
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = prunePreviewKeyboard(ctx, token)
	safeSend(bot, msg)

	slog.Info("Docker: prune preview sent, waiting for confirmation",
		"images", len(preview.images), "stopped", len(preview.stopped), "networks", len(preview.networks))
}

// prunePreviewFailureText distinguishes "this Docker cannot show me the list"
// from "the query failed": the first is a version problem the user can act on,
// the second is an operational one.
func prunePreviewFailureText(ctx *AppContext, err error) string {
	if errors.Is(err, errPrunePreviewUnsupported) {
		return ctx.Tr("prune_dryrun_unsupported")
	}
	return fmt.Sprintf(ctx.Tr("prune_dryrun_failed"), sanitizeErr(err))
}

// sendPruneNotice sends a prune message with no keyboard: there is nothing to
// confirm.
func sendPruneNotice(ctx *AppContext, bot BotAPI, text string) {
	msg := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, text)
	msg.ParseMode = "Markdown"
	safeSend(bot, msg)
}

// collectPrunePreview asks Docker what the prune would remove.
//
// `docker system df -v` is the authority and its failure is fatal to the
// preview: without it we cannot say what disappears. The other two categories
// are best-effort and their failure only shrinks the list, never fakes it.
func collectPrunePreview(cmdCtx context.Context) (prunePreview, error) {
	out, err := runCommandStdout(cmdCtx, "docker", "system", "df", "-v")
	if err != nil {
		return prunePreview{}, err
	}

	images, ok := parseDockerSystemDFUnusedImages(string(out))
	if !ok {
		return prunePreview{}, errPrunePreviewUnsupported
	}

	preview := prunePreview{images: images}

	// Stopped containers: `docker ps -a` is already the shape the rest of the
	// Docker area parses, and it is what makes the "stopped container" line
	// honest instead of an assumption.
	if containers, listErr := getContainerListWithError(); listErr == nil {
		for _, c := range containers {
			if !c.Running {
				preview.stopped = append(preview.stopped, c.Name)
			}
		}
	}

	// `dangling` on a network means "no container refers to it", which is what
	// the prune network pass removes.
	if nets, netErr := runCommandStdout(cmdCtx, "docker", "network", "ls", "--filter", "dangling=true", "--format", "{{.Name}}"); netErr == nil {
		preview.networks = splitNonEmptyLines(string(nets))
	}

	return preview, nil
}

// parseDockerSystemDFUnusedImages reads the Images section of
// `docker system df -v` and returns one label per image no container refers to,
// which is exactly what `docker system prune -a` deletes.
//
// The output is not ours, and it is not parseable by column position. Two
// properties of it decide the whole design:
//   - CREATED is human-readable ("6 minutes ago", "9 weeks ago"), so a row has
//     more whitespace-separated fields than the header declares. Every index
//     after CREATED is therefore shifted by an amount the parser cannot know.
//     Only the three leading columns are safe: a repository and a tag cannot
//     contain whitespace, Docker refuses to build such a reference.
//   - CONTAINERS is the last declared column, in every layout published so far.
//     Note that two of the headers carry a *multi-word* column ("SHARED SIZE",
//     "UNIQUE SIZE"), so the column count is not the field count either.
//
// So the parser anchors on the END of the row for the container count and on the
// BEGINNING for the name, and it verifies the header rather than assuming it: a
// layout that grew a column after CONTAINERS is refused, not guessed.
//
// Refusal is the safe direction throughout. A row that is short, or whose count
// is not a number ("--", "N/A"), is skipped rather than read as "0 containers":
// reading a used image as unused would put it on a delete list.
//
// ok is false when the header is missing or unusable. The caller must then tell
// the user the preview is unavailable and must not prune.
func parseDockerSystemDFUnusedImages(out string) ([]string, bool) {
	lines := strings.Split(out, "\n")

	// A section marker without a table below it means the host has no images: that
	// is "nothing to remove", not a layout we cannot read. No marker at all is a
	// refusal.
	hasSection := false
	for _, line := range lines {
		if strings.Contains(line, "Images space usage") {
			hasSection = true
			break
		}
	}

	header := -1
	headerFields := []string{}
	partialHeader := -1
	for i, line := range lines {
		fields := strings.Fields(line)
		if !containsDockerColumn(fields, "REPOSITORY") {
			continue
		}
		if !containsDockerColumn(fields, "CONTAINERS") {
			// A table that promises images but not the column this parser reads is
			// a layout we do not know: remember it and refuse below.
			partialHeader = i
			continue
		}
		header, headerFields = i, fields
		break
	}
	if header < 0 {
		if partialHeader >= 0 {
			return nil, false
		}
		// Section present, no table: the host has no images.
		return nil, hasSection
	}
	// REPOSITORY first and CONTAINERS last: those are the two anchors of this
	// parser, so a layout that does not promise them is refused, not guessed.
	if !strings.EqualFold(headerFields[0], "REPOSITORY") ||
		!strings.EqualFold(headerFields[len(headerFields)-1], "CONTAINERS") {
		return nil, false
	}

	var unused []string
	for _, line := range lines[header+1:] {
		// The table ends at the first blank line. What follows is another section
		// ("Containers space usage:"), whose short rows the length check below
		// skips anyway.
		if strings.TrimSpace(line) == "" {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		count, convErr := strconv.Atoi(fields[len(fields)-1])
		if convErr != nil || count > 0 {
			continue
		}
		unused = append(unused, dockerImageLabel(fields))
	}
	return unused, true
}

// containsDockerColumn reports whether a header row declares the given column.
func containsDockerColumn(fields []string, name string) bool {
	for _, field := range fields {
		if strings.EqualFold(field, name) {
			return true
		}
	}
	return false
}

// dockerImageLabel renders one image row as `repo:tag`. The `<none>:<none>` rows
// a prune leaves behind have no tag worth showing, so they carry the short image
// ID instead: it is the only handle the user has on them.
func dockerImageLabel(fields []string) string {
	repo, tag, id := fields[0], fields[1], fields[2]
	if tag != "" && tag != "<none>" {
		return repo + ":" + tag
	}
	if len(id) > 12 {
		id = id[:12]
	}
	return repo + ":" + tag + " (" + id + ")"
}

// prunePreviewText renders the inventory and the risk, for the confirmation.
func prunePreviewText(ctx *AppContext, p prunePreview) string {
	var b strings.Builder
	b.WriteString(ctx.Tr("prune_preview_title"))
	b.WriteString(fmt.Sprintf(ctx.Tr("prune_preview_source"), "docker system df -v"))

	if len(p.images) > 0 {
		b.WriteString("\n\n" + fmt.Sprintf(ctx.Tr("prune_preview_count"), len(p.images)))
		b.WriteString(ctx.Tr("prune_preview_images"))
		b.WriteString(pruneItemBlock(ctx, p.images))
	}
	if len(p.stopped) > 0 {
		b.WriteString("\n\n" + fmt.Sprintf(ctx.Tr("prune_preview_count"), len(p.stopped)))
		b.WriteString(ctx.Tr("prune_preview_stopped"))
		b.WriteString(pruneItemBlock(ctx, p.stopped))
	}
	if len(p.networks) > 0 {
		b.WriteString("\n\n" + fmt.Sprintf(ctx.Tr("prune_preview_count"), len(p.networks)))
		b.WriteString(ctx.Tr("prune_preview_networks"))
		b.WriteString(pruneItemBlock(ctx, p.networks))
	}

	b.WriteString("\n\n" + ctx.Tr("prune_warn_irreversible"))
	return b.String()
}

// pruneItemBlock renders one category as a code block of at most
// prunePreviewMaxItems lines, plus the count of what was left out. The names go
// inside a fence because they can carry "_" and "*", which Markdown would
// otherwise eat.
func pruneItemBlock(ctx *AppContext, items []string) string {
	shown := items
	if len(shown) > prunePreviewMaxItems {
		shown = shown[:prunePreviewMaxItems]
	}

	var b strings.Builder
	b.WriteString("\n```\n")
	for _, item := range shown {
		b.WriteString("• " + truncate(item, pruneItemMaxRunes) + "\n")
	}
	b.WriteString("```")
	if omitted := len(items) - len(shown); omitted > 0 {
		b.WriteString(fmt.Sprintf(ctx.Tr("prune_preview_more"), omitted))
	}
	return b.String()
}

// prunePreviewKeyboard is the Yes/No pair of the confirmation, built like the
// one askPowerConfirmation shows: the same two labels, the same short payloads.
func prunePreviewKeyboard(ctx *AppContext, token string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ "+ctx.Tr("yes"), pruneConfirmDataPrefix+token),
			tgbotapi.NewInlineKeyboardButtonData("❌ "+ctx.Tr("no"), pruneCancelDataPrefix+token),
		),
	)
}

// handlePruneConfirmCallback runs the prune behind the confirmation button.
//
// The token is the gate: it is consumed first, so a second press, a keyboard
// copied from the chat and a button from before a restart are all refused, and
// only then does the command run.
func handlePruneConfirmCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	token := strings.TrimPrefix(data, pruneConfirmDataPrefix)
	if !claimPruneConfirmation(token) {
		editMessage(bot, chatID, msgID, ctx.Tr("session_expired"), nil)
		return
	}

	editMessage(bot, chatID, msgID, ctx.Tr("prune_running"), nil)

	slog.Warn("Docker: weekly prune confirmed by the user, running docker system prune -a -f")

	goSafe("docker-prune-execute", func() {
		executePruneNow(ctx, bot, chatID, msgID)
	})
}

// handlePruneCancelCallback answers No. Like the confirm button it is token
// bound: the keyboard of a previous week is inert, and it only drops the
// confirmation it names.
func handlePruneCancelCallback(ctx *AppContext, bot BotAPI, chatID int64, msgID int, data string) {
	token := strings.TrimPrefix(data, pruneCancelDataPrefix)
	if !isPendingPrune(token) {
		editMessage(bot, chatID, msgID, ctx.Tr("session_expired"), nil)
		return
	}
	clearPruneConfirmation()
	editMessage(bot, chatID, msgID, ctx.Tr("cancelled"), nil)
	slog.Info("Docker: weekly prune cancelled by the user")
}

// executePruneNow runs `docker system prune -a -f` and reports it.
//
// It does NOT consult quiet hours: the notice was gated by them, and the user
// answered the button. Quiet hours are a promise not to *wake* someone up, and
// the outcome of a command they just asked for is not a wake-up.
func executePruneNow(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	c, cancel := context.WithTimeout(context.Background(), pruneExecuteTimeout)
	defer cancel()

	out, err := runCommandOutput(c, "docker", "system", "prune", "-a", "-f")

	var msg string
	if err != nil {
		reason := sanitizeErr(err).Error()
		if c.Err() == context.DeadlineExceeded {
			reason = ctx.Tr("prune_err_timeout")
		}
		msg = fmt.Sprintf(ctx.Tr("prune_err_title"), reason)
		ctx.State.AddEvent("warning", msg)
		slog.Error("Docker: weekly prune failed", "err", sanitizeErr(err), "output", string(out))
	} else {
		msg = fmt.Sprintf(ctx.Tr("prune_ok_title"), lastDockerOutputLine(string(out)))
		ctx.State.AddEvent("info", ctx.Tr("prune_done_event"))
		slog.Info("Docker: weekly prune completed", "output", string(out))
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐳 "+ctx.Tr("docker_menu_home_containers"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData("🏠 "+ctx.Tr("docker_menu_home"), "back_main"),
		),
	)
	editMessage(bot, chatID, msgID, msg, &kb)
}

// lastDockerOutputLine returns the last non-blank line of a command output,
// which is where Docker puts the reclaimed total. Empty output yields an empty
// string rather than a panic.
func lastDockerOutputLine(output string) string {
	lines := splitNonEmptyLines(output)
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}
