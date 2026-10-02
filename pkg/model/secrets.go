package model

import (
	"regexp"
	"strings"
)

// secretInText matches credentials that ride along inside transport errors.
//
// tgbotapi builds every request URL as "<endpoint>/bot<TOKEN>/<method>", and the
// *url.Error that http.Client returns embeds that URL verbatim: Error() is
// "Post \"https://api.telegram.org/bot123456:AAF.../getMe\": dial tcp ...". So
// every logged Telegram error carries the bot token, and because logging goes to
// stdout as well the token also lands in the systemd journal.
var secretInText = regexp.MustCompile(`(?i)(/bot\d+:[^/\s"]+)|([?&](?:auth|token|api_key|apikey|password)=)[^&\s"]+`)

// SanitizeSecrets masks the credentials embedded in an error or a text.
//
// It lives in pkg/model rather than in internal/app because internal/app imports
// pkg/commands: a sanitizer only internal/app could reach would force
// pkg/commands to carry a second copy of the pattern, and a pattern that drifts
// between two copies silently re-opens the leak in whichever copy was not
// updated. pkg/model is the lowest package both already import.
func SanitizeSecrets(text string) string {
	if text == "" {
		return text
	}
	return secretInText.ReplaceAllStringFunc(text, func(match string) string {
		switch {
		case len(match) > 0 && match[0] == '/':
			return "/bot<token>:redacted"
		case len(match) > 0 && (match[0] == '?' || match[0] == '&'):
			return match[:strings.IndexByte(match, '=')+1] + "redacted"
		default:
			return "redacted"
		}
	})
}

// SanitizeErr returns err with any credential masked, keeping the original in
// the chain so errors.Is/errors.As keep working.
func SanitizeErr(err error) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	masked := SanitizeSecrets(text)
	if masked == text {
		return err
	}
	return sanitizedError{msg: masked, err: err}
}

// sanitizedError is an error whose message is scrubbed but whose cause is kept.
type sanitizedError struct {
	msg string
	err error
}

func (e sanitizedError) Error() string { return e.msg }
func (e sanitizedError) Unwrap() error { return e.err }
