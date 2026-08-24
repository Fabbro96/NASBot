package app

import (
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type mockBotWithSent struct {
	sentMessages []tgbotapi.Chattable
}

func (m *mockBotWithSent) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	m.sentMessages = append(m.sentMessages, c)
	return tgbotapi.Message{MessageID: 100}, nil
}

func (m *mockBotWithSent) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (m *mockBotWithSent) GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return nil
}

func (m *mockBotWithSent) StopReceivingUpdates() {}

func setupTestAppContextForDisks() *AppContext {
	cfg := &Config{
		AllowedUserID: 12345,
		Paths: PathsConfig{
			SSD: "/",
		},
		Notifications: NotificationsConfig{
			SecondaryDisks: map[string]ResourceConfig{
				"/mnt/HDD": {Enabled: true, WarningThreshold: 85, CriticalThreshold: 95},
			},
		},
	}
	ctx := InitApp(cfg)
	ctx.Settings.SetLanguage("it")
	return ctx
}

func TestIsVirtualOrIgnoredFS(t *testing.T) {
	tests := []struct {
		device     string
		fstype     string
		mountpoint string
		ignored    bool
	}{
		{"/dev/loop0", "squashfs", "/snap/core/123", true},
		{"tmpfs", "tmpfs", "/run/user/1000", true},
		{"overlay", "overlay", "/var/lib/docker/overlay2/xyz/merged", true},
		{"proc", "proc", "/proc", true},
		{"sysfs", "sysfs", "/sys", true},
		{"none", "cgroup2", "/sys/fs/cgroup", true},
		{"/dev/sda1", "ext4", "/boot", true},
		{"/dev/sda2", "vfat", "/boot/efi", true},
		{"/dev/sda3", "ext4", "/", false},
		{"/dev/sdb1", "ext4", "/mnt/HDD", false},
		{"/dev/sdc1", "ntfs", "/media/backup", false},
		{"/dev/nvme0n1p1", "btrfs", "/data", false},
	}

	for _, tc := range tests {
		got := isVirtualOrIgnoredFS(tc.device, tc.fstype, tc.mountpoint)
		if got != tc.ignored {
			t.Errorf("isVirtualOrIgnoredFS(%q, %q, %q) = %v; want %v", tc.device, tc.fstype, tc.mountpoint, got, tc.ignored)
		}
	}
}

func TestDiskMounts_BaselineInitialization(t *testing.T) {
	ctx := setupTestAppContextForDisks()
	bot := &mockBotWithSent{}

	if ctx.Monitor.DiskMountsInitialized {
		t.Fatal("DiskMountsInitialized should start as false")
	}

	checkDiskMounts(ctx, bot)

	if !ctx.Monitor.DiskMountsInitialized {
		t.Fatal("DiskMountsInitialized should be true after checkDiskMounts")
	}

	// Should not have sent any alerts during baseline init
	if len(bot.sentMessages) > 0 {
		t.Fatalf("Expected 0 messages on baseline initialization, got %d", len(bot.sentMessages))
	}
}

func TestDiskMounts_DiskRemovedDetection(t *testing.T) {
	ctx := setupTestAppContextForDisks()
	bot := &mockBotWithSent{}

	// Seed snapshot with an active mount
	ctx.Monitor.Mu.Lock()
	ctx.Monitor.DiskMountsInitialized = true
	ctx.Monitor.DiskMountsSnapshot = map[string]DiskMountInfo{
		"/mnt/HDD": {
			Mountpoint: "/mnt/HDD",
			Device:     "/dev/sda1",
			Fstype:     "ext4",
			TotalBytes: 2000 * 1024 * 1024 * 1024,
			FreeBytes:  500 * 1024 * 1024 * 1024,
			Accessible: true,
		},
	}
	ctx.Monitor.Mu.Unlock()

	// Simulate removed disk
	handleDiskRemoved(ctx, bot, "/mnt/HDD", DiskMountInfo{Mountpoint: "/mnt/HDD", Device: "/dev/sda1"})

	if len(bot.sentMessages) == 0 {
		t.Fatal("Expected an alert message for removed disk, got none")
	}

	msg, ok := bot.sentMessages[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig, got %T", bot.sentMessages[0])
	}

	if !strings.Contains(msg.Text, "/mnt/HDD") || !strings.Contains(msg.Text, "sda1") {
		t.Errorf("Message missing mount point or device info: %s", msg.Text)
	}

	// Verify event logged
	events := ctx.State.GetEvents()
	if len(events) == 0 || !strings.Contains(events[len(events)-1].Message, "unmounted") {
		t.Errorf("Expected unmounted event logged in State, got: %+v", events)
	}
}

func TestDiskMounts_DiskAddedDetection(t *testing.T) {
	ctx := setupTestAppContextForDisks()
	bot := &mockBotWithSent{}

	ctx.Monitor.Mu.Lock()
	ctx.Monitor.DiskMountsInitialized = true
	ctx.Monitor.DiskMountsSnapshot = map[string]DiskMountInfo{}
	ctx.Monitor.Mu.Unlock()

	info := DiskMountInfo{
		Mountpoint: "/mnt/USB_Drive",
		Device:     "/dev/sdd1",
		Fstype:     "ext4",
		TotalBytes: 500 * 1024 * 1024 * 1024,
		FreeBytes:  300 * 1024 * 1024 * 1024,
		Accessible: true,
	}

	handleDiskAdded(ctx, bot, "/mnt/USB_Drive", info)

	if len(bot.sentMessages) == 0 {
		t.Fatal("Expected an alert message for added disk, got none")
	}

	msg, ok := bot.sentMessages[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig, got %T", bot.sentMessages[0])
	}

	if !strings.Contains(msg.Text, "/mnt/USB_Drive") || !strings.Contains(msg.Text, "sdd1") {
		t.Errorf("Message missing mount point or device info: %s", msg.Text)
	}

	events := ctx.State.GetEvents()
	if len(events) == 0 || !strings.Contains(events[len(events)-1].Message, "mounted") {
		t.Errorf("Expected mounted event logged in State, got: %+v", events)
	}
}

func TestDiskMounts_DeviceNodeChangedDetection(t *testing.T) {
	ctx := setupTestAppContextForDisks()
	bot := &mockBotWithSent{}

	oldInfo := DiskMountInfo{
		Mountpoint: "/mnt/HDD",
		Device:     "/dev/sda1",
		Fstype:     "ext4",
		TotalBytes: 2000 * 1024 * 1024 * 1024,
		FreeBytes:  500 * 1024 * 1024 * 1024,
		Accessible: true,
	}

	newInfo := DiskMountInfo{
		Mountpoint: "/mnt/HDD",
		Device:     "/dev/sdb1",
		Fstype:     "ext4",
		TotalBytes: 2000 * 1024 * 1024 * 1024,
		FreeBytes:  500 * 1024 * 1024 * 1024,
		Accessible: true,
	}

	handleDiskReconnected(ctx, bot, "/mnt/HDD", oldInfo, newInfo)

	if len(bot.sentMessages) == 0 {
		t.Fatal("Expected an alert message for reconnected disk, got none")
	}

	msg, ok := bot.sentMessages[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig, got %T", bot.sentMessages[0])
	}

	// Verify it contains old and new device nodes
	if !strings.Contains(msg.Text, "/dev/sda1") || !strings.Contains(msg.Text, "/dev/sdb1") {
		t.Errorf("Message missing device node transition info: %s", msg.Text)
	}

	// Verify it contains Docker warning
	if !strings.Contains(msg.Text, "Docker") {
		t.Errorf("Message missing Docker warning: %s", msg.Text)
	}

	// Verify inline keyboard has restart action
	if msg.ReplyMarkup == nil {
		t.Error("Expected inline keyboard for quick Docker restart actions")
	}

	events := ctx.State.GetEvents()
	if len(events) == 0 || !strings.Contains(events[len(events)-1].Message, "reconnected") {
		t.Errorf("Expected reconnected event logged in State, got: %+v", events)
	}
}

func TestDiskMounts_IOErrorDetection(t *testing.T) {
	ctx := setupTestAppContextForDisks()
	bot := &mockBotWithSent{}

	info := DiskMountInfo{
		Mountpoint: "/mnt/HDD",
		Device:     "/dev/sdb1",
		Accessible: false,
		ErrorMsg:   "input/output error",
	}

	handleDiskIOError(ctx, bot, "/mnt/HDD", info)

	if len(bot.sentMessages) == 0 {
		t.Fatal("Expected an alert message for disk I/O error, got none")
	}

	msg, ok := bot.sentMessages[0].(tgbotapi.MessageConfig)
	if !ok {
		t.Fatalf("Expected tgbotapi.MessageConfig, got %T", bot.sentMessages[0])
	}

	if !strings.Contains(msg.Text, "input/output error") || !strings.Contains(msg.Text, "/mnt/HDD") {
		t.Errorf("Message missing error details: %s", msg.Text)
	}

	// Check cooldown works: calling immediately again should not send another alert
	bot.sentMessages = nil
	handleDiskIOError(ctx, bot, "/mnt/HDD", info)
	if len(bot.sentMessages) > 0 {
		t.Errorf("Expected cooldown to suppress immediate second I/O error alert, got %d messages", len(bot.sentMessages))
	}

	// Reset cooldown and verify alert sends again
	ctx.Monitor.Mu.Lock()
	ctx.Monitor.DiskMountAlertCooldown["/mnt/HDD"] = time.Now().Add(-1 * time.Hour)
	ctx.Monitor.Mu.Unlock()

	handleDiskIOError(ctx, bot, "/mnt/HDD", info)
	if len(bot.sentMessages) != 1 {
		t.Errorf("Expected alert after cooldown expired, got %d messages", len(bot.sentMessages))
	}
}
