package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"nasbot/pkg/model"
)

// TestSaveStateNeverProducesATornFileUnderConcurrency pins the outcome the
// saveState comment is about: after many overlapping saves of the *shared* app
// context, the state file on disk is complete, parseable and internally
// consistent, never a mixture of two writes.
//
// What this test does and does not prove, because it matters when the test is
// trusted:
//
//   - It DOES pin the outcome, and it is a real regression guard for the write
//     path. Removing stateSaveMu does NOT make it fail, because saveState writes
//     through os.CreateTemp + os.Rename, and a unique temporary name plus an
//     atomic rename already make a torn file impossible. Verified by mutation on
//     2026-10-02; see the report.
//   - It does NOT prove the mutex does anything. With the current
//     unique-temporary write, stateSaveMu is not observable from outside the
//     package and looks redundant. What it is still worth is the lock ordering,
//     which the deadlock build tag exercises below.
func TestSaveStateNeverProducesATornFileUnderConcurrency(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	ctx := newTestAppContext()
	ctx.Docker.Mu.Lock()
	if ctx.Docker.AutoRestarts == nil {
		ctx.Docker.AutoRestarts = make(map[string][]time.Time)
	}
	ctx.Docker.Mu.Unlock()

	const (
		savers  = 6
		mutator = 6
		rounds  = 40
	)

	var wg sync.WaitGroup

	// Writers: one shared context, exactly as the running bot has a single `app`.
	for i := 0; i < savers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				saveState(ctx)
			}
		}()
	}

	// Mutators: the other call sites of saveState do not only save, they change
	// the settings first. Running them together is the real shape.
	for i := 0; i < mutator; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				ctx.State.AddEvent("probe", strconv.Itoa(r))
				ctx.Settings.SetReportsSettings(true, i+1,
					[]model.TimePoint{{Hour: i, Minute: r % 60}})
				ctx.Settings.SetQuietHours(model.QuietSettings{
					Enabled: true,
					Start:   model.TimePoint{Hour: i, Minute: r % 60},
					End:     model.TimePoint{Hour: 23, Minute: 59},
				})
				ctx.Settings.SetDockerPruneEnabled(r%2 == 0)
				ctx.Docker.Mu.Lock()
				ctx.Docker.AutoRestarts["plex"] = []time.Time{time.Now()}
				ctx.Docker.Mu.Unlock()
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("concurrent saveState did not finish: a lock is held across another")
	}

	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("the state file must exist after a save: %v", err)
	}

	var state BotState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("the state file is not valid JSON after %d concurrent saves: %v\n%s",
			savers*rounds, err, raw)
	}

	// Consistency, precisely stated. saveState reads the report interval and the
	// report times under one single RLock, and both were written by one single
	// SetReportsSettings call, so those two MUST agree. The quiet hours are a
	// different setter with its own lock, and the mutators run concurrently, so
	// the quiet hour may legitimately come from a different writer: tying the two
	// together asserted an atomicity across three independent setters that the
	// API never provided, and failed intermittently on correct code.
	if state.ReportInterval < 1 || state.ReportInterval > mutator {
		t.Fatalf("report interval %d is outside the range any writer used", state.ReportInterval)
	}
	idx := state.ReportInterval - 1
	if len(state.ReportTimes) != 1 || state.ReportTimes[0].Hour != idx {
		t.Errorf("report times %+v do not belong to writer %d (interval %d)",
			state.ReportTimes, idx, state.ReportInterval)
	}
	// Any writer's value, not necessarily the reports writer's: see above. What
	// must hold is that the persisted quiet hour is one the run actually used,
	// i.e. it was not corrupted or left at a default by the save.
	if state.QuietStartHour < 0 || state.QuietStartHour >= mutator {
		t.Errorf("quiet start hour %02d is outside the range any writer used (0..%d)",
			state.QuietStartHour, mutator-1)
	}
	if len(state.ReportEvents) > 100 {
		t.Errorf("state file holds %d events, the cap is 100", len(state.ReportEvents))
	}
	if !state.QuietHoursEnabled {
		t.Error("quiet hours were disabled, but no writer disables them")
	}
}

// TestSaveStateLockOrderIsDeadlockFree is what stateSaveMu is still worth.
//
// saveState takes stateSaveMu and then, one after the other, ctx.State.Mu,
// ctx.Docker.Mu, ctx.Monitor.Mu and ctx.Settings.Mu. Any other code path that
// takes those field locks in a different order and holds one across a saveState
// would wedge every monitor: a deadlock is not a panic, so goSafeResilient cannot
// restart the goroutine and the bot goes quiet with no error. Under the deadlock
// build tag (the CI gate runs it) this test fails instead of hanging the suite.
func TestSaveStateLockOrderIsDeadlockFree(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	ctx := newTestAppContext()

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for i := 0; i < 200; i++ {
			saveState(ctx)
			ctx.Settings.SetLanguage("en")
			ctx.State.AddEvent("tick", "t")
			ctx.Monitor.Mu.Lock()
			ctx.Monitor.NetConsecutiveDegraded = i % 2
			ctx.Monitor.Mu.Unlock()
		}
	}()

	select {
	case <-finished:
	case <-time.After(30 * time.Second):
		t.Fatal("saveState deadlocked against the field locks it also takes")
	}

	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("the state file must exist: %v", err)
	}
}

// TestSaveStateLeavesNoTemporaryFiles is the companion: the atomic write creates
// a unique temporary next to the target and renames it. A failed or serialised
// save must not accumulate "*.tmp-*" files in the state directory, which is the
// same directory the log rotates in.
func TestSaveStateLeavesNoTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	for i := 0; i < 5; i++ {
		saveState(newTestAppContext())
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "nasbot_state.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("state directory holds %v, want just nasbot_state.json", names)
	}
}

// TestSaveStateKeepsTheExistingFileMode: the state file holds the notification
// schedule and the language, and saveState inherits the mode of the file already
// there. On a fresh install the default must still be 0600, because the directory
// is inside the deployment tree.
func TestSaveStateKeepsTheExistingFileMode(t *testing.T) {
	t.Run("new file is private", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
		t.Setenv("NASBOT_STATE_FILE", statePath)

		saveState(newTestAppContext())

		info, err := os.Stat(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("mode = %04o, want 0600", got)
		}
	})

	t.Run("existing mode is preserved", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
		t.Setenv("NASBOT_STATE_FILE", statePath)
		if err := os.WriteFile(statePath, []byte("{}"), 0o640); err != nil {
			t.Fatal(err)
		}

		saveState(newTestAppContext())

		info, err := os.Stat(statePath)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("mode = %04o, want the 0640 already on disk", got)
		}
	})
}

// TestSaveStateRoundTripsEveryField proves the serialisation actually carries the
// schedule: a save that drops ReportDays or the prune window comes back with
// defaults, and the user finds their report times reset after a reboot.
func TestSaveStateRoundTripsEveryField(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	lastReport := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	saved := newTestAppContext()
	saved.State.Mu.Lock()
	saved.State.LastReport = lastReport
	saved.State.LastReleaseNotified = "v9.9.9"
	saved.State.Mu.Unlock()
	saved.Settings.SetLanguage("it")
	saved.Settings.SetReportsDays([]int{1, 3, 5})
	saved.Settings.SetReportsSettings(true, 2, []model.TimePoint{{Hour: 6, Minute: 45}})
	saved.Settings.SetQuietHours(model.QuietSettings{
		Enabled: true,
		Start:   model.TimePoint{Hour: 22, Minute: 15},
		End:     model.TimePoint{Hour: 6, Minute: 30},
	})
	saved.Settings.SetDockerPrune(model.PruneSettings{
		Enabled: true, Day: "saturday", Hour: 3,
	})
	saved.Docker.Mu.Lock()
	if saved.Docker.AutoRestarts == nil {
		saved.Docker.AutoRestarts = make(map[string][]time.Time)
	}
	saved.Docker.AutoRestarts["plex"] = []time.Time{lastReport}
	saved.Docker.Mu.Unlock()
	saveState(saved)

	loaded := newTestAppContext()
	loadState(loaded)

	if got := loaded.Settings.GetLanguage(); got != "it" {
		t.Errorf("language = %q, want it", got)
	}
	loaded.State.Mu.Lock()
	gotLastReport := loaded.State.LastReport
	gotNotified := loaded.State.LastReleaseNotified
	loaded.State.Mu.Unlock()
	if !gotLastReport.Equal(lastReport) {
		t.Errorf("last report = %v, want %v", gotLastReport, lastReport)
	}
	if gotNotified != "v9.9.9" {
		t.Errorf("last release notified = %q, want v9.9.9", gotNotified)
	}
	if days := loaded.Settings.GetReportsDays(); len(days) != 3 || days[0] != 1 || days[2] != 5 {
		t.Errorf("report days = %v, want [1 3 5]", days)
	}
	enabled, interval, times := loaded.Settings.GetReportsSettings()
	if !enabled || interval != 2 || len(times) != 1 || times[0].Hour != 6 || times[0].Minute != 45 {
		t.Errorf("reports = %v, %d, %+v", enabled, interval, times)
	}
	quiet := loaded.Settings.GetQuietHours()
	if !quiet.Enabled || quiet.Start.Hour != 22 || quiet.Start.Minute != 15 ||
		quiet.End.Hour != 6 || quiet.End.Minute != 30 {
		t.Errorf("quiet hours = %+v", quiet)
	}
	prune := loaded.Settings.GetDockerPrune()
	if !prune.Enabled || prune.Day != "saturday" || prune.Hour != 3 {
		t.Errorf("prune = %+v", prune)
	}
	loaded.Docker.Mu.RLock()
	restarts := len(loaded.Docker.AutoRestarts["plex"])
	loaded.Docker.Mu.RUnlock()
	if restarts != 1 {
		t.Errorf("auto restarts for plex = %d, want 1", restarts)
	}
}

// TestLoadStateOnMissingFileIsANoOp: a first run has no state. loadState must
// return without inventing values, or the defaults it writes would overwrite the
// configuration the user just set up.
func TestLoadStateOnMissingFileIsANoOp(t *testing.T) {
	t.Setenv("NASBOT_STATE_FILE", filepath.Join(t.TempDir(), "never-written.json"))

	ctx := newTestAppContext()
	before := ctx.Settings.GetLanguage()
	beforeQuiet := ctx.Settings.GetQuietHours()

	loadState(ctx)

	if got := ctx.Settings.GetLanguage(); got != before {
		t.Errorf("language changed from %q to %q on a first run", before, got)
	}
	if got := ctx.Settings.GetQuietHours(); got != beforeQuiet {
		t.Errorf("quiet hours changed on a first run: %+v -> %+v", beforeQuiet, got)
	}
	if events := ctx.State.GetEvents(); len(events) != 0 {
		t.Errorf("events appeared out of nowhere: %+v", events)
	}
}

// TestLoadStateOnCorruptFileKeepsCurrentSettings: a truncated or hand-edited state
// file must not wipe the running configuration. Refusing to load is the only safe
// answer.
func TestLoadStateOnCorruptFileKeepsCurrentSettings(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	if err := os.WriteFile(statePath, []byte(`{"language": "it", "report_times`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := newTestAppContext()
	ctx.Settings.SetLanguage("de")
	loadState(ctx)

	if got := ctx.Settings.GetLanguage(); got != "de" {
		t.Errorf("a corrupt state file overwrote the running language: %q", got)
	}
}

// TestLoadStateTruncatesAnOverlongEventHistory: saveState keeps at most 100
// events, but a state file written by an older version (or by hand) can hold
// more. loadState must not carry all of them into memory and then save them back.
func TestLoadStateTruncatesAnOverlongEventHistory(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "nasbot_state.json")
	t.Setenv("NASBOT_STATE_FILE", statePath)

	events := make([]ReportEvent, 0, 150)
	for i := 0; i < 150; i++ {
		events = append(events, ReportEvent{Time: time.Now(), Type: "probe", Message: "x"})
	}
	state := BotState{Language: "en", ReportEvents: events}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	ctx := newTestAppContext()
	loadState(ctx)

	if got := len(ctx.State.GetEvents()); got != 100 {
		t.Errorf("loaded %d events, want the last 100", got)
	}
}
