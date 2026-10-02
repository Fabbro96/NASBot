package model

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// capturingHandler records the slog records it is given, so a test can assert on
// the level, the message and the attributes instead of on "it did not panic".
type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
	attrs   []slog.Attr
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attrs = append(h.attrs, attrs...)
	return h
}

func (h *capturingHandler) WithGroup(string) slog.Handler { return h }

func (h *capturingHandler) snapshot() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

// captureLogs redirects the default logger for the duration of the test.
func captureLogs(t *testing.T) *capturingHandler {
	t.Helper()

	h := &capturingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

func TestRuntimeStateEvents(t *testing.T) {
	ctx := InitApp(nil)

	ctx.State.AddEvent("test_type", "test_msg")
	events := ctx.State.GetEvents()

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Type != "test_type" || events[0].Message != "test_msg" {
		t.Errorf("Event data mismatch: %v", events[0])
	}

	ctx.State.ClearEvents()
	if len(ctx.State.GetEvents()) != 0 {
		t.Errorf("Expected 0 events after clear")
	}
}

func TestThreadSafeStats(t *testing.T) {
	ctx := InitApp(nil)

	_, ready := ctx.GetStats()
	if ready {
		t.Fatal("Expected stats to be unready initially")
	}

	ctx.Stats.Set(Stats{CPU: 42.0})

	stats, ready := ctx.GetStats()
	if !ready || stats.CPU != 42.0 {
		t.Errorf("Stats not updated correctly: %v, ready=%v", stats, ready)
	}
}

// recordAttrs materialises the attributes of a captured record.
func recordAttrs(r slog.Record) []slog.Attr {
	var out []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		out = append(out, a)
		return true
	})
	return out
}

// minutesOfDay wraps a minute offset into [0, 1440).
func minutesOfDay(m int) int { return ((m % 1440) + 1440) % 1440 }

// TestIsQuietHours is the replacement for a test whose middle assertion was
// `_ = ctx.IsQuietHours()` with the comment "We'll trust the function logic".
// Discarding the result cannot fail, and it left the whole window comparison
// untested: a mutant that always answered false, or one that treated an
// overnight window as a same-day one, passed.
//
// IsQuietHours reads the wall clock and takes no clock injection, so the cases
// below are built from time.Now() rather than from fixed hours. Each window is
// placed so the expected answer holds for at least 59 minutes around "now": the
// minute cannot roll over between reading it here and reading it inside the
// function, which is what keeps the test deterministic.
func TestIsQuietHours(t *testing.T) {
	ctx := InitApp(nil)
	ctx.State.TimeLocation = time.UTC
	nowMin := time.Now().In(time.UTC).Hour()*60 + time.Now().In(time.UTC).Minute()

	cases := []struct {
		name    string
		enabled bool
		start   int
		end     int
		want    bool
	}{
		{
			// The switch every notification path consults.
			name: "disabled is never quiet", enabled: false,
			start: minutesOfDay(nowMin - 60), end: minutesOfDay(nowMin + 60), want: false,
		},
		{
			// A zero-length window would otherwise mute everything forever, or
			// nothing at all, depending on how the comparison falls through.
			name: "zero length window is not quiet", enabled: true,
			start: minutesOfDay(nowMin + 30), end: minutesOfDay(nowMin + 30), want: false,
		},
		{
			// start < end branch, inside the window.
			name: "same day window containing now", enabled: true,
			start: minutesOfDay(nowMin - 60), end: minutesOfDay(nowMin + 60), want: true,
		},
		{
			// start < end branch, outside the window. startMin and endMin are both
			// in the future, so the conjunction fails on its first term.
			name: "same day window ahead of now", enabled: true,
			start: minutesOfDay(nowMin + 60), end: minutesOfDay(nowMin + 120), want: false,
		},
		{
			// start > end branch (the window crosses midnight), now inside it.
			// Always lands on the overnight branch: start is 60 minutes after end
			// in absolute terms whichever side of midnight they fall.
			name: "overnight window containing now", enabled: true,
			start: minutesOfDay(nowMin - 60), end: minutesOfDay(nowMin - 120), want: true,
		},
		{
			// start > end or start < end depending on the hour, but now is always
			// in the excluded band: the two bounds are 12 hours away from now, so
			// the expected answer has at least 600 minutes of margin.
			name: "window twelve hours away from now", enabled: true,
			start: minutesOfDay(nowMin + 600), end: minutesOfDay(nowMin - 600), want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx.Settings.QuietHours = QuietSettings{
				Enabled: tc.enabled,
				Start:   TimePoint{Hour: tc.start / 60, Minute: tc.start % 60},
				End:     TimePoint{Hour: tc.end / 60, Minute: tc.end % 60},
			}

			if got := ctx.IsQuietHours(); got != tc.want {
				t.Errorf("IsQuietHours() = %v, want %v (now %02d:%02d UTC, window %02d:%02d-%02d:%02d)",
					got, tc.want, nowMin/60, nowMin%60, tc.start/60, tc.start%60, tc.end/60, tc.end%60)
			}
		})
	}
}

// TestIsQuietHoursWithNilTimeLocation: a context built by hand (a test, the
// standalone watchdog) leaves State.TimeLocation nil, and time.Now().In(nil)
// panics. The fallback to time.Local is the only thing between that and a crash,
// so the case is exercised with a window that is quiet in local time right now.
//
// The defect it covers: without the nil guard, every monitor that builds its own
// context dies on the first alert it tries to suppress.
func TestIsQuietHoursWithNilTimeLocation(t *testing.T) {
	ctx := InitApp(nil)
	ctx.State.TimeLocation = nil

	local := time.Now()
	nowMin := local.Hour()*60 + local.Minute()
	ctx.Settings.QuietHours = QuietSettings{
		Enabled: true,
		Start:   TimePoint{Hour: minutesOfDay(nowMin-60) / 60, Minute: minutesOfDay(nowMin-60) % 60},
		End:     TimePoint{Hour: minutesOfDay(nowMin+60) / 60, Minute: minutesOfDay(nowMin+60) % 60},
	}

	if !ctx.IsQuietHours() {
		t.Fatalf("IsQuietHours with a nil TimeLocation = false, want true "+
			"(now %02d:%02d local, window %02d:%02d-%02d:%02d)",
			nowMin/60, nowMin%60,
			ctx.Settings.QuietHours.Start.Hour, ctx.Settings.QuietHours.Start.Minute,
			ctx.Settings.QuietHours.End.Hour, ctx.Settings.QuietHours.End.Minute)
	}
}

// TestContextLogger replaces two calls guarded by "Should not panic".
//
// The defect it covers: LogError and LogInfo are the only way the runtime reports
// to the user. A version that dropped the arguments, or that logged the formatted
// concatenation as the message, would leave every alert with its context
// stripped and no test would notice, because both variants run happily.
func TestContextLogger(t *testing.T) {
	h := captureLogs(t)
	ctx := InitApp(nil)

	ctx.LogError("test error", slog.String("key", "val"))
	ctx.LogInfo("test info", slog.Int("count", 3))

	records := h.snapshot()
	if len(records) != 2 {
		t.Fatalf("expected two log records, got %d", len(records))
	}

	first := records[0]
	if first.Level != slog.LevelError {
		t.Errorf("LogError logged at %v, want %v", first.Level, slog.LevelError)
	}
	if first.Message != "test error" {
		t.Errorf("LogError message = %q, want %q", first.Message, "test error")
	}
	if got := recordAttrs(first); len(got) != 1 || got[0].Key != "key" || got[0].Value.String() != "val" {
		t.Errorf("LogError dropped or mangled its attributes: %+v", got)
	}

	second := records[1]
	if second.Level != slog.LevelInfo {
		t.Errorf("LogInfo logged at %v, want %v", second.Level, slog.LevelInfo)
	}
	if second.Message != "test info" {
		t.Errorf("LogInfo message = %q, want %q", second.Message, "test info")
	}
	if got := recordAttrs(second); len(got) != 1 || got[0].Key != "count" || got[0].Value.Int64() != 3 {
		t.Errorf("LogInfo dropped or mangled its attributes: %+v", got)
	}
}

// TestContextLoggerAttrTypesAreNotStringified: slog.Int must reach the handler as
// an int64, not as the string "3". A handler that formats attributes (a log
// aggregator grouping by value) would otherwise treat every counter as text and
// stop aggregating it.
func TestContextLoggerAttrTypesAreNotStringified(t *testing.T) {
	h := captureLogs(t)
	ctx := InitApp(nil)

	ctx.LogInfo("typed", slog.Int("n", 7), slog.Bool("flag", true))

	records := h.snapshot()
	if len(records) != 1 {
		t.Fatalf("expected one record, got %d", len(records))
	}
	attrs := recordAttrs(records[0])
	if len(attrs) != 2 {
		t.Fatalf("expected two attributes, got %d", len(attrs))
	}
	if attrs[0].Value.Kind() != slog.KindInt64 || attrs[0].Value.Int64() != 7 {
		t.Errorf("slog.Int arrived as %v", attrs[0].Value)
	}
	if attrs[1].Value.Kind() != slog.KindBool || !attrs[1].Value.Bool() {
		t.Errorf("slog.Bool arrived as %v", attrs[1].Value)
	}
}
