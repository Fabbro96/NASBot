package commands

import (
	"strings"
	"testing"

	"nasbot/pkg/model"
)

func setupTestContext() *model.AppContext {
	cfg := &model.Config{
		Paths: model.PathsConfig{
			SSD: "/",
			HDD: "/tmp",
		},
	}
	ctx := model.InitApp(cfg)

	// Inject a stub translation resolver
	model.Translate = func(_ string, key string) string {
		return "[" + key + "]" // simple wrapper to verify key usage
	}

	return ctx
}

func TestGetStatusText(t *testing.T) {
	ctx := setupTestContext()

	// Fill with some dummy stats to avoid loading defaults inside tests
	ctx.Stats.Set(model.Stats{
		CPU:    42.5,
		RAM:    60.0,
		Swap:   10.0,
		VolSSD: model.VolumeStats{Used: 50.0},
		VolHDD: model.VolumeStats{Used: 30.0},
		Uptime: 123456,
	})

	text := GetStatusText(ctx)

	if !strings.Contains(text, "[status_title]") {
		t.Errorf("Expected status title translation key, got text: \n%s", text)
	}
	if !strings.Contains(text, "42") { // CPU
		t.Errorf("Expected CPU usage, got text: \n%s", text)
	}
	if !strings.Contains(text, "0G") { // SSD free space as formatted
		t.Errorf("Expected SSD free space, got text: \n%s", text)
	}
}

func TestGetHelpText(t *testing.T) {
	ctx := setupTestContext()

	text := GetHelpText(ctx)

	if !strings.Contains(text, "[help_intro]") {
		t.Errorf("Expected help intro, got: %s", text)
	}
	if !strings.Contains(text, "/docker") {
		t.Errorf("Expected docker command in help, got: %s", text)
	}
}

func TestGetPingText(t *testing.T) {
	ctx := setupTestContext()

	text := GetPingText(ctx)
	if !strings.Contains(text, "[ping_pong]") {
		t.Errorf("Expected ping_pong, got: %s", text)
	}
}
