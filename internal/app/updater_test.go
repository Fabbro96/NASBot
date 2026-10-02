package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime"
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

// fakeReleasePayload is the GitHub release JSON the updater tests serve.
//
// It must list every binary the release workflow publishes plus the checksum
// manifest. It listed only "nasbot" once, which made
// TestApplyLatestRelease_CapturesMsgID architecture-dependent:
// preferredReleaseAssets() answers ["nasbot-arm64"] on arm64, pickAsset found
// nothing, fetchLatestRelease failed before the download started and the test
// saw one message where it asserts two.
const fakeReleasePayload = `{"tag_name": "v9.9.9", "assets": [` +
	`{"name": "nasbot", "browser_download_url": "http://fake"}, ` +
	`{"name": "nasbot-arm64", "browser_download_url": "http://fake"}, ` +
	`{"name": "SHA256SUMS.txt", "browser_download_url": "http://fake"}]}`

// publishedReleaseBinaries are the binaries .github/workflows/release.yml
// builds. Every architecture the bot is released on must find its own name here.
var publishedReleaseBinaries = []string{"nasbot", "nasbot-arm64"}

func TestApplyLatestRelease_CapturesMsgID(t *testing.T) {
	oldApp := app
	app = newTestAppContext()
	defer func() { app = oldApp }()
	installTempState(t)

	bot := &fakeBot{}

	app.HTTP = &http.Client{
		Transport: mockHTTPClientFunc(func(req *http.Request) *http.Response {
			if strings.Contains(req.URL.String(), "releases/latest") {
				body := fakeReleasePayload
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

// TestFakeReleasePayloadCoversEveryReleaseArchitecture is the guard against the
// regression the payload comment describes: a fixture that drops a binary makes
// the updater tests fail on that architecture only, so they are green on the
// developer laptop and red on the release runner.
func TestFakeReleasePayloadCoversEveryReleaseArchitecture(t *testing.T) {
	var rel githubRelease
	if err := json.Unmarshal([]byte(fakeReleasePayload), &rel); err != nil {
		t.Fatalf("the release fixture is not valid JSON: %v", err)
	}

	for _, name := range publishedReleaseBinaries {
		if pickAssetURL(rel, name) == "" {
			t.Errorf("the release fixture does not publish %q, so every test that serves "+
				"it fails on the architecture that needs that binary", name)
		}
	}
	if pickAssetURL(rel, checksumsAssetName) == "" {
		t.Errorf("the release fixture does not publish %q, so the checksum verification "+
			"path is never reached by the updater tests", checksumsAssetName)
	}

	// And the binary this architecture actually asks for must be there.
	wanted := preferredReleaseAssets()
	if len(wanted) == 0 {
		t.Logf("no release binary is published for %s, which is a deliberate refusal", runtime.GOARCH)
		return
	}
	if _, _, ok := pickAsset(rel); !ok {
		t.Errorf("pickAsset found nothing for %s (it wants %v) in the release fixture",
			runtime.GOARCH, wanted)
	}
}
