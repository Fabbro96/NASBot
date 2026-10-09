package app

import (
	"fmt"
	"strings"
	"testing"
)

// TestParseMdstatIssuesCapsPathologicalInput pins the fuzzer hardening: a
// header line can legitimately carry several failed devices, but the total is
// capped so the alert always fits a Telegram message.
func TestParseMdstatIssuesCapsPathologicalInput(t *testing.T) {
	var b strings.Builder
	b.WriteString("md0 : active raid1")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, " sd%d[%d](F)", i, i)
	}
	issues := parseMdstatIssues(b.String())
	if len(issues) != maxRaidIssues+1 {
		t.Fatalf("expected %d issues + tail, got %d", maxRaidIssues, len(issues))
	}
	if !strings.Contains(issues[len(issues)-1], "omitted") {
		t.Fatalf("last issue must note the omission, got %q", issues[len(issues)-1])
	}

	healthy := "Personalities : [raid1]\nmd0 : active raid1 sda1[0] sdb1[1]\n      104320 blocks [2/2] [UU]\n"
	if got := parseMdstatIssues(healthy); len(got) != 0 {
		t.Fatalf("healthy array reported %v", got)
	}
	degraded := "md0 : active raid1 sda1[0]\n      104320 blocks [2/1] [U_]\n"
	if got := parseMdstatIssues(degraded); len(got) == 0 {
		t.Fatalf("degraded array reported nothing")
	}
}
