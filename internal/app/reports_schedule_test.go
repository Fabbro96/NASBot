package app

import (
	"testing"
	"time"
)

func TestGetNextReportTime_DaysOfWeek(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Rome")
	ctx := newTestAppContext()
	ctx.State.TimeLocation = loc

	// Enable reports on Mondays (1) and Thursdays (4) at 08:00
	ctx.Settings.ReportsEnabled = true
	ctx.Settings.ReportTimes = []TimePoint{{Hour: 8, Minute: 0}}
	ctx.Settings.ReportDays = []int{1, 4} // Mon, Thu

	nextReport, tp := getNextReportTime(ctx)
	if tp.Hour != 8 || tp.Minute != 0 {
		t.Fatalf("expected timepoint 08:00, got %02d:%02d", tp.Hour, tp.Minute)
	}

	weekday := int(nextReport.Weekday())
	if weekday != 1 && weekday != 4 {
		t.Fatalf("expected next report on Monday (1) or Thursday (4), got weekday %d (%s)", weekday, nextReport.Weekday())
	}
}

func TestGetNextReportTime_Interval(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Rome")
	ctx := newTestAppContext()
	ctx.State.TimeLocation = loc

	// Interval mode (empty ReportDays)
	ctx.Settings.ReportsEnabled = true
	ctx.Settings.ReportInterval = 2
	ctx.Settings.ReportDays = []int{}
	ctx.Settings.ReportTimes = []TimePoint{{Hour: 7, Minute: 30}, {Hour: 18, Minute: 30}}

	nextReport, tp := getNextReportTime(ctx)
	if (tp.Hour != 7 || tp.Minute != 30) && (tp.Hour != 18 || tp.Minute != 30) {
		t.Fatalf("expected timepoint 07:30 or 18:30, got %02d:%02d", tp.Hour, tp.Minute)
	}

	if nextReport.IsZero() {
		t.Fatalf("expected valid next report time")
	}
}

func TestGetNextReportDescription(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Settings.ReportsEnabled = false
	desc := getNextReportDescription(ctx)
	if desc != ctx.Tr("report_disabled") {
		t.Fatalf("expected %q, got %q", ctx.Tr("report_disabled"), desc)
	}

	ctx.Settings.ReportsEnabled = true
	ctx.Settings.ReportTimes = []TimePoint{{Hour: 12, Minute: 0}}
	desc = getNextReportDescription(ctx)
	if desc == "" {
		t.Fatalf("expected non-empty description for enabled reports")
	}
}

func TestFormatDaysList(t *testing.T) {
	ctx := newTestAppContext()
	ctx.Settings.SetLanguage("it")

	all := formatDaysList(ctx, []int{})
	if all != ctx.Tr("report_preset_all") {
		t.Fatalf("expected all days preset, got %q", all)
	}

	workdays := formatDaysList(ctx, []int{1, 2, 3, 4, 5})
	if workdays != "Lun, Mar, Mer, Gio, Ven" {
		t.Fatalf("expected 'Lun, Mar, Mer, Gio, Ven', got %q", workdays)
	}
}
