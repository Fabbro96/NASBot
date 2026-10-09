package app

import (
	"testing"
	"time"
)

// Regression test for the "report every 10 minutes" bug: sleepReportWake wakes
// up on the 10-minute poll ceiling (or a settings change) well before the slot.
// That wake-up must NOT be treated as an expired timer.
func TestReportSlotDue_PollWakeIsNotDue(t *testing.T) {
	// Slot 1h in the future (e.g. woken by the 10-min poll ceiling).
	if reportSlotDue(time.Now().Add(1 * time.Hour)) {
		t.Fatalf("slot 1h in the future must not be due (this was the 10-min report spam bug)")
	}
	// Slot 10 minutes in the future: still a poll wake-up, not due.
	if reportSlotDue(time.Now().Add(10 * time.Minute)) {
		t.Fatalf("slot 10m in the future must not be due")
	}
}

func TestReportSlotDue_TimerExpiryIsDue(t *testing.T) {
	// Timer just expired.
	if !reportSlotDue(time.Now()) {
		t.Fatalf("slot right now must be due")
	}
	// Grace-period / slightly overdue slot.
	if !reportSlotDue(time.Now().Add(-time.Minute)) {
		t.Fatalf("overdue slot must be due")
	}
	// Jitter: a few seconds early still counts as due so a report is not
	// skipped because the timer fired marginally early.
	if !reportSlotDue(time.Now().Add(5 * time.Second)) {
		t.Fatalf("slot a few seconds away must be due (jitter tolerance)")
	}
}

// TestGetNextReportTimeSurvivesClockSkew: with LastReport in the future (NTP
// jump, manual clock change) the scheduler must return a future slot, never a
// past one that would fire immediately in a tight loop.
func TestGetNextReportTimeSurvivesClockSkew(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Settings.ReportsEnabled = true
	ctx.Settings.ReportInterval = 1
	ctx.Settings.ReportTimes = []TimePoint{{Hour: 7, Minute: 30}}
	ctx.State.Mu.Lock()
	ctx.State.LastReport = time.Now().Add(2 * time.Hour)
	ctx.State.Mu.Unlock()

	next, _ := getNextReportTime(ctx)
	if time.Until(next) < -reportDueTolerance {
		t.Fatalf("skewed LastReport produced an overdue slot %s (loop risk)", next)
	}
}
