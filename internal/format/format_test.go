package format

import "testing"

func TestFormatPeriod(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{45, "45 seconds"},
		{60, "1 minute"},
		{120, "2 minutes"},
		{3600, "1 hour"},
		{7200, "2 hours"},
	}

	for _, tc := range cases {
		if got := FormatPeriod(tc.in); got != tc.want {
			t.Fatalf("FormatPeriod(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTitleCaseWord(t *testing.T) {
	if got := TitleCaseWord("  heLLo "); got != "Hello" {
		t.Fatalf("TitleCaseWord = %q, want %q", got, "Hello")
	}
	if got := TitleCaseWord(" "); got != "" {
		t.Fatalf("TitleCaseWord(blank) = %q, want %q", got, "")
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcd", 4); got != "abcd" {
		t.Fatalf("Truncate = %q, want %q", got, "abcd")
	}
	if got := Truncate("abcdef", 4); got != "abc~" {
		t.Fatalf("Truncate = %q, want %q", got, "abc~")
	}
	if got := Truncate("abcdef", 0); got != "" {
		t.Fatalf("Truncate(0) = %q, want %q", got, "")
	}
	if got := Truncate("abcdef", -1); got != "" {
		t.Fatalf("Truncate(-1) = %q, want %q", got, "")
	}
	if got := Truncate("abcdef", 1); got != "~" {
		t.Fatalf("Truncate(1) = %q, want %q", got, "~")
	}
	// UTF-8 multibyte strings & emojis
	if got := Truncate("🚀🚨📊🖥💿🗄", 4); got != "🚀🚨📊~" {
		t.Fatalf("Truncate UTF-8 = %q, want %q", got, "🚀🚨📊~")
	}
	if got := Truncate("città", 4); got != "cit~" {
		t.Fatalf("Truncate UTF-8 accent = %q, want %q", got, "cit~")
	}
}
