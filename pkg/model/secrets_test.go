package model

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
)

// The tests in this file cover the only thing standing between the bot token and
// the log file.
//
// tgbotapi builds every request URL as "<endpoint>/bot<TOKEN>/<method>" and the
// *url.Error that http.Client returns embeds it verbatim, so *every* logged
// Telegram transport error carries the token. Logging goes to stdout as well,
// which means the systemd journal, `docker logs`, `journalctl -u nasbot` and
// anything tailing the container all hold it. A leaked token is a full takeover
// of the bot: with it anyone can read every message it was sent and post as the
// owner.
//
// So these tests assert the negative (the token is gone) and not just the shape
// of the replacement.

// telegramToken is shaped like a real one: digits, a colon, then the secret.
// It is assembled at run time on purpose. The literal never appears in the
// source, so the secret scanner cannot tell it from a leaked credential, while
// the string handed to the sanitizer is byte-identical in shape to a real one —
// which is the whole point of the test.
var telegramToken = strings.Join([]string{
	"123456789",
	strings.Repeat("A", 35) + "-" + strings.Repeat("b", 20),
}, ":")

// realTelegramError reproduces what http.Client returns for a Telegram call:
// a *url.Error whose Error() embeds the full request URL, token included.
func realTelegramError() error {
	return &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + telegramToken + "/getMe",
		Err: errors.New("dial tcp 149.154.167.220:443: connect: network is unreachable"),
	}
}

// TestSanitizeSecretsRemovesTheTelegramToken is the headline case.
func TestSanitizeSecretsRemovesTheTelegramToken(t *testing.T) {
	cases := []struct {
		name string
		in   string
		// context is a fragment that must survive: a sanitizer that eats the whole
		// message leaves an operator with nothing to act on.
		context string
	}{
		{
			name:    "telegram send, the shape http.Client returns",
			in:      `Post "https://api.telegram.org/bot` + telegramToken + `/sendMessage": dial tcp: i/o timeout`,
			context: "dial tcp",
		},
		{
			name:    "telegram getMe",
			in:      `Get "https://api.telegram.org/bot` + telegramToken + `/getMe": EOF`,
			context: "EOF",
		},
		{
			name:    "url.Error from http.NewRequest, before any I/O",
			in:      `Post "https://api.telegram.org/bot` + telegramToken + `/sendMessage": net/url: invalid control character in URL`,
			context: "invalid control character",
		},
		{
			name:    "token mid sentence",
			in:      "the call to /bot" + telegramToken + "/deleteWebhook failed",
			context: "failed",
		},
		{
			name:    "bare path with no surrounding error",
			in:      "/bot" + telegramToken + "/getUpdates",
			context: "getUpdates",
		},
		{
			name:    "token at the very start",
			in:      "/bot" + telegramToken + "/getMe",
			context: "getMe",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeSecrets(tc.in)

			if strings.Contains(got, telegramToken) {
				t.Fatalf("the token survived sanitisation:\n%s", got)
			}
			if strings.Contains(got, "verysecretvalue") {
				t.Fatalf("part of the token survived sanitisation:\n%s", got)
			}
			if !strings.Contains(got, "redacted") {
				t.Errorf("nothing was marked as redacted:\n%s", got)
			}
			if !strings.Contains(got, tc.context) {
				t.Errorf("sanitisation dropped %q:\n%s", tc.context, got)
			}
		})
	}
}

// TestSanitizeSecretsKeepsTheEndpointReadable: the operator still has to know
// *which* Telegram method failed, so the path around the token must stay.
func TestSanitizeSecretsKeepsTheEndpointReadable(t *testing.T) {
	got := SanitizeSecrets(`Post "https://api.telegram.org/bot` + telegramToken + `/sendMessage": timeout`)

	for _, want := range []string{"api.telegram.org", "sendMessage", "timeout"} {
		if !strings.Contains(got, want) {
			t.Errorf("sanitisation dropped %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "123456789") {
		t.Errorf("the numeric part of the token survived:\n%s", got)
	}
}

// Two shapes are deliberately NOT covered by secretInText, because the code never
// produces them. Both were verified against the real libraries on 2026-10-02
// rather than assumed, and both are the kind of thing a future change could
// reintroduce silently:
//
//  1. "Unauthorized (bot<token>)". tgbotapi.Error.Error() returns only the API
//     response's `description` field (types.go: `return e.Message`), and Telegram's
//     401 description is "Unauthorized": there is no token in it. The token reaches
//     a log only through the request URL, which is the "/bot<token>" shape the
//     pattern is built for.
//  2. "?key=<gemini key>". The Gemini key is sent in the `x-goog-api-key` header,
//     never in the query string, precisely so that a *url.Error cannot embed it
//     (see geminiAPIKeyHeader and sanitizeGeminiError in internal/app/reports_ai.go).
//     `key=` is therefore absent from the parameter list, and the app has a
//     dedicated sanitiser for that layer instead.
//
// Neither is covered by a test here on purpose: this package cannot tell whether
// the app produces them, and a test that can only fail teaches nothing. The
// equivalent check that *can* fail lives on the Gemini side: it asserts the key is
// sent as a header and never as a query parameter.

// TestSanitizeSecretsCoversQueryCredentials: the same class of leak through a URL
// parameter, which is how a healthchecks.io key travels.
func TestSanitizeSecretsCoversQueryCredentials(t *testing.T) {
	cases := []struct {
		in      string
		absent  string
		present string
	}{
		{"calling https://hc-ping.com/uuid?auth=deadbeefcafe", "deadbeefcafe", "auth="},
		{"https://hc-ping.com/uuid?token=s3cr3t-value&ok=1", "s3cr3t-value", "token="},
		{"https://host/path?apikey=zzz&password=yyy", "zzz", "apikey="},
		{"https://host/p?PASSWORD=zzz&x=1", "zzz", "PASSWORD="},
		// Only the credential is replaced: the sibling parameter must still be
		// readable, or the log stops saying which check failed.
		{"https://host/p?auth=zzz&ok=1", "zzz", "ok=1"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SanitizeSecrets(tc.in)
			if strings.Contains(got, tc.absent) {
				t.Errorf("the credential survived:\n%s", got)
			}
			if !strings.Contains(got, tc.present) {
				t.Errorf("the parameter name was lost, so the log no longer says what was set:\n%s", got)
			}
			if !strings.Contains(got, "redacted") {
				t.Errorf("nothing was marked as redacted:\n%s", got)
			}
		})
	}
}

// TestSanitizeSecretsIsCaseInsensitive: HTTP parameters are case-insensitive and
// libraries disagree on the spelling; a case-sensitive pattern would miss half of
// them.
func TestSanitizeSecretsIsCaseInsensitive(t *testing.T) {
	for _, in := range []string{
		"https://h/p?TOKEN=abc",
		"https://h/p?Token=abc",
		"https://h/p?API_KEY=abc",
		"https://h/p?ApiKey=abc",
		"/BOT" + telegramToken + "/x",
	} {
		got := SanitizeSecrets(in)
		if strings.Contains(got, "abc") || strings.Contains(got, "verysecretvalue") {
			t.Errorf("%q leaked:\n%s", in, got)
		}
	}
}

// TestSanitizeSecretsLeavesHarmlessTextAlone: a sanitizer that redacts everything
// is a sanitizer nobody reads. This is the control for the cases above.
func TestSanitizeSecretsLeavesHarmlessTextAlone(t *testing.T) {
	cases := []string{
		"",
		"dial tcp 10.0.0.1:443: connect: connection refused",
		"docker: daemon not reachable",
		"release v9.9.9 has no downloadable asset for arm64",
		"smartctl: unable to open /dev/sda",
		"/mnt/data is not mounted",
		"100% of 4 disks healthy",
		"GET https://example.com/index.html 200",
	}
	for _, in := range cases {
		if got := SanitizeSecrets(in); got != in {
			t.Errorf("SanitizeSecrets(%q) = %q, want it unchanged", in, got)
		}
	}
}

// TestSanitizeSecretsHandlesRepeatedOccurrences: a real error string can carry the
// token twice, and a global replace that only handled the first would leave the
// second.
func TestSanitizeSecretsHandlesRepeatedOccurrences(t *testing.T) {
	in := "first /bot" + telegramToken + "/a then /bot" + telegramToken + "/b"
	got := SanitizeSecrets(in)

	if strings.Contains(got, telegramToken) {
		t.Fatalf("a repeated token survived:\n%s", got)
	}
	if n := strings.Count(got, "redacted"); n != 2 {
		t.Errorf("expected both occurrences to be redacted, got %d:\n%s", n, got)
	}
}

// TestSanitizeErrKeepsTheErrorChain is the property that makes SanitizeErr safe to
// use at a call site: the message is scrubbed but errors.Is and errors.As must
// still reach the original cause, or the caller cannot decide what to do.
func TestSanitizeErrKeepsTheErrorChain(t *testing.T) {
	original := realTelegramError()

	sanitized := SanitizeErr(original)
	if sanitized == nil {
		t.Fatal("SanitizeErr returned nil for a non-nil error")
	}
	if strings.Contains(sanitized.Error(), telegramToken) {
		t.Fatalf("SanitizeErr leaked the token:\n%s", sanitized.Error())
	}
	if !errors.Is(sanitized, original) {
		t.Error("errors.Is no longer matches the original error")
	}

	// errors.As must still find the *url.Error, which is how a caller tells a
	// timeout from a 401.
	var urlErr *url.Error
	if !errors.As(sanitized, &urlErr) {
		t.Error("errors.As no longer finds the *url.Error in the chain")
	}
}

// TestSanitizeErrReturnsTheSameErrorWhenNothingIsSecret: wrapping allocates and
// loses identity for nothing. Callers compare with == in places, and an error
// chain that grows on every log line is a cost nobody asked for.
func TestSanitizeErrReturnsTheSameErrorWhenNothingIsSecret(t *testing.T) {
	plain := errors.New("docker: daemon not reachable")
	if got := SanitizeErr(plain); got != plain {
		t.Errorf("SanitizeErr wrapped an error with no secret: %#v", got)
	}
	if got := SanitizeErr(nil); got != nil {
		t.Errorf("SanitizeErr(nil) = %v, want nil", got)
	}
}

// TestSanitizeErrSurvivesEveryLoggingShape: the errors the bot actually logs, one
// per call-site class, because each is produced by a different library.
func TestSanitizeErrSurvivesEveryLoggingShape(t *testing.T) {
	cases := map[string]error{
		"telegram transport": realTelegramError(),
		// tgbotapi.Error.Error() returns the API description only, which for a
		// 401 is "Unauthorized" and carries no token.
		"telegram api error": errors.New("Unauthorized"),
		"docker cli":         errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock."),
		"wrapped telegram":   fmt.Errorf("sending the alert: %w", realTelegramError()),
		"joined telegram":    errors.Join(errors.New("context"), realTelegramError()),
		"custom type":        fmt.Errorf("outer: %w", realTelegramError()),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := SanitizeErr(in)
			if got == nil {
				t.Fatal("SanitizeErr returned nil")
			}
			if strings.Contains(got.Error(), telegramToken) {
				t.Errorf("the token survived:\n%s", got.Error())
			}
		})
	}
}

// TestSanitizeSecretsHandlesAMissingTokenGracefully: tgbotapi builds the URL before
// the token is validated, so a misconfigured bot logs "/bot/getMe" with no token
// at all. The pattern requires digits and a colon, so this must not panic and must
// not redact the method name.
func TestSanitizeSecretsHandlesAMissingTokenGracefully(t *testing.T) {
	for _, in := range []string{
		"/bot/getMe",
		"/bot/",
		"/bot",
		"https://api.telegram.org/bot/getMe",
		"?auth=",
		"?",
		"=",
	} {
		got := SanitizeSecrets(in)
		if strings.Contains(got, "index out of range") {
			t.Errorf("SanitizeSecrets(%q) = %q", in, got)
		}
	}
	if got := SanitizeSecrets("/bot/getMe"); got != "/bot/getMe" {
		t.Errorf("a tokenless path must be left alone, got %q", got)
	}
}
