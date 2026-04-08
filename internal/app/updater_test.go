package app

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestIsNewerRelease(t *testing.T) {
	tests := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "v1.2.0", current: "v1.1.9", want: true},
		{latest: "1.2.0", current: "1.2.0", want: false},
		{latest: "v1.2.0", current: "dev", want: true},
		{latest: "not-a-tag", current: "v1.0.0", want: false},
		{latest: "v1.0.0", current: "v2.0.0", want: false},
	}

	for _, tt := range tests {
		if got := isNewerRelease(tt.latest, tt.current); got != tt.want {
			t.Fatalf("isNewerRelease(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

func TestPickAssetFallsBackToAnyAsset(t *testing.T) {
	rel := githubRelease{TagName: "v1.2.3", Assets: []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}{
		{Name: "something-else", BrowserDownloadURL: "https://example.com/x"},
	}}

	name, url, ok := pickAsset(rel)
	if !ok {
		t.Fatalf("expected fallback asset pick")
	}
	if name != "something-else" || url == "" {
		t.Fatalf("unexpected picked asset: name=%q url=%q", name, url)
	}
}

type mockHTTPClientFunc func(req *http.Request) *http.Response

func (m mockHTTPClientFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req), nil
}

func TestApplyLatestRelease_CapturesMsgID(t *testing.T) {
	oldApp := app
	app = newTestAppContext()
	defer func() { app = oldApp }()

	bot := &fakeBot{}

	app.HTTP = &http.Client{
		Transport: mockHTTPClientFunc(func(req *http.Request) *http.Response {
			if strings.Contains(req.URL.String(), "releases/latest") {
				body := `{"tag_name": "v9.9.9", "assets": [{"name": "nasbot-linux-arm64", "browser_download_url": "http://fake"}]}`
				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(body)),
				}
			}
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(bytes.NewBufferString("fake fail")),
			}
		}),
	}

	applyLatestRelease(app, bot, 123, 0)

	if len(bot.sent) != 2 {
		t.Fatalf("expected exactly 2 messages (1 send, 1 edit), got %d sent", len(bot.sent))
	}

	_, isNewMsg := bot.sent[0].(tgbotapi.MessageConfig)
	if !isNewMsg {
		t.Fatalf("expected first message to be a MessageConfig, got %T", bot.sent[0])
	}

	secondMsg, isEditMsg := bot.sent[1].(tgbotapi.EditMessageTextConfig)
	if !isEditMsg {
		t.Fatalf("expected second message to be an EditMessageTextConfig, got %T", bot.sent[1])
	}

	if secondMsg.MessageID != 1 {
		t.Fatalf("expected edit message to target ID 1, got %d", secondMsg.MessageID)
	}

	expectedPrefix := "Update download failed"
	expectedPrefixIt := "Download update fallito"
	if !strings.Contains(secondMsg.Text, expectedPrefix) && !strings.Contains(secondMsg.Text, expectedPrefixIt) {
		t.Fatalf("expected text to contain download error, but got %q", secondMsg.Text)
	}
}
