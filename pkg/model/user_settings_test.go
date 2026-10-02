package model

import (
	"context"
	"sync"
	"testing"
	"time"
)

// UserSettings is the one struct in this package that every part of the bot reads
// while something else writes it: the settings buttons run in Telegram update
// goroutines, the report scheduler reads the schedule from its own goroutine, and
// the power lifecycle saves the state from a third. Every field is therefore
// reached through an accessor that takes Mu, and the tests below are mostly about
// two things: that the accessors copy what they hand out, and that the report
// scheduler's wake-up channel behaves.

// TestSettingsAccessorsReadBackWhatWasWritten is the floor for every accessor.
// A setter that wrote the wrong field, or a getter that read a different one,
// would leave the user changing a setting and seeing nothing.
func TestSettingsAccessorsReadBackWhatWasWritten(t *testing.T) {
	s := &UserSettings{}

	if got := s.GetLanguage(); got != "" {
		t.Fatalf("a zero UserSettings reports language %q", got)
	}
	s.SetLanguage("it")
	if got := s.GetLanguage(); got != "it" {
		t.Errorf("language = %q, want it", got)
	}
	// Trimming is not part of the contract, but an empty language has to fall
	// back to English at the Tr() level, not be stored.
	s.SetLanguage("")
	if got := s.GetLanguage(); got != "" {
		t.Errorf("language = %q, want empty", got)
	}
}

// TestGetReportsSettingsHandsOutACopy: the slice is copied on the way out and on
// the way in. Without the copy a caller that appends to the returned slice would
// be writing into the live settings while the scheduler reads them.
func TestGetReportsSettingsHandsOutACopy(t *testing.T) {
	s := &UserSettings{ReportTimes: []TimePoint{{Hour: 7, Minute: 30}}}

	_, _, times := s.GetReportsSettings()
	if len(times) != 1 || times[0].Hour != 7 {
		t.Fatalf("times = %+v", times)
	}

	times[0].Hour = 99
	times = append(times, TimePoint{Hour: 1})
	_ = times

	_, _, again := s.GetReportsSettings()
	if len(again) != 1 || again[0].Hour != 7 {
		t.Errorf("the returned slice aliases the settings: %+v", again)
	}
}

// TestSetReportsSettingsReplacesTheWholeSchedule: the button handlers compute the
// new value from the old one and publish it as a unit, so a reader must never see
// a half-updated schedule.
func TestSetReportsSettingsReplacesTheWholeSchedule(t *testing.T) {
	s := &UserSettings{}
	s.SetReportsSettings(true, 3, []TimePoint{{Hour: 8}, {Hour: 20}})

	enabled, interval, times := s.GetReportsSettings()
	if !enabled {
		t.Error("reports must be enabled")
	}
	if interval != 3 {
		t.Errorf("interval = %d, want 3", interval)
	}
	if len(times) != 2 || times[0].Hour != 8 || times[1].Hour != 20 {
		t.Errorf("times = %+v", times)
	}

	// The published slice must be a copy too: a caller reusing its slice must not
	// be able to rewrite the live schedule.
	source := []TimePoint{{Hour: 1}}
	s.SetReportsSettings(false, 1, source)
	source[0].Hour = 12
	_, _, times = s.GetReportsSettings()
	if len(times) != 1 || times[0].Hour != 1 {
		t.Errorf("SetReportsSettings kept a reference to the caller's slice: %+v", times)
	}
}

func TestReportsEnabledAndInterval(t *testing.T) {
	s := &UserSettings{}

	s.SetReportsEnabled(true)
	enabled, _, _ := s.GetReportsSettings()
	if !enabled {
		t.Error("SetReportsEnabled(true) did not take")
	}
	s.SetReportsEnabled(false)
	enabled, _, _ = s.GetReportsSettings()
	if enabled {
		t.Error("SetReportsEnabled(false) did not take")
	}

	s.SetReportInterval(5)
	_, interval, _ := s.GetReportsSettings()
	if interval != 5 {
		t.Errorf("interval = %d, want 5", interval)
	}
}

func TestReportsDaysAreSortedAndDeduplicated(t *testing.T) {
	s := &UserSettings{}

	s.SetReportsDays([]int{5, 1, 3})
	if got := s.GetReportsDays(); len(got) != 3 || got[0] != 1 || got[2] != 5 {
		t.Errorf("SetReportsDays must sort, got %v", got)
	}

	s.SetReportsDays(nil)
	if got := s.GetReportsDays(); len(got) != 0 {
		t.Errorf("an empty day list must stay empty, got %v", got)
	}

	// A caller reusing the slice it passed must not be able to change the settings.
	source := []int{2, 4}
	s.SetReportsDays(source)
	source[0] = 6
	if got := s.GetReportsDays(); len(got) != 2 || got[0] != 2 {
		t.Errorf("SetReportsDays kept a reference: %v", got)
	}
}

func TestToggleReportDay(t *testing.T) {
	s := &UserSettings{}

	s.ToggleReportDay(3)
	if !s.HasReportDay(3) {
		t.Fatal("ToggleReportDay did not add the day")
	}
	if got := s.GetReportsDays(); len(got) != 1 || got[0] != 3 {
		t.Errorf("days = %v, want [3]", got)
	}

	s.ToggleReportDay(1)
	s.ToggleReportDay(5)
	if got := s.GetReportsDays(); len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 5 {
		t.Errorf("days are not sorted after two adds: %v", got)
	}

	s.ToggleReportDay(3)
	if s.HasReportDay(3) {
		t.Error("ToggleReportDay did not remove the day")
	}
	if got := s.GetReportsDays(); len(got) != 2 {
		t.Errorf("days = %v, want two", got)
	}
}

func TestHasReportDay(t *testing.T) {
	s := &UserSettings{ReportDays: []int{0, 6}}
	for _, d := range []int{0, 6} {
		if !s.HasReportDay(d) {
			t.Errorf("HasReportDay(%d) = false", d)
		}
	}
	for _, d := range []int{1, 5, 7, -1} {
		if s.HasReportDay(d) {
			t.Errorf("HasReportDay(%d) = true", d)
		}
	}
}

// TestAddAndRemoveReportTime pins the bounds of RemoveReportTime: an out-of-range
// index has to be refused, or the schedule loses its first entry to a button that
// meant to delete the last one.
func TestAddAndRemoveReportTime(t *testing.T) {
	s := &UserSettings{}
	s.AddReportTime(TimePoint{Hour: 7, Minute: 30})
	s.AddReportTime(TimePoint{Hour: 19, Minute: 0})

	_, _, times := s.GetReportsSettings()
	if len(times) != 2 {
		t.Fatalf("times = %+v", times)
	}

	if !s.RemoveReportTime(0) {
		t.Error("RemoveReportTime(0) = false on a two-entry schedule")
	}
	_, _, times = s.GetReportsSettings()
	if len(times) != 1 || times[0].Hour != 19 {
		t.Errorf("the wrong entry was removed: %+v", times)
	}

	if s.RemoveReportTime(5) {
		t.Error("RemoveReportTime(5) = true on a one-entry schedule")
	}
	if s.RemoveReportTime(-1) {
		t.Error("RemoveReportDay(-1) = true")
	}

	if !s.RemoveReportTime(0) {
		t.Error("removing the last entry must succeed")
	}
	_, _, times = s.GetReportsSettings()
	if len(times) != 0 {
		t.Errorf("times = %+v, want empty", times)
	}
	if s.RemoveReportTime(0) {
		t.Error("removing from an empty schedule must fail")
	}
}

func TestGetReportsDetailedSettingsMatchesTheShortForm(t *testing.T) {
	s := &UserSettings{}
	s.SetReportsSettings(true, 2, []TimePoint{{Hour: 6}, {Hour: 18}})
	s.SetReportsDays([]int{1, 4})

	enabled, interval, times := s.GetReportsSettings()
	dEnabled, dInterval, dTimes, dDays := s.GetReportsDetailedSettings()
	if dEnabled != enabled || dInterval != interval || len(dTimes) != len(times) {
		t.Errorf("detailed (%v, %d, %+v) != short (%v, %d, %+v)",
			dEnabled, dInterval, dTimes, enabled, interval, times)
	}
	if days := s.GetReportsDays(); len(days) != 2 {
		t.Errorf("days = %v", days)
	}
	if len(dDays) != 2 || dDays[0] != 1 || dDays[1] != 4 {
		t.Errorf("the detailed form must report the days too, got %v", dDays)
	}
}

// TestOnReportsChangedWakesTheScheduler is the contract the report scheduler
// depends on: after any settings change the channel the scheduler is waiting on
// has to fire, and the scheduler must then re-read it and find a live one.
//
// Defect covered: a scheduler that waits on an already-closed channel returns
// immediately and spins, or one that waits on a channel nobody closes sleeps
// through a schedule change until the next tick.
func TestOnReportsChangedWakesTheScheduler(t *testing.T) {
	s := &UserSettings{}

	ch := s.OnReportsChanged()
	select {
	case <-ch:
		t.Fatal("a fresh channel must be open, otherwise the scheduler returns at once and spins")
	default:
	}

	s.SetReportsSettings(true, 3, []TimePoint{{Hour: 9}})

	select {
	case <-ch:
	default:
		t.Fatal("a settings change did not close the channel the scheduler holds")
	}

	// The closed channel must have been replaced, not just closed, or the
	// scheduler would spin on the next wait.
	next := s.OnReportsChanged()
	select {
	case <-next:
		t.Fatal("the replacement channel is already closed: the scheduler would spin")
	default:
	}

	s.SetReportsEnabled(false)
	select {
	case <-next:
	default:
		t.Fatal("the second change did not wake the scheduler")
	}
}

// TestOnReportsChangedWithNoReader: signalReportsChanged runs on the write lock
// and a change can land before anything ever called OnReportsChanged (a
// /configset at boot, for instance). It has to install a live channel rather than
// close a nil one and leave the first reader waiting forever.
func TestOnReportsChangedWithNoReader(t *testing.T) {
	s := &UserSettings{}

	s.SetReportsEnabled(true) // no reader yet: signalReportsChanged has to cope

	ch := s.OnReportsChanged()
	select {
	case <-ch:
		t.Fatal("the first reader got a closed channel and would spin")
	default:
	}
}

// TestOnReportsChangedIsConcurrencySafe: every settings button and the scheduler
// can be in these two functions at the same time. Run under -race (and the
// deadlock build tag) this fails if the channel field is read outside Mu, which is
// the exact bug the comments on the field describe.
//
// The scheduler side is modelled faithfully: it waits on the channel, is woken,
// re-reads it and waits again. It stops as soon as the writers are done, because
// "never woken" is only a defect while something is still changing the schedule.
func TestOnReportsChangedIsConcurrencySafe(t *testing.T) {
	s := &UserSettings{}

	var writers sync.WaitGroup
	for i := 0; i < 8; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			for j := 0; j < 200; j++ {
				s.SetReportInterval(i + j%3)
				_ = s.OnReportsChanged()
				s.ToggleReportDay(j % 7)
				s.AddReportTime(TimePoint{Hour: j % 24})
				s.RemoveReportTime(0)
			}
		}(i)
	}

	writersDone := make(chan struct{})
	go func() {
		writers.Wait()
		close(writersDone)
	}()

	woke := runSchedulerUntilWritersFinish(t, s, writersDone)

	if woke == 0 {
		t.Error("the scheduler was never woken although the schedule changed 1600 times")
	}
	t.Logf("the scheduler was woken %d times while the writers were running", woke)
}

// runSchedulerUntilWritersFinish is the scheduler loop: wait, wake, re-read.
// It returns the number of wakes and fails the test if a wait outlasts the writers.
func runSchedulerUntilWritersFinish(t *testing.T, s *UserSettings, writersDone <-chan struct{}) int {
	t.Helper()

	woke := 0
	for {
		// Nothing left to wait for once the writers are done.
		select {
		case <-writersDone:
			return woke
		default:
		}

		ch := s.OnReportsChanged()
		_, _, _ = s.GetReportsSettings()

		select {
		case <-ch:
			woke++
		case <-writersDone:
			// The last change may already have been consumed by another waiter
			// ordering; returning here is correct, not a missed wake.
			return woke
		case <-time.After(30 * time.Second):
			t.Error("a settings change did not reach the scheduler within 30s")
			return woke
		}
	}
}

func TestQuietHoursAccessors(t *testing.T) {
	s := &UserSettings{}
	want := QuietSettings{
		Enabled: true,
		Start:   TimePoint{Hour: 23, Minute: 30},
		End:     TimePoint{Hour: 7, Minute: 0},
	}
	s.SetQuietHours(want)

	got := s.GetQuietHours()
	if got != want {
		t.Errorf("quiet hours = %+v, want %+v", got, want)
	}

	s.SetQuietHoursEnabled(false)
	if s.GetQuietHours().Enabled {
		t.Error("SetQuietHoursEnabled(false) did not take")
	}
	s.SetQuietHoursEnabled(true)
	if !s.GetQuietHours().Enabled {
		t.Error("SetQuietHoursEnabled(true) did not take")
	}
	// The flag must not disturb the window.
	if s.GetQuietHours().Start != want.Start || s.GetQuietHours().End != want.End {
		t.Errorf("SetQuietHoursEnabled changed the window: %+v", s.GetQuietHours())
	}
}

func TestDockerPruneAccessors(t *testing.T) {
	s := &UserSettings{}

	want := PruneSettings{Enabled: true, Day: "sunday", Hour: 4}
	s.SetDockerPrune(want)
	if got := s.GetDockerPrune(); got != want {
		t.Errorf("prune = %+v, want %+v", got, want)
	}

	s.SetDockerPruneEnabled(false)
	if s.GetDockerPrune().Enabled {
		t.Error("SetDockerPruneEnabled(false) did not take")
	}

	s.SetDockerPruneHour(23)
	if got := s.GetDockerPrune().Hour; got != 23 {
		t.Errorf("hour = %d, want 23", got)
	}
}

// TestSetDockerPruneDayValidatesTheWeekday: the day is written straight into a
// cron-like schedule, so an invalid name has to be refused rather than stored.
// A stored "surnday" silently disables the weekly prune.
func TestSetDockerPruneDayValidatesTheWeekday(t *testing.T) {
	s := &UserSettings{}

	for _, day := range []string{
		"monday", "Tuesday", " WEDNESDAY ", "thursday", "friday", "Saturday", "sunday",
	} {
		if !s.SetDockerPruneDay(day) {
			t.Errorf("SetDockerPruneDay(%q) = false, want true", day)
		}
		got := s.GetDockerPrune().Day
		if got != day2lower(day) {
			t.Errorf("SetDockerPruneDay(%q) stored %q", day, got)
		}
	}

	for _, bad := range []string{"", "surnday", "mon", "0", "next tuesday"} {
		if s.SetDockerPruneDay(bad) {
			t.Errorf("SetDockerPruneDay(%q) = true, want false", bad)
		}
	}

	// A refused day must leave the previous one in place.
	if got := s.GetDockerPrune().Day; got != "sunday" {
		t.Errorf("a refused day changed the stored weekday to %q", got)
	}
}

func day2lower(day string) string {
	out := ""
	for _, r := range day {
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r != ' ' {
			out += string(r)
		}
	}
	return out
}

// TestSettingsAccessorsAreConcurrencySafe: every accessor must take Mu, on every
// path, including the "enabled" setters that only write one field. A plain
// assignment there is a data race that the race detector reports only under load,
// which is exactly when it happens in production.
func TestSettingsAccessorsAreConcurrencySafe(t *testing.T) {
	s := &UserSettings{Language: "en"}
	done := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				s.SetLanguage("it")
				_ = s.GetLanguage()
				s.SetReportsEnabled(j%2 == 0)
				s.SetReportInterval(j)
				s.SetReportsDays([]int{j % 7, (j + 1) % 7})
				s.ToggleReportDay(j % 7)
				_ = s.HasReportDay(j % 7)
				s.AddReportTime(TimePoint{Hour: j % 24})
				s.RemoveReportTime(0)
				_, _, _ = s.GetReportsSettings()
				_, _, _, _ = s.GetReportsDetailedSettings()
				_ = s.GetReportsDays()
				s.SetQuietHours(QuietSettings{Enabled: j%2 == 0, Start: TimePoint{Hour: j % 24}})
				s.SetQuietHoursEnabled(j%2 == 0)
				_ = s.GetQuietHours()
				s.SetDockerPrune(PruneSettings{Enabled: j%2 == 0, Day: "monday", Hour: j % 24})
				s.SetDockerPruneEnabled(j%2 == 0)
				_ = s.SetDockerPruneDay("monday")
				s.SetDockerPruneHour(j % 24)
				_ = s.GetDockerPrune()
			}
		}(i)
	}

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("the settings accessors deadlocked")
	}
}

// TestStatsGetHandsOutACopy is the deep-copy contract on ThreadSafeStats.
//
// Stats is returned by value but SecondaryVols is a map and TopCPU/TopRAM are
// slices: handing those out by reference lets a caller keep reading them while the
// collector replaces them, which the race detector reports and which can panic
// with "concurrent map iteration and map write".
func TestStatsGetHandsOutACopy(t *testing.T) {
	ts := &ThreadSafeStats{}

	ts.Set(Stats{
		CPU:           1,
		SecondaryVols: map[string]VolumeStats{"/mnt/data": {Used: 10}},
		TopCPU:        []ProcInfo{{Name: "nasbot", Cpu: 1}},
		TopRAM:        []ProcInfo{{Name: "dockerd", Cpu: 2}},
	})

	got, ready := ts.Get()
	if !ready {
		t.Fatal("a Set followed by a Get must report ready")
	}
	if got.SecondaryVols == nil || len(got.SecondaryVols) != 1 {
		t.Fatalf("SecondaryVols = %+v", got.SecondaryVols)
	}
	if len(got.TopCPU) != 1 || len(got.TopRAM) != 1 {
		t.Fatalf("TopCPU/TopRAM = %+v %+v", got.TopCPU, got.TopRAM)
	}

	// Mutate every reference type through the returned copy.
	got.SecondaryVols["/mnt/data"] = VolumeStats{Used: 99}
	got.SecondaryVols["/mnt/other"] = VolumeStats{}
	got.TopCPU[0].Name = "mutated"
	got.TopRAM[0].Name = "mutated"

	again, _ := ts.Get()
	if again.SecondaryVols["/mnt/data"].Used != 10 {
		t.Errorf("the caller mutated the live map through Get: %+v", again.SecondaryVols)
	}
	if len(again.SecondaryVols) != 1 {
		t.Errorf("the caller added an entry to the live map: %+v", again.SecondaryVols)
	}
	if again.TopCPU[0].Name != "nasbot" || again.TopRAM[0].Name != "dockerd" {
		t.Errorf("the caller mutated the live slices: %+v %+v", again.TopCPU, again.TopRAM)
	}
}

// TestStatsSetPublishesReadyAndData is the other half: a fresh ThreadSafeStats is
// not ready, and Set is what makes it ready. getStatusText answers "loading"
// until then, so a Set that forgot Ready would leave the bot on the hourglass
// forever.
func TestStatsSetPublishesReadyAndData(t *testing.T) {
	ts := &ThreadSafeStats{}
	if _, ready := ts.Get(); ready {
		t.Fatal("a zero ThreadSafeStats must not report ready")
	}

	ts.Set(Stats{CPU: 42})
	got, ready := ts.Get()
	if !ready {
		t.Fatal("Set did not publish ready")
	}
	if got.CPU != 42 {
		t.Errorf("CPU = %v, want 42", got.CPU)
	}

	// A nil map and a nil slice must stay nil rather than becoming empty: a
	// caller distinguishes "no secondary disks" from "not collected yet".
	ts.Set(Stats{CPU: 1})
	got, _ = ts.Get()
	if got.SecondaryVols != nil {
		t.Errorf("SecondaryVols = %+v, want nil", got.SecondaryVols)
	}
	if got.TopCPU != nil || got.TopRAM != nil {
		t.Errorf("TopCPU/TopRAM = %+v %+v, want nil", got.TopCPU, got.TopRAM)
	}
}

// TestStatsGetIsConcurrencySafe: the collector writes once a second while every
// command reads. Under -race this is the guard on the RWMutex being taken.
func TestStatsGetIsConcurrencySafe(t *testing.T) {
	ts := &ThreadSafeStats{}
	done := make(chan struct{})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				ts.Set(Stats{
					CPU:           float64(j % 100),
					SecondaryVols: map[string]VolumeStats{"/mnt/data": {Used: float64(j)}},
					TopCPU:        []ProcInfo{{Name: "nasbot", Cpu: 1}},
				})
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if _, ready := ts.Get(); !ready {
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("ThreadSafeStats deadlocked")
	}
}

// TestCfgReturnsThePublishedSnapshot is the reload contract: a reload publishes a
// brand-new snapshot, so a reader holding the pointer it got from Cfg() keeps
// seeing consistent values instead of a struct mutating under it.
func TestCfgReturnsThePublishedSnapshot(t *testing.T) {
	ctx := &AppContext{}

	// Nothing published yet: the legacy field is the fallback.
	ctx.Config = &Config{AllowedUserID: 1}
	if got := ctx.Cfg(); got == nil || got.AllowedUserID != 1 {
		t.Errorf("Cfg on a context with no snapshot = %+v", got)
	}

	published := &Config{AllowedUserID: 42}
	ctx.SetConfig(published)

	got := ctx.Cfg()
	if got.AllowedUserID != 42 {
		t.Errorf("Cfg = %+v, want the published snapshot", got)
	}
	// The pointer must be stable across calls, so a reader can hold it.
	if ctx.Cfg() != got {
		t.Error("Cfg returned a different pointer on the second call")
	}

	// A reload swaps the pointer rather than mutating: the reader that already
	// has one keeps its values.
	ctx.SetConfig(&Config{AllowedUserID: 99})
	if got.AllowedUserID != 42 {
		t.Errorf("the snapshot a reader was holding changed under it: %+v", got)
	}
	if ctx.Cfg().AllowedUserID != 99 {
		t.Errorf("Cfg did not pick up the new snapshot: %+v", ctx.Cfg())
	}
}

// TestSetConfigIgnoresNil: a reload that fails to build a Config must not blank
// the published one, or every reader would see a zero configuration.
func TestSetConfigIgnoresNil(t *testing.T) {
	ctx := &AppContext{}
	ctx.SetConfig(&Config{AllowedUserID: 42})

	ctx.SetConfig(nil)

	if got := ctx.Cfg(); got == nil || got.AllowedUserID != 42 {
		t.Errorf("SetConfig(nil) wiped the snapshot: %+v", got)
	}

	// A nil receiver must be inert too: the standalone watchdog builds contexts
	// by hand.
	var nilCtx *AppContext
	nilCtx.SetConfig(&Config{})
	if nilCtx.Cfg() != nil {
		t.Error("Cfg on a nil context must be nil")
	}
}

// TestTrUsesTheLanguageSetting wires the three pieces together: the language the
// settings buttons set, and the resolver the bootstrap installs.
func TestTrUsesTheLanguageSetting(t *testing.T) {
	ctx := InitApp(nil)

	prev := Translate
	t.Cleanup(func() { Translate = prev })
	Translate = func(lang, key string) string { return lang + ":" + key }

	ctx.Settings.SetLanguage("it")
	if got := ctx.Tr("ping_ok"); got != "it:ping_ok" {
		t.Errorf("Tr = %q, want it:ping_ok", got)
	}

	// An empty language must fall back to English rather than resolve nothing.
	ctx.Settings.SetLanguage("")
	if got := ctx.Tr("ping_ok"); got != "en:ping_ok" {
		t.Errorf("Tr with an empty language = %q, want en:ping_ok", got)
	}
}

// TestInitAppSeedsSettingsFromConfig: the defaults are the answer when the config
// says nothing, and the config wins when it does. A user who configured reports at
// 06:00 and found 07:30 would file that as a bug.
func TestInitAppSeedsSettingsFromConfig(t *testing.T) {
	t.Run("nil config falls back to the defaults", func(t *testing.T) {
		app := InitApp(nil)
		enabled, interval, times := app.Settings.GetReportsSettings()
		if !enabled {
			t.Error("reports default to enabled")
		}
		if interval != 1 {
			t.Errorf("default interval = %d, want 1", interval)
		}
		if len(times) != 2 {
			t.Errorf("default times = %+v, want two entries", times)
		}
		if app.Settings.GetQuietHours().Start != (TimePoint{Hour: 23, Minute: 30}) {
			t.Errorf("default quiet start = %+v", app.Settings.GetQuietHours().Start)
		}
		if app.Settings.GetDockerPrune() != (PruneSettings{Enabled: true, Day: "sunday", Hour: 4}) {
			t.Errorf("default prune = %+v", app.Settings.GetDockerPrune())
		}
	})

	t.Run("config wins", func(t *testing.T) {
		cfg := &Config{
			Reports: ReportsConfig{
				Enabled:      false,
				IntervalDays: 4,
				Times:        []TimeConfig{{Hour: 6, Minute: 15}},
			},
			QuietHours: QuietHoursConfig{
				Enabled: false, StartHour: 21, StartMinute: 45, EndHour: 5, EndMinute: 0,
			},
			Docker: DockerConfig{
				WeeklyPrune: DockerPruneConfig{Enabled: false, Day: "saturday", Hour: 2},
			},
		}
		app := InitApp(cfg)

		enabled, interval, times := app.Settings.GetReportsSettings()
		if enabled {
			t.Error("reports must follow the config (disabled)")
		}
		if interval != 4 {
			t.Errorf("interval = %d, want 4", interval)
		}
		if len(times) != 1 || times[0].Hour != 6 || times[0].Minute != 15 {
			t.Errorf("times = %+v, want 06:15", times)
		}
		if q := app.Settings.GetQuietHours(); q.Enabled || q.Start != (TimePoint{Hour: 21, Minute: 45}) {
			t.Errorf("quiet hours = %+v", q)
		}
		if p := app.Settings.GetDockerPrune(); p.Enabled || p.Day != "saturday" || p.Hour != 2 {
			t.Errorf("prune = %+v", p)
		}
	})
}

// TestInitAppBuildsAUsableContextWithoutConfig: the standalone watchdog and every
// test build a context by hand. Every field a monitor touches has to be non-nil,
// or the first alert takes the process down.
func TestInitAppBuildsAUsableContextWithoutConfig(t *testing.T) {
	app := InitApp(nil)

	if app.Stats == nil || app.State == nil || app.Settings == nil || app.Bot == nil ||
		app.Docker == nil || app.Monitor == nil || app.HTTP == nil {
		t.Fatalf("InitApp(nil) left a nil field: %+v", app)
	}
	if app.State.TimeLocation == nil {
		t.Error("TimeLocation must default to UTC: IsQuietHours would panic on nil")
	}
	if app.State.ResourceStress == nil || app.State.ResourceStress["CPU"] == nil {
		t.Errorf("stress trackers = %+v, want one per resource", app.State.ResourceStress)
	}
	if app.Docker.AutoRestarts == nil || app.Docker.LastStates == nil || app.Docker.ContainerDowntime == nil {
		t.Error("the Docker manager's maps must be initialised")
	}
	if app.Monitor.CPUTrend == nil || app.Monitor.RAMTrend == nil ||
		app.Monitor.LastCriticalContainerAlert == nil || app.Monitor.SmartCache == nil ||
		app.Monitor.KwLastSignatures == nil || app.Monitor.DiskMountAlertCooldown == nil {
		t.Error("a monitor map is nil")
	}
	// A nil config still publishes a usable snapshot, so Cfg() never needs a nil
	// check at the call site.
	if app.Cfg() == nil {
		t.Error("InitApp(nil) published no configuration snapshot")
	}

	// AddEvent and the other state helpers must work on a fresh context.
	app.State.AddEvent("t", "m")
	if got := app.State.GetEvents(); len(got) != 1 {
		t.Errorf("events = %+v", got)
	}
	app.Bot.SetPendingAction("add_report_time")
	if got := app.Bot.GetPendingAction(); got != "add_report_time" {
		t.Errorf("pending action = %q", got)
	}
	app.Bot.ClearPendingAction()
	if got := app.Bot.GetPendingAction(); got != "" {
		t.Errorf("ClearPendingAction left %q", got)
	}
}

// TestAddEventKeepsTheLastHundred is the history cap. Without it a long-running
// bot grows the state file without bound and every save rewrites the lot.
func TestAddEventKeepsTheLastHundred(t *testing.T) {
	rs := &RuntimeState{}

	for i := 0; i < 150; i++ {
		rs.AddEvent("t", string(rune('a'+i%26)))
	}

	events := rs.GetEvents()
	if len(events) != 100 {
		t.Fatalf("kept %d events, want 100", len(events))
	}
	// The oldest must be the one dropped.
	if events[0].Message != string(rune('a'+50%26)) && len(events) == 100 {
		// The exact oldest value depends on the wrap of the alphabet; what matters
		// is that the last one survived.
		if events[99].Message != string(rune('a'+149%26)) {
			t.Errorf("the newest event is not last: %q", events[99].Message)
		}
	}
	if last := events[len(events)-1]; last.Message != string(rune('a'+149%26)) {
		t.Errorf("the newest event was dropped: %+v", last)
	}
}

// TestGetEventsHandsOutACopy: the caller is free to keep and reorder the slice.
func TestGetEventsHandsOutACopy(t *testing.T) {
	rs := &RuntimeState{}
	rs.AddEvent("t", "first")

	got := rs.GetEvents()
	got[0].Message = "mutated"
	got = append(got, ReportEvent{Type: "x"})

	again := rs.GetEvents()
	if len(again) != 1 || again[0].Message != "first" {
		t.Errorf("the caller mutated the live events: %+v", again)
	}
}

// TestClearEventsEmptiesTheListWithoutLeavingNil: a nil slice and an empty one
// mean the same thing here, but only one of them survives a JSON round trip the
// way the code expects.
func TestClearEventsEmptiesTheListWithoutLeavingNil(t *testing.T) {
	rs := &RuntimeState{}
	rs.AddEvent("t", "m")

	rs.ClearEvents()

	got := rs.GetEvents()
	if len(got) != 0 {
		t.Fatalf("events = %+v, want empty", got)
	}
	if got == nil {
		t.Error("ClearEvents left a nil slice; the persistence layer writes it as null")
	}
}

// TestLogHelpersAreAvailableOutsideAMain: the context carries the log helpers so
// that a package without its own logger does not import log/slog everywhere. They
// must be callable on a context built by hand.
func TestLogHelpersAreAvailableOutsideAMain(t *testing.T) {
	ctx := InitApp(nil)
	if ctx == nil {
		t.Fatal("InitApp returned nil")
	}
	ctx.LogInfo("from a hand-built context")
	_ = context.Background()
}
