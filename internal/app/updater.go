package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	pmodel "nasbot/pkg/model"
)

var Version = "dev"

const (
	defaultGitHubRepo   = "Fabbro96/NASBot"
	defaultReleaseCheck = 1 * time.Hour
	startupReleaseCheck = 10 * time.Second

	// checksumsAssetName is the manifest published by the release workflow
	// (.github/workflows/release.yml) next to every binary. An update is
	// installed only if its digest matches the one in this file.
	checksumsAssetName = "SHA256SUMS.txt"

	// maxAssetSize caps a release binary: 50MB is an order of magnitude more
	// than the Go binaries this project ships, and keeps a hostile or broken
	// release from filling the disk.
	maxAssetSize = 50 << 20

	// maxChecksumsSize caps the checksum manifest (two lines in practice).
	maxChecksumsSize = 64 << 10

	// downloadTimeout bounds a whole asset download.
	downloadTimeout = 10 * time.Minute

	// checksumsTimeout bounds the (small) checksum manifest download.
	checksumsTimeout = 30 * time.Second

	// restartCommandTimeout bounds `start_bot.sh restart`: without a deadline a
	// blocked script leaves the restart goroutine hanging forever.
	restartCommandTimeout = 60 * time.Second
)

// updaterApplyMu serialises the whole apply path (download → checksum
// verification → install). Without it the hourly updaterLoop tick and a manual
// "update_apply_latest" click can write and rename the same staging file at the
// same time and install a corrupted binary.
var updaterApplyMu pmodel.Mutex

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type releaseCandidate struct {
	Tag          string
	URL          string
	AssetName    string
	AssetURL     string
	ChecksumsURL string
	Changelog    string
}

func updaterRepo() string {
	repo := strings.TrimSpace(os.Getenv("NASBOT_GITHUB_REPO"))
	if repo == "" {
		return defaultGitHubRepo
	}
	return repo
}

// releaseVersion is a parsed release tag, ordered by semver precedence.
type releaseVersion struct {
	// core holds up to four numeric dot-separated segments (major, minor,
	// patch and the optional fourth segment); missing segments count as 0.
	core [4]int
	// pre is the prerelease part (without the leading "-"), "" when stable.
	pre string
}

// parseSemverTag is the historical 3-integer view of a tag, kept for callers
// that only need major.minor.patch. Version ordering goes through
// parseReleaseVersion/compareReleaseVersions, which also honour the prerelease
// suffix and the fourth segment.
func parseSemverTag(tag string) ([3]int, bool) {
	v, ok := parseReleaseVersion(tag)
	return [3]int{v.core[0], v.core[1], v.core[2]}, ok
}

// parseReleaseVersion parses a release tag such as "v1.2.3", "v1.2.3-rc.1" or
// "v1.2.3.4". ok is false when no numeric segment can be read at all (for
// example "dev" or "nightly"): such a tag must never be treated as a version,
// otherwise a malformed release could replace the running binary.
func parseReleaseVersion(tag string) (releaseVersion, bool) {
	var v releaseVersion

	t := strings.TrimSpace(strings.ToLower(tag))
	t = strings.TrimPrefix(t, "v")
	if t == "" {
		return v, false
	}
	// Build metadata carries no precedence in semver.
	if i := strings.IndexByte(t, '+'); i >= 0 {
		t = t[:i]
	}
	if i := strings.IndexByte(t, '-'); i >= 0 {
		v.pre = t[i+1:]
		t = t[:i]
	}

	parsedSomething := false
	for i, seg := range strings.Split(t, ".") {
		if i >= len(v.core) {
			break
		}
		n, err := strconv.Atoi(seg)
		if err != nil {
			break
		}
		v.core[i] = n
		parsedSomething = true
	}
	if !parsedSomething {
		return releaseVersion{}, false
	}
	return v, true
}

// compareReleaseVersions implements semver precedence:
//
//  1. the numeric core is compared segment by segment, a missing segment
//     counting as 0 (so "v1.2.3.4" is greater than "v1.2.3");
//  2. on an equal core, a stable version outranks any prerelease
//     ("v1.2.3" > "v1.2.3-rc.1");
//  3. two prereleases are compared by dot-separated identifiers: numeric
//     identifiers compare numerically and rank below alphanumeric ones,
//     alphanumeric identifiers compare lexically, and if one list is a prefix
//     of the other the longer one has higher precedence.
//
// It returns 1 if a > b, -1 if a < b, 0 if they have equal precedence.
func compareReleaseVersions(a, b releaseVersion) int {
	for i := range a.core {
		if a.core[i] > b.core[i] {
			return 1
		}
		if a.core[i] < b.core[i] {
			return -1
		}
	}
	return comparePrerelease(a.pre, b.pre)
}

func comparePrerelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // stable outranks a prerelease of the same core
	}
	if b == "" {
		return -1
	}

	aIDs := strings.Split(a, ".")
	bIDs := strings.Split(b, ".")
	for i := 0; i < len(aIDs) && i < len(bIDs); i++ {
		if c := comparePrereleaseID(aIDs[i], bIDs[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(aIDs) > len(bIDs):
		return 1
	case len(aIDs) < len(bIDs):
		return -1
	}
	return strings.Compare(a, b)
}

func comparePrereleaseID(a, b string) int {
	aNum, aErr := strconv.Atoi(a)
	bNum, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		switch {
		case aNum > bNum:
			return 1
		case aNum < bNum:
			return -1
		}
		return 0
	case aErr == nil:
		return -1 // numeric identifiers rank below alphanumeric ones
	case bErr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

// isNewerRelease reports whether latestTag outranks currentVersion. A tag that
// cannot be parsed is never newer (fail silently, keep the running binary); a
// running version that cannot be parsed (the "dev" default) accepts any release.
func isNewerRelease(latestTag, currentVersion string) bool {
	latest, okLatest := parseReleaseVersion(latestTag)
	current, okCurrent := parseReleaseVersion(currentVersion)
	if !okLatest {
		return false
	}
	if !okCurrent {
		return true
	}
	return compareReleaseVersions(latest, current) > 0
}

// preferredReleaseAssets lists, in order of preference, the release assets that
// actually exist for the running architecture. .github/workflows/release.yml
// publishes exactly two binaries, "nasbot" (amd64) and "nasbot-arm64"; naming
// an asset that is never published would only hide that fact. Architectures the
// pipeline does not build return no candidate at all: refusing to update is the
// only safe answer, installing a foreign binary would break the bot.
func preferredReleaseAssets() []string {
	switch runtime.GOARCH {
	case "amd64":
		return []string{"nasbot"}
	case "arm64":
		return []string{"nasbot-arm64"}
	default:
		return nil
	}
}

func pickAsset(rel githubRelease) (name, url string, ok bool) {
	for _, wanted := range preferredReleaseAssets() {
		for _, a := range rel.Assets {
			if a.Name == wanted {
				return a.Name, a.BrowserDownloadURL, true
			}
		}
	}
	// Do not fallback to a random asset to avoid breaking the bot with wrong architecture.
	return "", "", false
}

func pickAssetURL(rel githubRelease, name string) string {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.BrowserDownloadURL
		}
	}
	return ""
}

func fetchLatestRelease(ctx *AppContext) (releaseCandidate, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", updaterRepo())
	reqCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return releaseCandidate{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nasbot-updater")

	client := http.DefaultClient
	if ctx != nil && ctx.HTTP != nil {
		client = ctx.HTTP
	}

	resp, err := client.Do(req)
	if err != nil {
		return releaseCandidate{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return releaseCandidate{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return releaseCandidate{}, fmt.Errorf("github release API error %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rel githubRelease
	if err := json.Unmarshal(body, &rel); err != nil {
		return releaseCandidate{}, err
	}
	assetName, assetURL, ok := pickAsset(rel)
	if !ok {
		return releaseCandidate{}, fmt.Errorf("no downloadable release assets found for %s (published assets: %s)", runtime.GOARCH, strings.Join(preferredReleaseAssets(), ", "))
	}

	return releaseCandidate{
		Tag:          rel.TagName,
		URL:          rel.HTMLURL,
		AssetName:    assetName,
		AssetURL:     assetURL,
		ChecksumsURL: pickAssetURL(rel, checksumsAssetName),
		Changelog:    rel.Body,
	}, nil
}

func checkForUpdate(ctx *AppContext) (releaseCandidate, bool, error) {
	rel, err := fetchLatestRelease(ctx)
	if err != nil {
		return releaseCandidate{}, false, err
	}
	if !isNewerRelease(rel.Tag, Version) {
		return rel, false, nil
	}
	return rel, true, nil
}

func notifyUpdateAvailable(ctx *AppContext, bot BotAPI, rel releaseCandidate) {
	ctx.State.Mu.Lock()
	if ctx.State.LastReleaseNotified == rel.Tag {
		ctx.State.Mu.Unlock()
		return
	}
	ctx.State.Mu.Unlock()

	text := fmt.Sprintf(ctx.Tr("update_available"), rel.Tag, Version, rel.AssetName)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬇️ "+ctx.Tr("update_apply_now"), "update_apply_latest"),
			tgbotapi.NewInlineKeyboardButtonURL("📦 Release", rel.URL),
		),
	)
	msg := tgbotapi.NewMessage(updaterOwnerChatID(ctx), text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	if _, err := bot.Send(msg); err != nil {
		slog.Error("Release update notification failed", "err", sanitizeErr(err))
		msg.ParseMode = ""
		safeSend(bot, msg)
		return
	}

	markReleaseNotified(ctx, rel.Tag)
	saveState(ctx)
}

// markReleaseNotified records that rel.Tag has already been announced or
// installed, so an update that could not be restarted does not produce a new
// "version available" message on every hourly tick.
func markReleaseNotified(ctx *AppContext, tag string) {
	ctx.State.Mu.Lock()
	ctx.State.LastReleaseNotified = tag
	ctx.State.Mu.Unlock()
}

// updaterOwnerChatID is the fallback destination for updater messages that were
// not triggered from a chat (the hourly auto-apply path passes chatID 0).
func updaterOwnerChatID(ctx *AppContext) int64 {
	if ctx == nil {
		return 0
	}
	cfg := ctx.Cfg()
	if cfg == nil {
		return 0
	}
	return cfg.AllowedUserID
}

// updaterAutoApply reports whether an available update must be applied without
// waiting for the user to press the button.
func updaterAutoApply(ctx *AppContext) bool {
	if ctx == nil {
		return false
	}
	cfg := ctx.Cfg()
	return cfg != nil && cfg.Update.AutoApply
}

// updaterCheckInterval is how long the updater waits between two checks.
func updaterCheckInterval(ctx *AppContext) time.Duration {
	if ctx == nil {
		return defaultReleaseCheck
	}
	cfg := ctx.Cfg()
	if cfg != nil && cfg.Update.CheckIntervalHours > 0 {
		return time.Duration(cfg.Update.CheckIntervalHours) * time.Hour
	}
	return defaultReleaseCheck
}

func updaterLoop(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	// First check after a short delay at startup
	if !sleepWithContext(runCtx, startupReleaseCheck) {
		return
	}

	for {
		rel, hasUpdate, err := checkForUpdate(ctx)
		switch {
		case err != nil:
			slog.Warn("Update check failed", "err", sanitizeErr(err))
		case hasUpdate && updaterAutoApply(ctx):
			// Auto-apply without user interaction. The release is already
			// known here: re-checking would cost a second GitHub call per tick.
			applyRelease(ctx, bot, 0, 0, rel, true)
		case hasUpdate:
			notifyUpdateAvailable(ctx, bot, rel)
		}

		if !sleepWithContext(runCtx, updaterCheckInterval(ctx)) {
			return
		}
	}
}

func updaterTargetPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	baseDir := filepath.Dir(exe)
	return filepath.Join(baseDir, "nasbot-update"), nil
}

// updaterHTTPClient is used for release downloads instead of ctx.HTTP, whose
// 30s timeout is too short for a release binary.
func updaterHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// downloadToFile streams url into a fresh temporary file created in dir and
// returns its path. The caller owns the file: on any error it is already
// removed. sizeCap is enforced twice: from the announced Content-Length before
// a single byte is written, and from the number of bytes actually copied, so a
// body longer than the cap can never be mistaken for a complete download.
func downloadToFile(dir, url, prefix string, sizeCap int64, timeout time.Duration) (string, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "nasbot-updater")

	resp, err := updaterHTTPClient(timeout).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Announced size first: an oversized asset is refused before being written.
	if resp.ContentLength > sizeCap {
		return "", fmt.Errorf("download refused: announced size %d bytes exceeds the %d byte limit", resp.ContentLength, sizeCap)
	}

	// Same directory as the final target, so the install is a rename on the same
	// filesystem, and a unique name, so two concurrent runs cannot share it.
	f, err := os.CreateTemp(dir, prefix)
	if err != nil {
		return "", err
	}
	tmp := f.Name()

	written, err := io.Copy(f, io.LimitReader(resp.Body, sizeCap+1))
	if err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download interrupted after %d bytes: %w", written, err)
	}
	// io.Copy reports no error when it hits the LimitReader, so the copied
	// count is the only evidence that the payload was cut short: a body at or
	// over the cap, or shorter than announced, is not a complete file.
	if written > sizeCap {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download refused: received more than the %d byte limit", sizeCap)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download incomplete: got %d of %d announced bytes", written, resp.ContentLength)
	}
	if written == 0 {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("download refused: empty payload")
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// parseChecksumLine extracts the digest published for assetName from a
// `sha256sum` manifest ("<64 hex>  <name>"), tolerating the binary-mode '*'
// prefix and a leading "./".
func parseChecksumLine(manifest, assetName string) (string, bool) {
	for _, line := range strings.Split(manifest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		digest := strings.ToLower(fields[0])
		if len(digest) != 2*sha256.Size {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = strings.TrimPrefix(name, "./")
		if name != assetName {
			continue
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", false
		}
		return digest, true
	}
	return "", false
}

// verifyAssetChecksum refuses to install path unless its digest matches the one
// published in the release checksum manifest. A missing manifest, a manifest
// that does not mention the asset, and a digest mismatch are all hard failures.
func verifyAssetChecksum(dir, path string, rel releaseCandidate) error {
	if strings.TrimSpace(rel.ChecksumsURL) == "" {
		return fmt.Errorf("release %s publishes no %s: refusing to install %s unverified", rel.Tag, checksumsAssetName, rel.AssetName)
	}

	manifestPath, err := downloadToFile(dir, rel.ChecksumsURL, ".nasbot-checksums-*", maxChecksumsSize, checksumsTimeout)
	if err != nil {
		return fmt.Errorf("cannot download %s: %w", checksumsAssetName, err)
	}
	defer os.Remove(manifestPath)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	expected, ok := parseChecksumLine(string(raw), rel.AssetName)
	if !ok {
		return fmt.Errorf("%s does not list %s: refusing to install it unverified", checksumsAssetName, rel.AssetName)
	}

	got, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", rel.AssetName, expected, got)
	}
	slog.Info("Release asset verified", "asset", rel.AssetName, "tag", rel.Tag, "sha256", got)
	return nil
}

// downloadReleaseAsset downloads the release binary, verifies it against the
// checksum manifest published with the same release, and only then installs it
// as the staging binary. Anything that goes wrong leaves the currently installed
// binary untouched.
func downloadReleaseAsset(rel releaseCandidate) (string, error) {
	target, err := updaterTargetPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(target)

	tmp, err := downloadToFile(dir, rel.AssetURL, ".nasbot-update-*", maxAssetSize, downloadTimeout)
	if err != nil {
		return "", err
	}

	// Verification happens before the rename: a binary that is not the published
	// one must never reach the path that replaces the running executable.
	if err := verifyAssetChecksum(dir, tmp, rel); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}

	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return target, nil
}

func restartWithStartScript() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	base := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(base, "scripts", "start_bot.sh"),
		filepath.Join(base, "start_bot.sh"),
		filepath.Join(filepath.Dir(base), "scripts", "start_bot.sh"),
		filepath.Join(filepath.Dir(base), "start_bot.sh"),
	}
	var script string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			script = c
			break
		}
	}
	if script == "" {
		// Standalone or Docker fallback: replace binary directly and exit to let
		// Docker/system restart it. Exiting is only safe once the swap is known
		// to have happened: exiting after a failed rename would bring the old
		// binary back, keep isNewerRelease true and retry forever.
		target, err := updaterTargetPath()
		if err != nil {
			return fmt.Errorf("no restart script and staging path unknown: %w", err)
		}
		if err := os.Rename(target, exe); err != nil {
			return fmt.Errorf("cannot replace %s with the new binary: %w", exe, err)
		}
		if err := os.Chmod(exe, 0o755); err != nil {
			return fmt.Errorf("new binary installed but not made executable: %w", err)
		}
		slog.Info("New binary installed, restart script not found: exiting to let the environment (Docker) restart NASBot")
		os.Exit(0)
		return nil
	}

	// Bounded: runCommand has no deadline of its own, so an unresponsive script
	// would otherwise keep this goroutine alive forever.
	runCtx, cancel := context.WithTimeout(context.Background(), restartCommandTimeout)
	defer cancel()
	return runCommand(runCtx, script, "restart")
}

// notifyUpdaterStatus reports an update progress line: it edits the message
// that triggered the update when there is one, otherwise it sends a new message.
// It returns the message ID that can be edited next, or 0 when nothing was sent.
func notifyUpdaterStatus(ctx *AppContext, bot BotAPI, chatID int64, msgID int, text string) int {
	if msgID > 0 {
		editMessage(bot, chatID, msgID, text, nil)
		return msgID
	}
	if chatID == 0 {
		slog.Warn("Updater has no destination chat: progress not reported", "text", text)
		return 0
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	sent, err := bot.Send(msg)
	if err != nil {
		slog.Error("Updater status message failed, retrying as plain text", "err", sanitizeErr(err))
		msg.ParseMode = ""
		sent, _ = bot.Send(msg)
	}
	return sent.MessageID
}

// applyLatestRelease checks for a new release and applies it. It is the entry
// point used by the /update command and by the "update_apply_latest" callback;
// both already have a chat and a triggering message.
func applyLatestRelease(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	rel, hasUpdate, err := checkForUpdate(ctx)
	if err != nil {
		notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_check_failed"), sanitizeErr(err)))
		return
	}
	applyRelease(ctx, bot, chatID, msgID, rel, hasUpdate)
}

// applyRelease downloads, verifies and installs an already known release.
//
// The caller must hold no updater lock: it is taken here so that the hourly tick
// and a manual click can never apply two updates at once. chatID may be 0 (the
// auto-apply path): it then falls back to the owner, because tgbotapi silently
// fails on chat 0 and an update that dies halfway would leave no trace.
func applyRelease(ctx *AppContext, bot BotAPI, chatID int64, msgID int, rel releaseCandidate, hasUpdate bool) {
	if chatID == 0 {
		chatID = updaterOwnerChatID(ctx)
		if chatID == 0 {
			slog.Warn("No owner chat configured: update progress cannot be reported", "tag", rel.Tag)
		}
	}

	if !hasUpdate {
		notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_none"), Version))
		return
	}
	if os.Getenv("NASBOT_DOCKER") == "true" {
		notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_docker_available"), rel.Tag))
		return
	}

	// The first status line yields the message ID every later line edits.
	msgID = notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_downloading"), rel.Tag, rel.AssetName))

	updaterApplyMu.Lock()
	stagedPath, err := downloadReleaseAsset(rel)
	updaterApplyMu.Unlock()
	if err != nil {
		slog.Error("Update download or verification failed", "tag", rel.Tag, "asset", rel.AssetName, "err", sanitizeErr(err))
		notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_download_failed"), sanitizeErr(err)))
		return
	}

	addPowerLifecycleEvent(ctx, "reboot", false, "command", "scripts/start_bot.sh restart", "post-update-"+rel.Tag)
	markReleaseNotified(ctx, rel.Tag)
	saveState(ctx)

	notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_success"), rel.Tag))

	goSafe("updater-restart", func() {
		time.Sleep(1200 * time.Millisecond)
		if err := restartWithStartScript(); err != nil {
			slog.Error("Update restart failed", "staged", stagedPath, "err", sanitizeErr(err))
			notifyUpdaterStatus(ctx, bot, chatID, msgID, fmt.Sprintf(ctx.Tr("update_restart_failed"), sanitizeErr(err)))
			return
		}
		slog.Info("Update restart handed over to the start script", "tag", rel.Tag)
	})
}
