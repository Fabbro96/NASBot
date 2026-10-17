package app

import (
	"reflect"
	"sort"
	"strings"
)

// ConfigPatchResult reports what applyConfigPatch refused to apply and what it
// had to correct on the way.
type ConfigPatchResult struct {
	// Ignored lists the patch keys that were refused: locked fields and keys
	// that Config does not declare. Callers must show these to the user, a
	// silently dropped key reads as "applied" but did nothing.
	Ignored []string
	// Corrected lists the sanitizing clamps applied to the written file.
	Corrected []string
}

// lockedConfigPaths are the keys /configset must never write. bot_token and
// allowed_user_id are credentials: they are the access control of this bot, and
// letting a chat rewrite them turns a stolen allowed_user_id into full control.
// auto_apply downloads and runs a release without asking: it is not a setting, it
// is remote code execution.
var lockedConfigPaths = map[string]struct{}{
	"bot_token":         {},
	"allowed_user_id":   {},
	"update.auto_apply": {},
	// Enabling /cmd from a chat message is execution of code, not a setting:
	// the same class as update.auto_apply. It stays an explicit edit of
	// config.json followed by a restart.
	"shell_command": {},
}

// applyConfigPatch applies a JSON patch to the config file and republishes the
// configuration.
//
// The patch is filtered against the keys Config actually declares, so an
// arbitrary path can no longer reach a field nobody intended to expose. Every
// refused key is reported back: a silently dropped key looks applied to the user
// who sent it.
func applyConfigPatch(patch map[string]interface{}) (ConfigPatchResult, error) {
	result := ConfigPatchResult{}
	if len(patch) == 0 {
		return result, nil
	}

	// Read-modify-write del file: l'intera sequenza (leggere, mergiare,
	// riscrivere, ripubblicare) sta sotto lo stesso lock, altrimenti due patch
	// partite insieme si cancellano a vicenda le chiavi.
	configWriteMu.Lock()
	defer configWriteMu.Unlock()

	path, err := resolveConfigPath()
	if err != nil {
		return result, err
	}

	configMap, err := readConfigFile(path)
	if err != nil {
		return result, err
	}

	allowed, ignored := filterPatchKeys(patch, "")
	sort.Strings(ignored)
	result.Ignored = ignored
	if len(allowed) == 0 {
		return result, nil
	}

	// Merge patch
	deepMerge(configMap, allowed)
	fillMissingConfigFields(configMap)

	updated, _, corrected, err := decodeConfigMap(configMap)
	if err != nil {
		return result, err
	}

	if err := writeConfigFile(path, updated, configMap); err != nil {
		return result, err
	}

	publishConfig(updated)

	result.Corrected = corrected
	return result, nil
}

// filterPatchKeys drops every key of patch that is not a field of Config, plus
// the locked ones, and reports their dotted paths.
func filterPatchKeys(patch map[string]interface{}, prefix string) (map[string]interface{}, []string) {
	allowed := make(map[string]interface{}, len(patch))
	ignored := make([]string, 0)

	for k, v := range patch {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}

		if _, locked := lockedConfigPaths[path]; locked {
			ignored = append(ignored, path)
			continue
		}

		kind, known := configPathKinds[path]
		if !known {
			ignored = append(ignored, path)
			continue
		}

		switch kind {
		case configPathFreeForm:
			clean, subIgnored := filterFreeFormMap(v, path)
			ignored = append(ignored, subIgnored...)
			if len(clean) > 0 {
				allowed[k] = clean
			}
		case configPathSection:
			nested, ok := v.(map[string]interface{})
			if !ok {
				// A scalar where a section belongs: refuse rather than replace the
				// whole section with a string.
				ignored = append(ignored, path)
				continue
			}
			subAllowed, subIgnored := filterPatchKeys(nested, path)
			ignored = append(ignored, subIgnored...)
			if len(subAllowed) > 0 {
				allowed[k] = subAllowed
			}
		case configPathLeaf:
			if _, isMap := v.(map[string]interface{}); isMap {
				ignored = append(ignored, path)
				continue
			}
			allowed[k] = v
		}
	}

	return allowed, ignored
}

// filterFreeFormMap checks the entries of a map keyed by data against the fields
// of its value type. The keys are mount points and stay as they are; only the
// contents of each entry are checked.
func filterFreeFormMap(v interface{}, prefix string) (map[string]interface{}, []string) {
	nested, ok := v.(map[string]interface{})
	if !ok {
		return nil, []string{prefix}
	}
	allowed := make(map[string]interface{}, len(nested))
	ignored := make([]string, 0)
	for key, entry := range nested {
		entryMap, ok := entry.(map[string]interface{})
		if !ok {
			ignored = append(ignored, prefix+"."+key)
			continue
		}
		clean := make(map[string]interface{}, len(entryMap))
		for field, value := range entryMap {
			fieldPath := prefix + "." + field
			if _, known := configPathKinds[fieldPath]; !known {
				ignored = append(ignored, fieldPath)
				continue
			}
			clean[field] = value
		}
		if len(clean) > 0 {
			allowed[key] = clean
		}
	}
	return allowed, ignored
}

// configPathKind describes what may appear under a known configuration path.
type configPathKind uint8

const (
	// configPathLeaf is a scalar or a list.
	configPathLeaf configPathKind = iota
	// configPathSection is a nested struct: its keys must be known fields.
	configPathSection
	// configPathFreeForm is a map keyed by data (a mount point), whose own keys
	// are arbitrary but whose entries are still checked.
	configPathFreeForm
)

// configPathKinds maps every dotted JSON path of Config to what it accepts. It
// is derived from the struct tags, so a new field is writable by default and a
// typo is refused, with no second list to keep in sync by hand.
var configPathKinds = buildConfigPathKinds()

func buildConfigPathKinds() map[string]configPathKind {
	kinds := make(map[string]configPathKind)
	collectConfigPaths(reflect.TypeOf(Config{}), "", kinds)
	return kinds
}

func collectConfigPaths(t reflect.Type, prefix string, out map[string]configPathKind) {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}

		switch {
		case isMapOfStruct(field.Type):
			// A map keyed by data (a mount point). The entries are not fields, but
			// what sits inside each entry is: register the value type's fields so
			// an entry cannot smuggle in an unknown key either.
			out[path] = configPathFreeForm
			collectConfigPaths(field.Type.Elem(), path, out)
		case isStructLike(field.Type):
			out[path] = configPathSection
			collectConfigPaths(field.Type, path, out)
		default:
			out[path] = configPathLeaf
		}
	}
}

func isMapOfStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Map && isStructLike(t.Elem())
}

func isStructLike(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.Kind() == reflect.Struct
}

func deepMerge(dest, src map[string]interface{}) {
	for k, v := range src {
		if vMap, ok := v.(map[string]interface{}); ok {
			if destMap, ok := dest[k].(map[string]interface{}); ok {
				deepMerge(destMap, vMap)
				continue
			}
		}
		dest[k] = v
	}
}
