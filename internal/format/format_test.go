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
}
