package app

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

// readConfigFile reads and parses the config file, falling back to the .bak copy
// written before the last rewrite if the main file is not valid JSON.
func readConfigFile(path string) (map[string]interface{}, error) {
	fileContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var configMap map[string]interface{}
	if err := json.Unmarshal(fileContent, &configMap); err != nil {
		restored, restoreErr := restoreFromBackup(path)
		if restoreErr != nil {
			return nil, fmt.Errorf("%w (no usable backup: %v)", err, restoreErr)
		}
		return restored, nil
	}
	return configMap, nil
}

// restoreFromBackup replaces a corrupt config file with its backup and returns
// the parsed backup. Without this a crash during a rewrite left bot_token
// truncated, Unmarshal failed and the bot exited in a restart loop with no way
// out but editing the file by hand.
func restoreFromBackup(path string) (map[string]interface{}, error) {
	bakPath := path + ".bak"
	bakContent, err := os.ReadFile(bakPath)
	if err != nil {
		return nil, err
	}
	var restored map[string]interface{}
	if err := json.Unmarshal(bakContent, &restored); err != nil {
		return nil, err
	}
	slog.Error("Config file was not valid JSON, restored from backup",
		"file", path, "backup", bakPath, "restored_keys", len(restored))
	return restored, nil
}

// hardenConfigPermissions tightens an existing config file to 0600.
//
// os.WriteFile only applies its mode when it creates the file, so a config.json
// created by `cp config.example.json config.json` kept whatever mode cp gave it
// and bot_token stayed world-readable for the whole life of the install.
func hardenConfigPermissions(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.Mode().Perm() == configFileMode {
		return
	}
	if err := os.Chmod(path, configFileMode); err != nil {
		slog.Warn("Failed to restrict config file permissions", "file", path, "err", err)
		return
	}
	slog.Warn("Restricted config file permissions", "file", path, "from", info.Mode().Perm().String(), "to", configFileMode.String())
}

// writeConfigFile persists c, preserving the keys of the original file that
// Config does not declare.
//
// The write goes to a temporary file in the same directory, is fsynced, and is
// then renamed over the target, so an interrupted write can never truncate
// bot_token. The previous content is kept as <path>.bak. The final chmod is
// explicit because Rename carries the temporary file's mode, not the target's.
func writeConfigFile(path string, c *Config, original map[string]interface{}) error {
	typedContent, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	// Merge the unrecognized keys of the file back in before writing.
	var merged map[string]interface{}
	if err := json.Unmarshal(typedContent, &merged); err != nil {
		return err
	}
	mergeUnknownKeys(merged, original)
	finalContent, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return err
	}

	if err := backupConfigFile(path); err != nil {
		slog.Warn("Failed to back up config file", "file", path, "err", err)
	}
	return writeFileAtomic(path, finalContent, configFileMode)
}

// backupConfigFile copies the current config file to <path>.bak, unless the
// current file is not valid JSON.
func backupConfigFile(path string) error {
	current, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// A corrupt file must never replace a good backup: the backup is the only
	// copy readConfigFile can still restore from.
	if !json.Valid(current) {
		slog.Warn("Config file is not valid JSON, keeping the existing backup", "file", path)
		return nil
	}
	return writeFileAtomic(path+".bak", current, configFileMode)
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory, then renames it into place.
func writeFileAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nasbot-config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	// Remove the temporary file on any failure: a rename would have moved it
	// away already, and a leftover .tmp in the config directory is litter that
	// the next start-up would ignore.
	defer func() {
		if tmp != nil {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// fsync before rename: rename is atomic, the content it points at is not
	// durable until the file's own data reaches the disk.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	tmp = nil

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// mergeUnknownKeys copies the keys of src that dest does not declare into dest,
// recursing into sections both sides know about. Keys of unknown sections are
// copied whole.
//
// Marshalling Config alone drops every custom key, so the first rewrite after
// an upgrade silently deleted whatever the user had added by hand.
func mergeUnknownKeys(dest, src map[string]interface{}) {
	for k, v := range src {
		if existing, ok := dest[k]; ok {
			destMap, destIsMap := existing.(map[string]interface{})
			srcMap, srcIsMap := v.(map[string]interface{})
			if destIsMap && srcIsMap {
				mergeUnknownKeys(destMap, srcMap)
			}
			continue
		}
		dest[k] = v
	}
}
