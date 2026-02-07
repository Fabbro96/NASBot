package main

import (
	"strings"
	"testing"
	"time"

	"nasbot/internal/format"
)

func TestFormatBytes(t *testing.T) {
	giB := uint64(1024 * 1024 * 1024)
	cases := []struct {
		name  string
		input uint64
		want  string
	}{
		{"zero", 0, "0G"},
		{"oneGiB", giB, "1G"},
		{"oneTiB", giB * 1024, "1T"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := format.FormatBytes(tc.input); got != tc.want {
				t.Fatalf("formatBytes(%d) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 90 * time.Second, "1m30s"},
		{"hours", 1 * time.Hour, "1h0m"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := format.FormatDuration(tc.in); got != tc.want {
				t.Fatalf("formatDuration(%s) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMakeProgressBar(t *testing.T) {
	cases := []struct {
		name string
		in   float64
		want string
	}{
		{"zero", 0, strings.Repeat("░", 10)},
		{"five", 5, "█" + strings.Repeat("░", 9)},
		{"hundred", 100, strings.Repeat("█", 10)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := format.MakeProgressBar(tc.in); got != tc.want {
				t.Fatalf("makeProgressBar(%.1f) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
