package model

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// AppContext holds the application dependencies and state.
type AppContext struct {
	// Config is the legacy configuration pointer.
	//
	// Deprecated: read the configuration with Cfg(). This field is kept only so
	// that contexts assembled by hand still carry a configuration, and because
	// InitApp fills it for backwards compatibility. Nothing in the live path
	// may read it: a configuration reload publishes a brand-new snapshot
	// instead of mutating this struct, so readers of this pointer would keep
	// seeing the values from boot time.
	Config *Config

	// cfg holds the published configuration snapshot. It is swapped by pointer
	// and never written in place, so a reader can hold the pointer it got from
	// Cfg() and keep a consistent view for as long as it needs it.
	cfg atomic.Pointer[Config]

	Stats    *ThreadSafeStats
	State    *RuntimeState
	Bot      *BotContext
	Docker   *DockerManager
	Monitor  *MonitorState
	Settings *UserSettings
	HTTP     *http.Client
}

// ThreadSafeStats wraps stats with a mutex
type ThreadSafeStats struct {
	Mu    RWMutex
	Data  Stats
	Ready bool
}

// RuntimeState holds runtime volatile state
type RuntimeState struct {
	Mu                  Mutex
	ResourceStress      map[string]*StressTracker
	DockerFailure       time.Time
	LastReport          time.Time
	LastReleaseNotified string
	ReportEvents        []ReportEvent
	DiskHistory         []DiskUsagePoint
	TimeLocation        *time.Location
}

// BotContext holds bot-specific interaction state
type BotContext struct {
	Mu                     Mutex
	StartTime              time.Time
	PendingAction          string
	PendingContainerAction string
	PendingContainerName   string
}

// DockerManager holds Docker monitoring state
type DockerManager struct {
	Mu                RWMutex
	Cache             DockerCache
	AutoRestarts      map[string][]time.Time
	LastStates        map[string]bool      // true = running
	ContainerDowntime map[string]time.Time // When it went down
	PruneDoneToday    bool
}

// SmartResult holds the last known SMART status for a disk
type SmartResult struct {
	Temp   int
	Health string
}

// MonitorState holds historical trends and alert states
type MonitorState struct {
	Mu                         Mutex
	CPUTrend                   []TrendPoint
	RAMTrend                   []TrendPoint
	LastTempAlert              time.Time
	Healthchecks               HealthchecksState
	HealthInDowntime           bool
	RaidLastSignature          string
	RaidDownSince              time.Time
	RaidAlertTime              time.Time
	LastCriticalAlert          time.Time
	LastCriticalContainerAlert map[string]time.Time
	SmartLastCheckTime         time.Time
	SmartCache                 map[string]SmartResult
	NetFailCount               int
	NetLastCheckTime           time.Time
	NetConsecutiveDegraded     int
	NetDownSince               time.Time
	NetDownAlertTime           time.Time
	NetDNSAlertTime            time.Time
	NetForceRebootTriggered    bool
	KwLastSignatures           map[string]string
	KwInitialized              bool
	KwLastCheckTime            time.Time
	KwConsecutiveCheckErrors   int
	KwLastCheckError           string
	RecentOOMs                 []time.Time
	DiskMountsSnapshot         map[string]DiskMountInfo
	DiskMountsInitialized      bool
	DiskMountAlertCooldown     map[string]time.Time
}

// UserSettings holds persistent user preferences (loaded from JSON).
//
// The fields are exported so that state.go can restore a persisted blob in one
// shot while holding Mu. Every other writer must go through the setters below:
// IsQuietHours and the getters read under RLock, and a plain field assignment
// would not take the write lock at all.
type UserSettings struct {
	Mu             RWMutex
	Language       string
	ReportsEnabled bool
	ReportInterval int
	ReportTimes    []TimePoint
	ReportDays     []int // 0=Sunday, 1=Monday, ..., 6=Saturday. Empty = based on ReportInterval
	QuietHours     QuietSettings
	DockerPrune    PruneSettings

	// reportsChanged is closed and replaced every time the report schedule
	// changes, so the scheduler can wait on a notification instead of sleeping
	// until the next scheduled report. nil until the first reader asks for it,
	// which is why OnReportsChanged takes the write lock.
	reportsChanged chan struct{}
}

type TimePoint struct {
	Hour   int
	Minute int
}

type QuietSettings struct {
	Enabled bool
	Start   TimePoint
	End     TimePoint
}

type PruneSettings struct {
	Enabled bool
	Day     string
	Hour    int
}

// InitApp initializes the application context
func InitApp(cfg *Config) *AppContext {
	// Default settings seeded from config, overridden by persisted state later.
	reportsEnabled := true
	reportInterval := 1
	reportTimes := []TimePoint{{7, 30}, {18, 30}}

	quietHours := QuietSettings{
		Enabled: true,
		Start:   TimePoint{23, 30},
		End:     TimePoint{7, 0},
	}
	dockerPrune := PruneSettings{Enabled: true, Day: "sunday", Hour: 4}

	if cfg != nil {
		reportsEnabled = cfg.Reports.Enabled
		if cfg.Reports.IntervalDays > 0 {
			reportInterval = cfg.Reports.IntervalDays
		}
		if len(cfg.Reports.Times) > 0 {
			reportsTimes := make([]TimePoint, 0, len(cfg.Reports.Times))
			for _, tc := range cfg.Reports.Times {
				reportsTimes = append(reportsTimes, TimePoint{Hour: tc.Hour, Minute: tc.Minute})
			}
			reportTimes = reportsTimes
		}

		// Quiet hours defaults.
		quietHours = QuietSettings{
			Enabled: cfg.QuietHours.Enabled,
			Start:   TimePoint{cfg.QuietHours.StartHour, cfg.QuietHours.StartMinute},
			End:     TimePoint{cfg.QuietHours.EndHour, cfg.QuietHours.EndMinute},
		}

		// Docker prune defaults.
		dockerPrune = PruneSettings{
			Enabled: cfg.Docker.WeeklyPrune.Enabled,
			Day:     cfg.Docker.WeeklyPrune.Day,
			Hour:    cfg.Docker.WeeklyPrune.Hour,
		}
	}

	// Initialize HTTP client
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        5,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	app := &AppContext{
		Config: cfg,
		Stats:  &ThreadSafeStats{},
		State: &RuntimeState{
			ResourceStress: make(map[string]*StressTracker),
			ReportEvents:   make([]ReportEvent, 0),
			DiskHistory:    make([]DiskUsagePoint, 0, 288),
			TimeLocation:   time.UTC, // Default, updated in main
		},
		Bot: &BotContext{
			StartTime: time.Now(),
		},
		Docker: &DockerManager{
			AutoRestarts:      make(map[string][]time.Time),
			LastStates:        make(map[string]bool),
			ContainerDowntime: make(map[string]time.Time),
		},
		Monitor: &MonitorState{
			CPUTrend:                   make([]TrendPoint, 0, 72),
			RAMTrend:                   make([]TrendPoint, 0, 72),
			LastCriticalContainerAlert: make(map[string]time.Time),
			SmartCache:                 make(map[string]SmartResult),
			KwLastSignatures:           make(map[string]string),
			DiskMountsSnapshot:         make(map[string]DiskMountInfo),
			DiskMountAlertCooldown:     make(map[string]time.Time),
		},
		Settings: &UserSettings{
			Language:       "en",
			ReportsEnabled: reportsEnabled,
			ReportInterval: reportInterval,
			ReportTimes:    reportTimes,
			QuietHours:     quietHours,
			DockerPrune:    dockerPrune,
		},
		HTTP: httpClient,
	}

	// Publish the initial configuration. A nil argument still yields a usable
	// snapshot so that readers never have to nil-check Cfg().
	if cfg == nil {
		cfg = &Config{}
	}
	app.SetConfig(cfg)

	// Initialize Stress trackers
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		app.State.ResourceStress[res] = &StressTracker{}
	}

	return app
}

// Cfg returns the published configuration snapshot.
//
// The snapshot is never modified after publication: a reload builds a new
// Config and swaps the pointer with SetConfig. A caller may therefore keep the
// pointer it received for the whole duration of a unit of work and read
// consistent values from it.
func (c *AppContext) Cfg() *Config {
	if c == nil {
		return nil
	}
	if snapshot := c.cfg.Load(); snapshot != nil {
		return snapshot
	}
	// Nothing has been published yet: contexts built by hand (tests) still carry
	// the legacy field.
	return c.Config
}

// SetConfig publishes cfg as the configuration snapshot.
//
// The caller must not modify cfg afterwards: every reader that got it from Cfg()
// can still be holding the pointer. To change the configuration, build a new
// Config from the file and publish that one instead.
func (ctx *AppContext) SetConfig(cfg *Config) {
	if ctx == nil || cfg == nil {
		return
	}
	ctx.cfg.Store(cfg)
}

// ThreadSafeStats Methods
func (ts *ThreadSafeStats) Get() (Stats, bool) {
	ts.Mu.RLock()
	defer ts.Mu.RUnlock()
	return cloneStats(ts.Data), ts.Ready
}

// cloneStats deep-copies the map and slice fields of Stats.
//
// Stats is returned by value, but SecondaryVols is a map and TopCPU/TopRAM are
// slices: handing those out by reference lets a caller keep reading them while
// the collector replaces them, which the race detector reports and which can
// panic with "concurrent map iteration and map write". The copy is shallow on
// purpose: VolumeStats and ProcInfo hold only scalars.
func cloneStats(s Stats) Stats {
	if s.SecondaryVols != nil {
		vols := make(map[string]VolumeStats, len(s.SecondaryVols))
		for k, v := range s.SecondaryVols {
			vols[k] = v
		}
		s.SecondaryVols = vols
	}
	s.TopCPU = cloneProcInfo(s.TopCPU)
	s.TopRAM = cloneProcInfo(s.TopRAM)
	return s
}

func cloneProcInfo(src []ProcInfo) []ProcInfo {
	if src == nil {
		return nil
	}
	out := make([]ProcInfo, len(src))
	copy(out, src)
	return out
}

func (ts *ThreadSafeStats) Set(s Stats) {
	ts.Mu.Lock()
	defer ts.Mu.Unlock()
	ts.Data = s
	ts.Ready = true
}

// RuntimeState Methods
func (rs *RuntimeState) AddEvent(eventType, message string) {
	rs.Mu.Lock()
	defer rs.Mu.Unlock()
	rs.ReportEvents = append(rs.ReportEvents, ReportEvent{
		Time:    time.Now(),
		Type:    eventType,
		Message: message,
	})
	// Keep last 100 events
	if len(rs.ReportEvents) > 100 {
		rs.ReportEvents = rs.ReportEvents[len(rs.ReportEvents)-100:]
	}
}

func (rs *RuntimeState) GetEvents() []ReportEvent {
	rs.Mu.Lock()
	defer rs.Mu.Unlock()
	// Return copy
	events := make([]ReportEvent, len(rs.ReportEvents))
	copy(events, rs.ReportEvents)
	return events
}

func (rs *RuntimeState) ClearEvents() {
	rs.Mu.Lock()
	defer rs.Mu.Unlock()
	rs.ReportEvents = []ReportEvent{}
}

// BotContext Methods
func (b *BotContext) SetPendingAction(action string) {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.PendingAction = action
}

func (b *BotContext) GetPendingAction() string {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	return b.PendingAction
}

func (b *BotContext) ClearPendingAction() {
	b.Mu.Lock()
	defer b.Mu.Unlock()
	b.PendingAction = ""
}

// Settings methods (Thread-safe accessors)
func (s *UserSettings) GetLanguage() string {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.Language
}

func (s *UserSettings) SetLanguage(lang string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Language = lang
}

// OnReportsChanged returns a channel that is closed the next time the report
// schedule changes. Every report setter calls signalReportsChanged, so a change
// made from the settings buttons reaches the scheduler immediately instead of
// waiting for the next run.
//
// Callers must re-read the channel after it fires: signalReportsChanged replaces
// the field before closing the old channel, so a reader that kept the closed one
// gets a live one on the next call. That is what keeps a scheduler loop from
// spinning on an already-closed channel.
func (s *UserSettings) OnReportsChanged() <-chan struct{} {
	// The write lock, not RLock: a context built by hand (tests) has no channel
	// yet, and this is where it gets created.
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if s.reportsChanged == nil {
		s.reportsChanged = make(chan struct{})
	}
	return s.reportsChanged
}

// signalReportsChanged wakes the report scheduler. Call with s.Mu held for
// writing.
//
// The field is replaced *before* the old channel is closed, so a reader that
// already grabbed the closed channel can re-read and find a live one. Closing
// under the lock is safe: receivers only select on the channel and never need
// s.Mu to receive, so nobody can block on a lock we are holding.
func (s *UserSettings) signalReportsChanged() {
	if s.reportsChanged == nil {
		// No reader yet. OnReportsChanged creates a fresh live channel, so
		// there is nothing to notify.
		s.reportsChanged = make(chan struct{})
		return
	}
	old := s.reportsChanged
	s.reportsChanged = make(chan struct{})
	close(old)
}

func (s *UserSettings) GetReportsSettings() (enabled bool, interval int, times []TimePoint) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	enabled = s.ReportsEnabled
	interval = s.ReportInterval
	times = make([]TimePoint, len(s.ReportTimes))
	copy(times, s.ReportTimes)
	return
}

func (s *UserSettings) GetReportsDays() []int {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	days := make([]int, len(s.ReportDays))
	copy(days, s.ReportDays)
	return days
}

func (s *UserSettings) SetReportsDays(days []int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ReportDays = make([]int, len(days))
	copy(s.ReportDays, days)
	sort.Ints(s.ReportDays)
	s.signalReportsChanged()
}

func (s *UserSettings) ToggleReportDay(day int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	found := false
	var newDays []int
	for _, d := range s.ReportDays {
		if d == day {
			found = true
		} else {
			newDays = append(newDays, d)
		}
	}
	if !found {
		newDays = append(newDays, day)
	}
	sort.Ints(newDays)
	s.ReportDays = newDays
	s.signalReportsChanged()
}

func (s *UserSettings) HasReportDay(day int) bool {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	for _, d := range s.ReportDays {
		if d == day {
			return true
		}
	}
	return false
}

func (s *UserSettings) GetReportsDetailedSettings() (enabled bool, interval int, times []TimePoint, days []int) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	enabled = s.ReportsEnabled
	interval = s.ReportInterval
	times = make([]TimePoint, len(s.ReportTimes))
	copy(times, s.ReportTimes)
	days = make([]int, len(s.ReportDays))
	copy(days, s.ReportDays)
	return
}

func (s *UserSettings) SetReportsEnabled(enabled bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ReportsEnabled = enabled
	s.signalReportsChanged()
}

func (s *UserSettings) SetReportInterval(interval int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ReportInterval = interval
	s.signalReportsChanged()
}

// SetReportsSettings replaces the report schedule as a unit, which is how the
// button handlers change it: they compute the new value from the old one and
// must not expose a half-updated schedule to a concurrent reader.
func (s *UserSettings) SetReportsSettings(enabled bool, interval int, times []TimePoint) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ReportsEnabled = enabled
	s.ReportInterval = interval
	s.ReportTimes = append(make([]TimePoint, 0, len(times)), times...)
	s.signalReportsChanged()
}

func (s *UserSettings) AddReportTime(tp TimePoint) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.ReportTimes = append(s.ReportTimes, tp)
	s.signalReportsChanged()
}

// RemoveReportTime drops the time point at idx and reports whether it existed.
func (s *UserSettings) RemoveReportTime(idx int) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if idx < 0 || idx >= len(s.ReportTimes) {
		return false
	}
	s.ReportTimes = append(s.ReportTimes[:idx], s.ReportTimes[idx+1:]...)
	s.signalReportsChanged()
	return true
}

// GetQuietHours returns a copy of the quiet hours window.
func (s *UserSettings) GetQuietHours() QuietSettings {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.QuietHours
}

// SetQuietHours replaces the quiet hours window as a unit.
func (s *UserSettings) SetQuietHours(q QuietSettings) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.QuietHours = q
}

func (s *UserSettings) SetQuietHoursEnabled(enabled bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.QuietHours.Enabled = enabled
}

// GetDockerPrune returns a copy of the weekly prune schedule.
func (s *UserSettings) GetDockerPrune() PruneSettings {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return s.DockerPrune
}

// SetDockerPrune replaces the weekly prune schedule as a unit.
func (s *UserSettings) SetDockerPrune(p PruneSettings) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.DockerPrune = p
}

func (s *UserSettings) SetDockerPruneEnabled(enabled bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.DockerPrune.Enabled = enabled
}

// SetDockerPruneDay stores a weekday name and reports whether it is a valid one.
func (s *UserSettings) SetDockerPruneDay(day string) bool {
	normalized := strings.ToLower(strings.TrimSpace(day))
	switch normalized {
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
	default:
		return false
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.DockerPrune.Day = normalized
	return true
}

func (s *UserSettings) SetDockerPruneHour(hour int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.DockerPrune.Hour = hour
}

// Helpers designed to bridge the gap during refactor
func (ctx *AppContext) GetStats() (Stats, bool) {
	return ctx.Stats.Get()
}

// IsQuietHours returns true if we are currently in quiet hours
func (ctx *AppContext) IsQuietHours() bool {
	ctx.Settings.Mu.RLock()
	defer ctx.Settings.Mu.RUnlock()

	q := ctx.Settings.QuietHours
	if !q.Enabled {
		return false
	}

	// TimeLocation is set once at start-up, but a context built by hand (tests)
	// leaves it nil, and time.Now().In(nil) panics.
	loc := ctx.State.TimeLocation
	if loc == nil {
		loc = time.Local
	}

	now := time.Now().In(loc)
	nowMin := now.Hour()*60 + now.Minute()
	startMin := q.Start.Hour*60 + q.Start.Minute
	endMin := q.End.Hour*60 + q.End.Minute
	if startMin == endMin {
		// Avoid muting notifications for a zero-length quiet window.
		return false
	}

	if startMin < endMin {
		// Same day (e.g. 14:00 to 16:00)
		return nowMin >= startMin && nowMin < endMin
	} else {
		// Overnight (e.g. 22:00 to 07:00)
		return nowMin >= startMin || nowMin < endMin
	}
}

func (ctx *AppContext) LogError(msg string, args ...any) {
	slog.Error(msg, args...)
}

func (ctx *AppContext) LogInfo(msg string, args ...any) {
	slog.Info(msg, args...)
}

// Tr translates a key using the current language setting
func (ctx *AppContext) Tr(key string) string {
	lang := ctx.Settings.GetLanguage()
	if lang == "" {
		lang = "en"
	}
	return Translate(lang, key)
}
