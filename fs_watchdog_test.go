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
		{"maxZero", "/a/b", 0, ""},
		{"emptyPath", "", 5, ""},
		{"maxThreeAbs", "/abcdef", 3, "/.."},
		{"maxFourRel", "abcdef", 4, "...."},
		{"maxOneRel", "abcdef", 1, "."},
		{"maxTwoRel", "abcdef", 2, ".."},
		{"rootOne", "/", 1, "/"},
		{"rootTwo", "/", 2, "/."},
		{"rootFour", "/", 4, "/..."},
		{"exactLen", "/a/b/c", 6, "/a/b/c"},
		{"deepLong", "/a/b/c/d/e/f", 10, "...d/e/f"},
		{"deepTight", "/a/b/c/d/e/f", 8, "...d/e/f"},
		{"deepTighter", "/a/b/c/d/e/f", 7, "...d..."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncatePath(tc.path, tc.maxLen); got != tc.want {
				t.Fatalf("truncatePath(%q,%d) = %q, want %q", tc.path, tc.maxLen, got, tc.want)
			}
		})
	}
}
