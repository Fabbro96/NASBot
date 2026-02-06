package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ═══════════════════════════════════════════════════════════════════
//  FILESYSTEM WATCHDOG - Lazy Evaluation Approach
// ═══════════════════════════════════════════════════════════════════
//
//  Designed for low-resource embedded systems (ARM, 1GB RAM)
//
//  Strategy:
//  1. Light check every 30-60 min using syscall.Statfs (instant, no I/O)
//  2. Deep scan ONLY when disk usage > 90% (emergency condition)
//  3. Memory-efficient: scan directory-by-directory, no full file lists
//
// ═══════════════════════════════════════════════════════════════════

// FSWatchdogConfig holds filesystem watchdog configuration
// Defined in config_types.go

// DirUsage holds directory usage info (memory-efficient)
type DirUsage struct {
	Path  string
	Size  int64
	Files int
}

// FileInfo holds basic file info for top-N tracking
type FileInfo struct {
	Path string
	Size int64
}

// FSWatchdog manages filesystem monitoring
type FSWatchdog struct {
	config         FSWatchdogConfig
	lastLightCheck time.Time
	lastDeepScan   time.Time
	lastAlertTime  time.Time
	isScanning     bool
	scanMutex      sync.Mutex
}

var (
	fsWatchdog     *FSWatchdog
	fsWatchdogOnce sync.Once
)

// GetFSWatchdog returns the singleton FSWatchdog instance
func GetFSWatchdog() *FSWatchdog {
	fsWatchdogOnce.Do(func() {
		fsWatchdog = &FSWatchdog{
			config: fsWatchdogConfigFromCfg(),
		}
	})
	return fsWatchdog
}

func fsWatchdogConfigFromCfg() FSWatchdogConfig {
	return FSWatchdogConfig{
		Enabled:           cfg.FSWatchdog.Enabled,
		CheckIntervalMins: cfg.FSWatchdog.CheckIntervalMins,
		WarningThreshold:  cfg.FSWatchdog.WarningThreshold,
		CriticalThreshold: cfg.FSWatchdog.CriticalThreshold,
		DeepScanPaths:     cfg.FSWatchdog.DeepScanPaths,
		ExcludePatterns:   cfg.FSWatchdog.ExcludePatterns,
		TopNFiles:         cfg.FSWatchdog.TopNFiles,
	}
}

func updateFSWatchdogConfig() {
	if fsWatchdog == nil {
		return
	}
	fsWatchdog.scanMutex.Lock()
	fsWatchdog.config = fsWatchdogConfigFromCfg()
	fsWatchdog.scanMutex.Unlock()
}

// ═══════════════════════════════════════════════════════════════════
//  LIGHT CHECK - Instant, no I/O overhead
// ═══════════════════════════════════════════════════════════════════

// StatfsResult holds the result of a statfs call
type StatfsResult struct {
	Path        string
	TotalBytes  uint64
	FreeBytes   uint64
	AvailBytes  uint64 // Available to non-root users
	UsedBytes   uint64
	UsedPercent float64
	Inodes      uint64
	FreeInodes  uint64
}

// GetDiskUsage performs a lightweight disk usage check using syscall.Statfs
// This is O(1), instant, and causes no disk I/O
func GetDiskUsage(path string) (*StatfsResult, error) {
	var stat syscall.Statfs_t

	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs %s: %w", path, err)
	}

	// Calculate sizes
	blockSize := uint64(stat.Bsize)
	totalBytes := stat.Blocks * blockSize
	freeBytes := stat.Bfree * blockSize
	availBytes := stat.Bavail * blockSize // Available to non-privileged users
	usedBytes := totalBytes - freeBytes

	// Calculate percentage
	var usedPercent float64
	if totalBytes > 0 {
		usedPercent = float64(usedBytes) / float64(totalBytes) * 100
	}

	return &StatfsResult{
		Path:        path,
		TotalBytes:  totalBytes,
		FreeBytes:   freeBytes,
		AvailBytes:  availBytes,
		UsedBytes:   usedBytes,
		UsedPercent: usedPercent,
		Inodes:      stat.Files,
		FreeInodes:  stat.Ffree,
	}, nil
}

// LightCheck performs a fast disk usage check
// Returns: usedPercent, freeGB, error
func (w *FSWatchdog) LightCheck(path string) (float64, float64, error) {
	result, err := GetDiskUsage(path)
	if err != nil {
		return 0, 0, err
	}

	freeGB := float64(result.FreeBytes) / 1024 / 1024 / 1024
	w.lastLightCheck = time.Now()

	return result.UsedPercent, freeGB, nil
}

// ═══════════════════════════════════════════════════════════════════
//  DEEP SCAN - Memory-efficient directory-by-directory scan
// ═══════════════════════════════════════════════════════════════════

// DeepScanResult holds the results of a deep filesystem scan
type DeepScanResult struct {
	ScanTime     time.Time
	Duration     time.Duration
	TotalScanned int64
	DirUsages    []DirUsage // Top directories by size
	LargestFiles []FileInfo // Top N largest files
	Errors       []string
}

// shouldExclude checks if a path should be excluded from scanning
func (w *FSWatchdog) shouldExclude(path string) bool {
	for _, pattern := range w.config.ExcludePatterns {
		if strings.HasPrefix(path, pattern) {
			return true
		}
	}
	return false
}

// scanDirectory scans a single directory and returns its total size
// Memory-efficient: does not store file list, only aggregates size
func (w *FSWatchdog) scanDirectory(dirPath string, topFiles *[]FileInfo, maxTopFiles int) (int64, int, error) {
	var totalSize int64
	var fileCount int

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		// Permission denied or other errors - skip silently
		return 0, 0, nil
	}

	for _, entry := range entries {
		fullPath := filepath.Join(dirPath, entry.Name())

		// Skip excluded paths
		if w.shouldExclude(fullPath) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.IsDir() {
			// Recursive scan for directories
			subSize, subCount, _ := w.scanDirectory(fullPath, topFiles, maxTopFiles)
			totalSize += subSize
			fileCount += subCount
		} else {
			// Regular file
			size := info.Size()
			totalSize += size
			fileCount++

			// Track top N largest files (memory-efficient insertion sort)
			if topFiles != nil && size > 0 {
				w.insertTopFile(topFiles, FileInfo{Path: fullPath, Size: size}, maxTopFiles)
			}
		}
	}

	return totalSize, fileCount, nil
}

// insertTopFile maintains a sorted slice of top N largest files
// Memory-efficient: keeps only N items, insertion sort O(N)
func (w *FSWatchdog) insertTopFile(files *[]FileInfo, newFile FileInfo, maxN int) {
	// Find insertion position
	pos := len(*files)
	for i, f := range *files {
		if newFile.Size > f.Size {
			pos = i
			break
		}
	}

	// Insert if within top N
	if pos < maxN {
		if len(*files) < maxN {
			*files = append(*files, FileInfo{})
		}
		// Shift elements
		copy((*files)[pos+1:], (*files)[pos:])
		(*files)[pos] = newFile

		// Trim to maxN
		if len(*files) > maxN {
			*files = (*files)[:maxN]
		}
	}
}

// DeepScan performs a comprehensive filesystem scan
// Only called when disk usage exceeds critical threshold
func (w *FSWatchdog) DeepScan(paths []string) *DeepScanResult {
	w.scanMutex.Lock()
	if w.isScanning {
		w.scanMutex.Unlock()
		return nil // Already scanning
	}
	w.isScanning = true
	w.scanMutex.Unlock()

	defer func() {
		w.scanMutex.Lock()
		w.isScanning = false
		w.scanMutex.Unlock()
	}()

	startTime := time.Now()
	result := &DeepScanResult{
		ScanTime:     startTime,
		DirUsages:    make([]DirUsage, 0),
		LargestFiles: make([]FileInfo, 0, w.config.TopNFiles),
		Errors:       make([]string, 0),
	}

	slog.Info("[FSWatchdog] Starting deep scan...")

	for _, basePath := range paths {
		if w.shouldExclude(basePath) {
			continue
		}

		// Scan first-level directories to get per-directory usage
		entries, err := os.ReadDir(basePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", basePath, err))
			continue
		}

		for _, entry := range entries {
			fullPath := filepath.Join(basePath, entry.Name())

			if w.shouldExclude(fullPath) {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				continue
			}

			if info.IsDir() {
				// Scan directory recursively
				size, files, _ := w.scanDirectory(fullPath, &result.LargestFiles, w.config.TopNFiles)
				if size > 0 {
					result.DirUsages = append(result.DirUsages, DirUsage{
						Path:  fullPath,
						Size:  size,
						Files: files,
					})
				}
				result.TotalScanned += size
			} else {
				// Root-level file
				result.TotalScanned += info.Size()
				w.insertTopFile(&result.LargestFiles, FileInfo{
					Path: fullPath,
					Size: info.Size(),
				}, w.config.TopNFiles)
			}
		}
	}

	// Sort directories by size (largest first)
	sort.Slice(result.DirUsages, func(i, j int) bool {
		return result.DirUsages[i].Size > result.DirUsages[j].Size
	})

	// Keep only top 20 directories
	if len(result.DirUsages) > 20 {
		result.DirUsages = result.DirUsages[:20]
	}

	result.Duration = time.Since(startTime)
	w.lastDeepScan = time.Now()

	slog.Info("[FSWatchdog] Deep scan complete",
		"duration", result.Duration.Round(time.Millisecond).String(),
		"scanned", formatBytes(uint64(result.TotalScanned)))

	return result
}

// ═══════════════════════════════════════════════════════════════════
//  WATCHDOG LOOP - Lazy Evaluation Strategy
// ═══════════════════════════════════════════════════════════════════

// RunFSWatchdog starts the filesystem watchdog goroutine
func RunFSWatchdog(bot *tgbotapi.BotAPI) {
	w := GetFSWatchdog()

	if !w.config.Enabled {
		slog.Info("[FSWatchdog] Disabled in config")
		return
	}

	interval := time.Duration(w.config.CheckIntervalMins) * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	slog.Info("[FSWatchdog] Started",
		"check_every", w.config.CheckIntervalMins,
		"warning_pct", w.config.WarningThreshold,
		"critical_pct", w.config.CriticalThreshold)

	// Initial check after 1 minute
	time.Sleep(1 * time.Minute)
	w.checkAndAlert(bot, "/")

	for range ticker.C {
		w.checkAndAlert(bot, "/")
	}
}

// checkAndAlert performs the lazy evaluation check
func (w *FSWatchdog) checkAndAlert(bot *tgbotapi.BotAPI, path string) {
	// Step 1: Light check (instant, no I/O)
	usedPercent, freeGB, err := w.LightCheck(path)
	if err != nil {
		slog.Error("[FSWatchdog] Light check failed", "err", err)
		return
	}

	slog.Info(fmt.Sprintf("[FSWatchdog] Light check: %.1f%% used, %.1fGB free", usedPercent, freeGB))

	// Step 2: Evaluate threshold
	if usedPercent < w.config.WarningThreshold {
		// All good, stay dormant
		return
	}

	// Step 3: Warning threshold reached
	if usedPercent >= w.config.WarningThreshold && usedPercent < w.config.CriticalThreshold {
		// Only alert once per hour
		if time.Since(w.lastAlertTime) < 1*time.Hour {
			return
		}

		if !isQuietHours() {
			msg := fmt.Sprintf("⚠️ *Disk Space Warning*\n\n"+
				"Path: `%s`\n"+
				"Used: `%.1f%%`\n"+
				"Free: `%.1fGB`\n\n"+
				"_Monitoring..._", path, usedPercent, freeGB)

			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}

		w.lastAlertTime = time.Now()
		addReportEvent("warning", fmt.Sprintf("Disk %s at %.1f%%", path, usedPercent))
		return
	}

	// Step 4: CRITICAL - Trigger deep scan
	if usedPercent >= w.config.CriticalThreshold {
		slog.Warn(fmt.Sprintf("[FSWatchdog] CRITICAL: %.1f%% - triggering deep scan", usedPercent))

		// Notify user
		if !isQuietHours() {
			msg := fmt.Sprintf("🚨 *Disk Space Critical!*\n\n"+
				"Path: `%s`\n"+
				"Used: `%.1f%%`\n"+
				"Free: `%.1fGB`\n\n"+
				"_Starting deep scan to identify large files..._", path, usedPercent, freeGB)

			m := tgbotapi.NewMessage(AllowedUserID, msg)
			m.ParseMode = "Markdown"
			bot.Send(m)
		}

		// Run deep scan in background to not block
		go func() {
			result := w.DeepScan(w.config.DeepScanPaths)
			if result == nil {
				return // Scan already in progress
			}

			// Send results
			w.sendDeepScanReport(bot, result, usedPercent, freeGB)
		}()

		w.lastAlertTime = time.Now()
		addReportEvent("critical", fmt.Sprintf("Disk %s at %.1f%% - deep scan triggered", path, usedPercent))
	}
}

// sendDeepScanReport formats and sends the deep scan results
func (w *FSWatchdog) sendDeepScanReport(bot *tgbotapi.BotAPI, result *DeepScanResult, usedPercent, freeGB float64) {
	var b strings.Builder

	b.WriteString("📊 *Deep Scan Results*\n\n")
	b.WriteString(fmt.Sprintf("⏱ Scan time: `%v`\n", result.Duration.Round(time.Millisecond)))
	b.WriteString(fmt.Sprintf("📁 Total scanned: `%s`\n\n", formatBytes(uint64(result.TotalScanned))))

	// Top directories
	if len(result.DirUsages) > 0 {
		b.WriteString("*📁 Largest Directories:*\n")
		for i, dir := range result.DirUsages {
			if i >= 10 {
				break
			}
			b.WriteString(fmt.Sprintf("`%s` %s\n",
				formatBytes(uint64(dir.Size)),
				truncatePath(dir.Path, 35)))
		}
		b.WriteString("\n")
	}

	// Top files
	if len(result.LargestFiles) > 0 {
		b.WriteString("*📄 Largest Files:*\n")
		for i, file := range result.LargestFiles {
			if i >= 10 {
				break
			}
			b.WriteString(fmt.Sprintf("`%s` %s\n",
				formatBytes(uint64(file.Size)),
				truncatePath(file.Path, 35)))
		}
	}

	// Errors (if any)
	if len(result.Errors) > 0 {
		b.WriteString(fmt.Sprintf("\n_⚠️ %d paths skipped due to errors_", len(result.Errors)))
	}

	if !isQuietHours() {
		m := tgbotapi.NewMessage(AllowedUserID, b.String())
		m.ParseMode = "Markdown"
		bot.Send(m)
	}
}

// truncatePath shortens a path for display
func truncatePath(path string, maxLen int) string {
	if len(path) <= maxLen {
		return path
	}
	// Keep the last part of the path
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path[:maxLen-3] + "..."
	}
	// Show .../last/two/parts
	result := "..." + filepath.Join(parts[len(parts)-3:]...)
	if len(result) > maxLen {
		return result[:maxLen-3] + "..."
	}
	return result
}

// ═══════════════════════════════════════════════════════════════════
//  MANUAL TRIGGER (for /diskinfo command)
// ═══════════════════════════════════════════════════════════════════

// GetDiskInfoText returns disk usage info for manual command
func GetDiskInfoText() string {
	var b strings.Builder
	b.WriteString("💾 *Disk Status*\n\n")

	for _, path := range []string{"/", PathSSD, PathHDD} {
		result, err := GetDiskUsage(path)
		if err != nil {
			continue
		}

		icon := "✅"
		if result.UsedPercent >= 90 {
			icon = "🚨"
		} else if result.UsedPercent >= 80 {
			icon = "⚠️"
		}

		b.WriteString(fmt.Sprintf("%s `%s`\n", icon, path))
		b.WriteString(fmt.Sprintf("   Used: `%.1f%%` · Free: `%s`\n",
			result.UsedPercent,
			formatBytes(result.FreeBytes)))
		b.WriteString(fmt.Sprintf("   Inodes: `%.1f%%` free\n\n",
			float64(result.FreeInodes)/float64(result.Inodes)*100))
	}

	w := GetFSWatchdog()
	if !w.lastLightCheck.IsZero() {
		b.WriteString(fmt.Sprintf("_Last check: %s_\n", w.lastLightCheck.Format("15:04")))
	}
	if !w.lastDeepScan.IsZero() {
		b.WriteString(fmt.Sprintf("_Last deep scan: %s_", w.lastDeepScan.Format("02/01 15:04")))
	}

	return b.String()
}
