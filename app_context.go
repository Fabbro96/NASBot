package main

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"
)

// AppContext holds the application dependencies and state.
type AppContext struct {
	Config   *Config
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
	mu    sync.RWMutex
	Data  Stats
	Ready bool
}

// RuntimeState holds runtime volatile state
type RuntimeState struct {
	mu             sync.Mutex
	ResourceStress map[string]*StressTracker
	DockerFailure  time.Time
	LastReport     time.Time
	ReportEvents   []ReportEvent
	DiskHistory    []DiskUsagePoint
	PIDFile        *os.File
	TimeLocation   *time.Location
}

// BotContext holds bot-specific interaction state
type BotContext struct {
	mu                     sync.Mutex
	StartTime              time.Time
	PendingAction          string
	PendingContainerAction string
	PendingContainerName   string
}

// DockerManager holds Docker monitoring state
type DockerManager struct {
	mu                sync.RWMutex
	Cache             DockerCache
	AutoRestarts      map[string][]time.Time
	LastStates        map[string]bool      // true = running
	ContainerDowntime map[string]time.Time // When it went down
	PruneDoneToday    bool
}

// MonitorState holds historical trends and alert states
type MonitorState struct {
	mu                         sync.Mutex
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
	NetFailCount               int
	NetDownSince               time.Time
	NetDownAlertTime           time.Time
	NetDNSAlertTime            time.Time
	KwLastSignatures           map[string]string
	KwInitialized              bool
}

// UserSettings holds persistent user preferences (loaded from JSON)
type UserSettings struct {
	mu            sync.RWMutex
	Language      string
	ReportMode    int
	ReportMorning TimePoint
	ReportEvening TimePoint
	QuietHours    QuietSettings
	DockerPrune   PruneSettings
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
			KwLastSignatures:           make(map[string]string),
		},
		Settings: &UserSettings{
			Language:      "en",
			ReportMode:    2,
			ReportMorning: TimePoint{7, 30},
			ReportEvening: TimePoint{18, 30},
			QuietHours: QuietSettings{
				Enabled: true,
				Start:   TimePoint{23, 30},
				End:     TimePoint{7, 0},
			},
			DockerPrune: PruneSettings{
				Enabled: true,
				Day:     "sunday",
				Hour:    4,
			},
		},
		HTTP: httpClient,
	}

	// Initialize Stress trackers
	for _, res := range []string{"CPU", "RAM", "Swap", "SSD", "HDD"} {
		app.State.ResourceStress[res] = &StressTracker{}
	}

	return app
}

// ThreadSafeStats Methods
func (ts *ThreadSafeStats) Get() (Stats, bool) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.Data, ts.Ready
}

func (ts *ThreadSafeStats) Set(s Stats) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.Data = s
	ts.Ready = true
}

// RuntimeState Methods
func (rs *RuntimeState) AddEvent(eventType, message string) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
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
	rs.mu.Lock()
	defer rs.mu.Unlock()
	// Return copy
	events := make([]ReportEvent, len(rs.ReportEvents))
	copy(events, rs.ReportEvents)
	return events
}

func (rs *RuntimeState) ClearEvents() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.ReportEvents = []ReportEvent{}
}

// BotContext Methods
func (b *BotContext) SetPendingAction(action string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.PendingAction = action
}

func (b *BotContext) GetPendingAction() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.PendingAction
}

func (b *BotContext) ClearPendingAction() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.PendingAction = ""
}

// Settings methods (Thread-safe accessors)
func (s *UserSettings) GetLanguage() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Language
}

func (s *UserSettings) SetLanguage(lang string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Language = lang
}

func (s *UserSettings) GetReportMode() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ReportMode
}

// Helpers designed to bridge the gap during refactor
func (ctx *AppContext) GetStats() (Stats, bool) {
	return ctx.Stats.Get()
}

// IsQuietHours returns true if we are currently in quiet hours
func (ctx *AppContext) IsQuietHours() bool {
	ctx.Settings.mu.RLock()
	defer ctx.Settings.mu.RUnlock()

	q := ctx.Settings.QuietHours
	if !q.Enabled {
		return false
	}

	now := time.Now().In(ctx.State.TimeLocation)
	nowMin := now.Hour()*60 + now.Minute()
	startMin := q.Start.Hour*60 + q.Start.Minute
	endMin := q.End.Hour*60 + q.End.Minute

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
	t, ok := translations[lang]
	if !ok {
		t = translations["en"]
	}
	if v, ok := t[key]; ok {
		return v
	}
	// Fallback to English
	if tEn, ok := translations["en"]; ok {
		if v, ok := tEn[key]; ok {
			return v
		}
	}
	return key
}
