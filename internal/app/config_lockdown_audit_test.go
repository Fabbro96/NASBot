package app

// Deep-audit regression tests: /configset must refuse every spelling of the
// locked keys. The basic case is covered by TestApplyConfigPatch_SanitizesAnd
// IgnoresLocked; these are the bypass shapes (case variants, trailing spaces,
// nested maps, wrong types) a stolen chat session would try.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestApplyConfigPatch_RefusesLockedKeyBypassShapes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	originalConfigFile := configFile
	configFile = path
	defer func() {
		configFile = originalConfigFile
	}()

	seed := `{"bot_token": "real-token", "allowed_user_id": 1}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	patches := []map[string]interface{}{
		{"bot_token": "ATTACKER"},
		{"Bot_Token": "ATTACKER"},
		{"BOT_TOKEN": "ATTACKER"},
		{"bot_token ": "ATTACKER"},
		{"allowed_user_id": float64(999)},
		{"Allowed_User_ID": float64(999)},
		{"update": map[string]interface{}{"auto_apply": true}},
		{"Update": map[string]interface{}{"Auto_Apply": true}},
		{"shell_command": map[string]interface{}{"enabled": true}},
		{"shell_command": map[string]interface{}{"allowed_binaries": []interface{}{"reboot"}}},
		{"bot_token": map[string]interface{}{"x": 1}},
		{"update": "garbage"},
		{"shell_command": "garbage"},
	}
	for i, p := range patches {
		res, err := applyConfigPatch(p)
		if err != nil {
			t.Fatalf("patch %d: unexpected error: %v", i, err)
		}
		if len(res.Ignored) == 0 {
			t.Errorf("patch %d (%v): nothing refused, bypass?", i, p)
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var saved struct {
		BotToken      string `json:"bot_token"`
		AllowedUserID int64  `json:"allowed_user_id"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if saved.BotToken != "real-token" {
		t.Fatalf("bot_token was overwritten: %q", saved.BotToken)
	}
	if saved.AllowedUserID != 1 {
		t.Fatalf("allowed_user_id was overwritten: %d", saved.AllowedUserID)
	}
}
