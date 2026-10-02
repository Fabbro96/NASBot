package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"nasbot/internal/format"
	"nasbot/pkg/model"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/shirou/gopsutil/v3/disk"
)

// Cooldown duration for repeated disk mount/remove/reconnect alerts
const diskErrorAlertCooldown = 30 * time.Minute

// Maximum number of container names shown in alert messages to stay within Telegram limits
const maxContainersInAlert = 15

// diskCheckGuards holds the per-context reentrancy guard for checkDiskMounts,
// keyed by *MonitorState. It used to be a single package-level atomic.Bool, so
// two AppContexts (the tests, or a second InitApp) shared one flag and a test
// that left it true silently turned the next call into a no-op.
//
// TODO(model): this belongs in model.MonitorState as a plain bool (it is only
// ever touched with Monitor.Mu held, so no extra mutex is needed).
var (
	diskCheckGuardMu model.Mutex
	diskCheckGuards  = map[*model.MonitorState]*atomic.Bool{}
)

func diskCheckGuardFor(ms *model.MonitorState) *atomic.Bool {
	diskCheckGuardMu.Lock()
	defer diskCheckGuardMu.Unlock()
	g, ok := diskCheckGuards[ms]
	if !ok {
		if len(diskCheckGuards) > 64 {
			// Keep the table bounded: one entry per monitor state, pruned lazily.
			for k, v := range diskCheckGuards {
				if !v.Load() {
					delete(diskCheckGuards, k)
				}
			}
		}
		g = &atomic.Bool{}
		diskCheckGuards[ms] = g
	}
	return g
}

// checkDiskMounts inspects mounted filesystems and detects added, removed,
// reconnected/device-changed disks or I/O errors.
func checkDiskMounts(ctx *AppContext, bot BotAPI) {
	// Prevent overlapping executions if previous check is still running
	guard := diskCheckGuardFor(ctx.Monitor)
	if !guard.CompareAndSwap(false, true) {
		return
	}
	defer guard.Store(false)

	currentMounts := getCurrentDiskMounts(ctx)

	// Guard against first-boot flap: if we got zero mounts, don't initialize
	// the snapshot as empty (it would cause a flood of "disk added" alerts on
	// the next tick when partitions are properly enumerated).
	if len(currentMounts) == 0 {
		return
	}

	ctx.Monitor.Mu.Lock()
	if !ctx.Monitor.DiskMountsInitialized {
		ctx.Monitor.DiskMountsSnapshot = currentMounts
		ctx.Monitor.DiskMountsInitialized = true
		ctx.Monitor.Mu.Unlock()
		return
	}
	snapshot := make(map[string]DiskMountInfo, len(ctx.Monitor.DiskMountsSnapshot))
	for k, v := range ctx.Monitor.DiskMountsSnapshot {
		snapshot[k] = v
	}
	ctx.Monitor.Mu.Unlock()

	// 1. Detect Removed / Unmounted Disks
	for mount, oldInfo := range snapshot {
		if _, exists := currentMounts[mount]; !exists {
			handleDiskRemoved(ctx, bot, mount, oldInfo)
		}
	}

	// 2. Detect Added Disks, Device Node Changes, and I/O Errors
	for mount, currInfo := range currentMounts {
		oldInfo, exists := snapshot[mount]
		if !exists {
			// Newly added / mounted disk
			handleDiskAdded(ctx, bot, mount, currInfo)
			continue
		}

		// Check for device node change (e.g. /dev/sda1 -> /dev/sdb1)
		if oldInfo.Device != "" && currInfo.Device != "" && oldInfo.Device != currInfo.Device {
			handleDiskReconnected(ctx, bot, mount, oldInfo, currInfo)
			continue
		}

		// Check for I/O / Inaccessibility errors
		if !currInfo.Accessible && oldInfo.Accessible {
			handleDiskIOError(ctx, bot, mount, currInfo)
		}
	}

	// Update snapshot and clean up stale cooldown entries
	ctx.Monitor.Mu.Lock()
	ctx.Monitor.DiskMountsSnapshot = currentMounts
	// Purge cooldown entries for mounts no longer present
	for mount := range ctx.Monitor.DiskMountAlertCooldown {
		if _, exists := currentMounts[mount]; !exists {
			delete(ctx.Monitor.DiskMountAlertCooldown, mount)
		}
	}
	ctx.Monitor.Mu.Unlock()
}

// getCurrentDiskMounts gathers all active real partitions and mounts
func getCurrentDiskMounts(ctx *AppContext) map[string]DiskMountInfo {
	result := make(map[string]DiskMountInfo)

	partitions, err := disk.Partitions(true)
	if err == nil {
		for _, p := range partitions {
			if isVirtualOrIgnoredFS(p.Device, p.Fstype, p.Mountpoint) {
				continue
			}

			info := DiskMountInfo{
				Mountpoint: p.Mountpoint,
				Device:     p.Device,
				Fstype:     p.Fstype,
				Accessible: true,
			}

			// Check accessibility and get space
			d, usageErr := disk.Usage(p.Mountpoint)
			if usageErr == nil {
				info.TotalBytes = d.Total
				info.FreeBytes = d.Free
			} else {
				info.Accessible = false
				info.ErrorMsg = usageErr.Error()
			}

			result[p.Mountpoint] = info
		}
	}

	// Ensure configured paths (like SSD or secondary disks) are checked even if not in partitions
	if ctx != nil && ctx.Cfg() != nil {
		configuredPaths := []string{}
		if ctx.Cfg().Paths.SSD != "" {
			configuredPaths = append(configuredPaths, ctx.Cfg().Paths.SSD)
		}
		for secPath := range ctx.Cfg().Notifications.SecondaryDisks {
			if secPath != "" {
				configuredPaths = append(configuredPaths, secPath)
			}
		}

		for _, path := range configuredPaths {
			if _, exists := result[path]; !exists {
				// Path configured but missing in partitions: check if accessible via statfs
				d, usageErr := disk.Usage(path)
				if usageErr == nil {
					result[path] = DiskMountInfo{
						Mountpoint: path,
						Device:     "configured",
						Fstype:     d.Fstype,
						TotalBytes: d.Total,
						FreeBytes:  d.Free,
						Accessible: true,
					}
				} else {
					// Configured path is inaccessible - record it so the diff
					// logic can fire an I/O error alert instead of a generic
					// "disk unmounted" alert.
					result[path] = DiskMountInfo{
						Mountpoint: path,
						Device:     "configured",
						Accessible: false,
						ErrorMsg:   usageErr.Error(),
					}
				}
			}
		}
	}

	return result
}

// isVirtualOrIgnoredFS returns true for pseudo/virtual filesystems or Docker mounts
func isVirtualOrIgnoredFS(device, fstype, mountpoint string) bool {
	// Loop devices and snaps
	if strings.HasPrefix(device, "/dev/loop") {
		return true
	}

	// Virtual and kernel filesystems
	ignoredFSTypes := map[string]bool{
		"squashfs":    true,
		"tmpfs":       true,
		"devtmpfs":    true,
		"overlay":     true,
		"proc":        true,
		"sysfs":       true,
		"cgroup":      true,
		"cgroup2":     true,
		"nsfs":        true,
		"bpf":         true,
		"tracefs":     true,
		"pstore":      true,
		"autofs":      true,
		"mqueue":      true,
		"hugetlbfs":   true,
		"devpts":      true,
		"binfmt_misc": true,
		"configfs":    true,
		"securityfs":  true,
		"debugfs":     true,
		"fusectl":     true,
		"efivarfs":    true,
		"rpc_pipefs":  true,
		"ramfs":       true,
		"rootfs":      true,
		"devfs":       true,
		"selinuxfs":   true,
	}
	if ignoredFSTypes[fstype] {
		return true
	}

	// Ignored device names
	if device == "none" || device == "sunrpc" || device == "devpts" || device == "udev" || device == "systemd-1" {
		return true
	}

	// Internal Docker and systemd paths
	if strings.HasPrefix(mountpoint, "/var/lib/docker") ||
		strings.HasPrefix(mountpoint, "/var/run/docker") ||
		strings.HasPrefix(mountpoint, "/run/docker") ||
		strings.HasPrefix(mountpoint, "/var/lib/containerd") {
		return true
	}

	// Filter out standard non-storage system mounts like /boot
	if mountpoint == "/boot" || mountpoint == "/boot/efi" {
		return true
	}

	return false
}

// formatContainerList builds a Telegram-safe container list string, truncating if needed
func formatContainerList(affected []string) string {
	if len(affected) == 0 {
		return ""
	}
	display := affected
	suffix := ""
	if len(affected) > maxContainersInAlert {
		display = affected[:maxContainersInAlert]
		suffix = fmt.Sprintf(" _+%d altri_", len(affected)-maxContainersInAlert)
	}
	return "`" + strings.Join(display, "`, `") + "`" + suffix
}

// takeDiskAlertCooldown reports whether the mount may raise an alert now, and
// consumes the cooldown slot when it may.
//
// All the disk alert handlers go through this, including "disk added": a
// remove/add flap is one event, not two. Previously only handleDiskIOError
// consulted DiskMountAlertCooldown, so a USB disk flapping twice a minute
// produced two Telegram messages per flap, each one preceded by a `docker ps`
// with a 5s timeout. quietHours suppresses the messages but still lets the
// mount be tracked, which is the same rule the temperature, RAM and
// critical-container paths follow.
func takeDiskAlertCooldown(ctx *AppContext, mount string, quietHoursAllowed bool) bool {
	if quietHoursAllowed && ctx.IsQuietHours() {
		return false
	}
	ctx.Monitor.Mu.Lock()
	defer ctx.Monitor.Mu.Unlock()
	if last, ok := ctx.Monitor.DiskMountAlertCooldown[mount]; ok && time.Since(last) < diskErrorAlertCooldown {
		return false
	}
	ctx.Monitor.DiskMountAlertCooldown[mount] = time.Now()
	return true
}

// handleDiskRemoved notifies the user when a disk is unmounted or disconnected
func handleDiskRemoved(ctx *AppContext, bot BotAPI, mount string, oldInfo DiskMountInfo) {
	if !takeDiskAlertCooldown(ctx, mount, true) {
		slog.Debug("Disk unmount alert suppressed (cooldown or quiet hours)", "mount", mount)
		return
	}

	slog.Warn("Disk unmounted/removed", "mount", mount, "device", oldInfo.Device)
	ctx.State.AddEvent("critical", fmt.Sprintf("Disk %s (%s) unmounted", mount, oldInfo.Device))

	affected := getContainersUsingMount(ctx, mount)
	affectedText := ""
	if len(affected) > 0 {
		affectedText = "\n\n" + fmt.Sprintf(ctx.Tr("disk_affected_containers"), formatContainerList(affected))
	}

	devName := oldInfo.Device
	if devName == "" {
		devName = "unknown"
	}

	msg := fmt.Sprintf(ctx.Tr("disk_unmounted_alert"), mount, devName) + affectedText

	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = getDiskAlertKeyboard(ctx)
	safeSend(bot, m)
}

// handleDiskAdded notifies the user when a new disk is mounted
func handleDiskAdded(ctx *AppContext, bot BotAPI, mount string, info DiskMountInfo) {
	// Shares the cooldown with handleDiskRemoved: a remove/add flap is one
	// event, and the removal alert already told the user the disk went away.
	if !takeDiskAlertCooldown(ctx, mount, true) {
		slog.Debug("Disk added alert suppressed (cooldown or quiet hours)", "mount", mount)
		return
	}

	slog.Info("Disk mounted/added", "mount", mount, "device", info.Device)
	ctx.State.AddEvent("info", fmt.Sprintf("Disk %s (%s) mounted", mount, info.Device))

	devName := info.Device
	if devName == "" {
		devName = "unknown"
	}
	fsType := info.Fstype
	if fsType == "" {
		fsType = "filesystem"
	}
	sizeStr := format.FormatBytes(info.TotalBytes)
	freeStr := format.FormatBytes(info.FreeBytes)

	msg := fmt.Sprintf(ctx.Tr("disk_mounted_alert"), mount, devName, fsType, sizeStr, freeStr)

	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_refresh"), "refresh_status"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_docker"), "show_docker"),
		),
	)
	safeSend(bot, m)
}

// handleDiskReconnected notifies the user of a device node change (e.g. sda1 -> sdb1)
func handleDiskReconnected(ctx *AppContext, bot BotAPI, mount string, oldInfo, newInfo DiskMountInfo) {
	if !takeDiskAlertCooldown(ctx, mount, true) {
		slog.Debug("Disk reconnect alert suppressed (cooldown or quiet hours)", "mount", mount)
		return
	}

	slog.Warn("Disk reconnected with changed device node", "mount", mount, "old_device", oldInfo.Device, "new_device", newInfo.Device)
	ctx.State.AddEvent("critical", fmt.Sprintf("Disk %s reconnected: %s -> %s", mount, oldInfo.Device, newInfo.Device))

	affected := getContainersUsingMount(ctx, mount)
	affectedText := ""
	if len(affected) > 0 {
		affectedText = "\n\n" + fmt.Sprintf(ctx.Tr("disk_affected_containers"), formatContainerList(affected))
	}

	msg := fmt.Sprintf(ctx.Tr("disk_reconnected_alert"), mount, oldInfo.Device, newInfo.Device) +
		"\n\n" + ctx.Tr("disk_reconnect_docker_warn") + affectedText

	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = getDiskAlertKeyboard(ctx)
	safeSend(bot, m)
}

// handleDiskIOError notifies the user when a mount encounters I/O errors
func handleDiskIOError(ctx *AppContext, bot BotAPI, mount string, info DiskMountInfo) {
	if !takeDiskAlertCooldown(ctx, mount, false) {
		return
	}

	slog.Error("Disk I/O error detected", "mount", mount, "error", info.ErrorMsg)
	ctx.State.AddEvent("critical", fmt.Sprintf("Disk %s I/O error: %s", mount, info.ErrorMsg))

	devName := info.Device
	if devName == "" {
		devName = mount
	}
	errStr := info.ErrorMsg
	if errStr == "" {
		errStr = "I/O error / inaccessible"
	}

	msg := fmt.Sprintf(ctx.Tr("disk_io_error_alert"), mount, devName, errStr)

	m := tgbotapi.NewMessage(ctx.Cfg().AllowedUserID, msg)
	m.ParseMode = "Markdown"
	m.ReplyMarkup = getDiskAlertKeyboard(ctx)
	safeSend(bot, m)
}

// getContainersUsingMount finds running Docker containers mapping the given mount point
func getContainersUsingMount(ctx *AppContext, mountpoint string) []string {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := runCommandOutput(c, "docker", "ps", "--format", "{{.Names}}\t{{.Mounts}}")
	if err != nil {
		return nil
	}

	var affected []string
	cleanMount := strings.TrimRight(mountpoint, "/")

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			cName := strings.TrimSpace(parts[0])
			cMounts := strings.Split(parts[1], ",")
			for _, m := range cMounts {
				m = strings.TrimSpace(m)
				m = strings.TrimRight(m, "/")
				if m == cleanMount || strings.HasPrefix(m, cleanMount+"/") {
					affected = append(affected, cName)
					break
				}
			}
		}
	}
	return affected
}

// getDiskAlertKeyboard provides quick action buttons for disk-related alerts
func getDiskAlertKeyboard(ctx *AppContext) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 "+ctx.Tr("btn_restart_all_containers"), "docker_restart_all"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_docker"), "show_docker"),
			tgbotapi.NewInlineKeyboardButtonData(ctx.Tr("btn_refresh"), "refresh_status"),
		),
	)
}
