package model

import "time"

// VolumeStats holds disk usage statistics
type VolumeStats struct {
	Used float64
	Free uint64
}

// Stats holds all system statistics
type Stats struct {
	CPU, RAM, Swap              float64
	RAMFreeMB, RAMTotalMB       uint64
	Load1m, Load5m, Load15m     float64
	Uptime                      uint64
	VolSSD                      VolumeStats
	SecondaryVols               map[string]VolumeStats
	ReadMBs, WriteMBs, DiskUtil float64
	NetRxMbps, NetTxMbps        float64
	NetRxTotalMB, NetTxTotalMB  float64
	TopCPU, TopRAM              []ProcInfo
}

// ProcInfo holds process information
type ProcInfo struct {
	Name string
	Mem  float64
	Cpu  float64
}

// ContainerInfo holds Docker container information
type ContainerInfo struct {
	Name    string
	Status  string
	Image   string
	ID      string
	Running bool
}

type DiskUsagePoint struct {
	Time          time.Time
	SSDUsed       float64
	SSDFree       uint64
	SecondaryUsed map[string]float64
	SecondaryFree map[string]uint64
}

// ReportEvent tracks events for the periodic report
type ReportEvent struct {
	Time    time.Time
	Type    string // "warning", "critical", "action", "info"
	Message string
}

// StressTracker tracks stress periods for a resource
type StressTracker struct {
	CurrentStart  time.Time
	StressCount   int
	LongestStress time.Duration
	TotalStress   time.Duration
	Notified      bool
}

// DiskPrediction holds disk prediction results
type DiskPrediction struct {
	DaysUntilFull float64
	GBPerDay      float64
}

// TrendPoint stores a single metric at a point in time
type TrendPoint struct {
	Time  time.Time
	Value float64
}

// DockerCache holds cached container list with TTL
type DockerCache struct {
	Containers []ContainerInfo
	LastUpdate time.Time
}
