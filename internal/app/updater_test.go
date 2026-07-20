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
				body := `{"tag_name": "v9.9.9", "assets": [{"name": "nasbot", "browser_download_url": "http://fake"}]}`
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

	applyLatestRelease(app, bot, 0, 0)

	if len(bot.sent) != 1 {
		t.Fatalf("expected exactly 1 message, got %d sent", len(bot.sent))
	}

	firstMsg, isNewMsg := bot.sent[0].(tgbotapi.MessageConfig)
	if !isNewMsg {
		t.Fatalf("expected first message to be a MessageConfig, got %T", bot.sent[0])
	}

	if !strings.Contains(firstMsg.Text, "Docker") {
		t.Fatalf("expected text to contain Docker, but got %q", firstMsg.Text)
	}
}

func TestParseSemverTag(t *testing.T) {
	tests := []struct {
		tag      string
		expected [3]int
		valid    bool
	}{
		{"v1.2.3", [3]int{1, 2, 3}, true},
		{"1.2.3", [3]int{1, 2, 3}, true},
		{"V1.0", [3]int{1, 0, 0}, true},
		{"v1", [3]int{1, 0, 0}, true},
		{"1.x", [3]int{1, 0, 0}, true},
		{"v2.0.0-beta", [3]int{2, 0, 0}, true},
		{"v0.10.1", [3]int{0, 10, 1}, true},
		{"v1.2.3.4", [3]int{1, 2, 3}, true},
		{"random", [3]int{0, 0, 0}, false},
	}

	for _, tc := range tests {
		got, ok := parseSemverTag(tc.tag)
		if ok != tc.valid {
			t.Errorf("parseSemverTag(%q) valid = %v, want %v", tc.tag, ok, tc.valid)
		}
		if got != tc.expected {
			t.Errorf("parseSemverTag(%q) = %v, want %v", tc.tag, got, tc.expected)
		}
	}
}
