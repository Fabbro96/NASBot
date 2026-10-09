package app

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestSplitTelegramMessageSanitizesInvalidUTF8 pins the fuzzer finding:
// non-UTF-8 bytes from kernel logs used to reach Telegram verbatim and get the
// whole report rejected.
func TestSplitTelegramMessageSanitizesInvalidUTF8(t *testing.T) {
	chunks := splitTelegramMessage("ok\xa0\xff broken", 4000, 4, "[truncated]")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !utf8.ValidString(chunks[0]) {
		t.Fatalf("chunk is not valid UTF-8: %q", chunks[0])
	}
	if strings.Contains(chunks[0], "\xa0") {
		t.Fatalf("raw invalid byte survived: %q", chunks[0])
	}
}
