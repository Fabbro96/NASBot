package app

import (
	"nasbot/pkg/model"
	"testing"
)

func TestGetMainKeyboard(t *testing.T) {
	ctx := &AppContext{
		Settings: &model.UserSettings{Language: "en"},
	}
	kb := getMainKeyboard(ctx)
	if len(kb.InlineKeyboard) != 3 {
		t.Errorf("expected 3 rows")
	}
	// Row 1: refresh, temp, net
	if len(kb.InlineKeyboard[0]) != 3 {
		t.Errorf("expected 3 buttons in row 1")
	}
}

func TestGetPowerMenuText(t *testing.T) {
	ctx := &AppContext{
		Settings: &model.UserSettings{Language: "en"},
	}
	text, kb := getPowerMenuText(ctx)
	if text == "" {
		t.Errorf("expected non-empty text")
	}
	if len(kb.InlineKeyboard) != 3 {
		t.Errorf("expected 3 rows in power menu")
	}
}
