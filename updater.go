package main

import (
	"context"
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
)

var Version = "dev"

const (
	defaultGitHubRepo = "Fabbro96/NASBot"
	releaseCheckEvery = 6 * time.Hour
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type releaseCandidate struct {
	Tag       string
	URL       string
	AssetName string
	AssetURL  string
}

func updaterRepo() string {
	repo := strings.TrimSpace(os.Getenv("NASBOT_GITHUB_REPO"))
	if repo == "" {
		return defaultGitHubRepo
	}
	return repo
}

func parseSemverTag(tag string) ([3]int, bool) {
	t := strings.TrimSpace(strings.ToLower(tag))
	t = strings.TrimPrefix(t, "v")
	parts := strings.SplitN(t, "-", 2)
	base := parts[0]
	segs := strings.Split(base, ".")
	if len(segs) != 3 {
		return [3]int{}, false
	}
	maj, err1 := strconv.Atoi(segs[0])
	min, err2 := strconv.Atoi(segs[1])
	pat, err3 := strconv.Atoi(segs[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return [3]int{}, false
	}
	return [3]int{maj, min, pat}, true
}

func isNewerRelease(latestTag, currentVersion string) bool {
	latest, okLatest := parseSemverTag(latestTag)
	current, okCurrent := parseSemverTag(currentVersion)
	if !okLatest {
		return false
	}
	if !okCurrent {
		return true
	}
	for i := 0; i < 3; i++ {
		if latest[i] > current[i] {
			return true
		}
		if latest[i] < current[i] {
			return false
		}
	}
	return false
}

func preferredReleaseAssets() []string {
	if runtime.GOARCH == "arm64" {
		return []string{"nasbot-arm64", "nasbot-update-arm64", "nasbot"}
	}
	return []string{"nasbot", "nasbot-amd64", "nasbot-arm64"}
}

func pickAsset(rel githubRelease) (name, url string, ok bool) {
	for _, wanted := range preferredReleaseAssets() {
		for _, a := range rel.Assets {
			if a.Name == wanted {
				return a.Name, a.BrowserDownloadURL, true
			}
		}
	}
	for _, a := range rel.Assets {
		if a.BrowserDownloadURL != "" {
			return a.Name, a.BrowserDownloadURL, true
		}
	}
	return "", "", false
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
		return releaseCandidate{}, fmt.Errorf("no downloadable release assets found for %s", runtime.GOARCH)
	}

	return releaseCandidate{Tag: rel.TagName, URL: rel.HTMLURL, AssetName: assetName, AssetURL: assetURL}, nil
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
	ctx.State.mu.Lock()
	if ctx.State.LastReleaseNotified == rel.Tag {
		ctx.State.mu.Unlock()
		return
	}
	ctx.State.mu.Unlock()

	text := fmt.Sprintf("🆕 Nuova versione disponibile: *%s*\nVersione attuale: *%s*\nAsset per questa architettura: `%s`", rel.Tag, Version, rel.AssetName)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬇️ Aggiorna ora", "update_apply_latest"),
			tgbotapi.NewInlineKeyboardButtonURL("📦 Release", rel.URL),
		),
	)
	msg := tgbotapi.NewMessage(ctx.Config.AllowedUserID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = kb
	if _, err := bot.Send(msg); err != nil {
		slog.Error("Release update notification failed", "err", err)
		msg.ParseMode = ""
		safeSend(bot, msg)
		return
	}

	ctx.State.mu.Lock()
	ctx.State.LastReleaseNotified = rel.Tag
	ctx.State.mu.Unlock()
	saveState(ctx)
}

func updaterLoop(ctx *AppContext, bot BotAPI, runCtx context.Context) {
	if !sleepWithContext(runCtx, 30*time.Second) {
		return
	}

	for {
		rel, hasUpdate, err := checkForUpdate(ctx)
		if err != nil {
			slog.Warn("Update check failed", "err", err)
		} else if hasUpdate {
			notifyUpdateAvailable(ctx, bot, rel)
		}

		if !sleepWithContext(runCtx, releaseCheckEvery) {
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

func downloadReleaseAsset(ctx *AppContext, rel releaseCandidate) (string, error) {
	reqCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rel.AssetURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "nasbot-updater")

	client := http.DefaultClient
	if ctx != nil && ctx.HTTP != nil {
		client = ctx.HTTP
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("download failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	target, err := updaterTargetPath()
	if err != nil {
		return "", err
	}
	tmp := target + ".tmp"

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, target); err != nil {
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
	}
	var script string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			script = c
			break
		}
	}
	if script == "" {
		return fmt.Errorf("start script not found in expected paths")
	}
	return runCommand(context.Background(), script, "restart")
}

func applyLatestRelease(ctx *AppContext, bot BotAPI, chatID int64, msgID int) {
	rel, hasUpdate, err := checkForUpdate(ctx)
	if err != nil {
		errText := fmt.Sprintf("❌ Update check failed: %v", err)
		if msgID > 0 {
			editMessage(bot, chatID, msgID, errText, nil)
		} else {
			sendMarkdown(bot, chatID, errText)
		}
		return
	}
	if !hasUpdate {
		text := fmt.Sprintf("✅ Nessun update disponibile. Versione corrente: *%s*", Version)
		if msgID > 0 {
			editMessage(bot, chatID, msgID, text, nil)
		} else {
			sendMarkdown(bot, chatID, text)
		}
		return
	}

	statusText := fmt.Sprintf("⏳ Download update %s (%s) in corso...", rel.Tag, rel.AssetName)
	if msgID > 0 {
		editMessage(bot, chatID, msgID, statusText, nil)
	} else {
		sendMarkdown(bot, chatID, statusText)
	}

	if _, err := downloadReleaseAsset(ctx, rel); err != nil {
		errText := fmt.Sprintf("❌ Download update fallito: %v", err)
		if msgID > 0 {
			editMessage(bot, chatID, msgID, errText, nil)
		} else {
			sendMarkdown(bot, chatID, errText)
		}
		return
	}
	addPowerLifecycleEvent(ctx, "reboot", false, "command", "scripts/start_bot.sh restart", "post-update-"+rel.Tag)
	saveState(ctx)

	okText := fmt.Sprintf("✅ Update %s scaricato. Riavvio NASBot in corso...", rel.Tag)
	if msgID > 0 {
		editMessage(bot, chatID, msgID, okText, nil)
	} else {
		sendMarkdown(bot, chatID, okText)
	}

	go func() {
		time.Sleep(1200 * time.Millisecond)
		if err := restartWithStartScript(); err != nil {
			slog.Error("Update restart failed", "err", err)
			sendMarkdown(bot, chatID, fmt.Sprintf("❌ Restart post-update fallito: %v", err))
		}
	}()
}
