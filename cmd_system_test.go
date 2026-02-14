package main

import "testing"

func TestIsForceRebootArg(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{in: "force", want: true},
		{in: " FORCE ", want: true},
		{in: "-f", want: true},
		{in: "--force", want: true},
		{in: "now", want: true},
		{in: "", want: false},
		{in: "reboot", want: false},
		{in: "force please", want: false},
	}

	for _, tt := range tests {
		if got := isForceRebootArg(tt.in); got != tt.want {
			t.Fatalf("isForceRebootArg(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
