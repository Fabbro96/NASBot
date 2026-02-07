package main

import "testing"

func TestTruncatePath(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		maxLen int
		want   string
	}{
		{"short", "/a/b", 10, "/a/b"},
		{"twoParts", "/ab", 4, "/..."},
		{"long", "/a/b/c/d/e", 12, "...c/d/e"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncatePath(tc.path, tc.maxLen); got != tc.want {
				t.Fatalf("truncatePath(%q,%d) = %q, want %q", tc.path, tc.maxLen, got, tc.want)
			}
		})
	}
}
