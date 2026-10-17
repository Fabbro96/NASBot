package app

import (
	"hash/fnv"
	"strconv"
	"sync"
)

// callbackRefs maps a mount path to a short token that fits Telegram's inline
// callback data limit.
//
// Inline callback data is capped at 64 bytes by Telegram. Encoding the mount
// inline and cutting it to fit was wrong three times over: the cut can land in
// the middle of a UTF-8 rune, which makes Telegram reject the whole keyboard;
// two mounts sharing the first bytes collide onto the same entry, so the buttons
// write to whichever one the config map happened to resolve; and the rebuilt
// buttons create a second, truncated entry in config.json.
var callbackRefs = struct {
	mu   sync.RWMutex
	refs map[string]string
}{refs: map[string]string{}}

func callbackRefFor(mount string) string {
	callbackRefs.mu.RLock()
	ref, ok := callbackRefs.refs[mount]
	callbackRefs.mu.RUnlock()
	if ok {
		return ref
	}

	callbackRefs.mu.Lock()
	defer callbackRefs.mu.Unlock()
	if ref, ok := callbackRefs.refs[mount]; ok {
		return ref // registered while the lock was released
	}

	// A hash keeps the token stable across restarts, but 20 bits can collide.
	// On a collision with another mount, step to a free token so two buttons
	// never address the same mount.
	base := mountCallbackRef(mount)
	ref = base
	for n := 2; callbackRefTaken(ref); n++ {
		ref = base + "x" + strconv.Itoa(n)
	}
	callbackRefs.refs[mount] = ref
	return ref
}

// callbackRefTaken reports whether ref is already assigned. The caller holds
// callbackRefs.mu.
func callbackRefTaken(ref string) bool {
	for _, known := range callbackRefs.refs {
		if known == ref {
			return true
		}
	}
	return false
}

// mountCallbackRef builds the token for mount from a hash of the path. A hash is
// used instead of a plain index so that adding a disk does not renumber the
// callbacks of the others, which would break keyboards already on screen.
func mountCallbackRef(mount string) string {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(mount))
	return "d" + strconv.FormatUint(uint64(sum.Sum32()%1_000_000), 36)
}

// resolveCallbackRef maps a token or a raw mount path back to the mount path.
// Tokens are tried first; anything else is treated as a literal path so that a
// keyboard built before the upgrade keeps working.
func resolveCallbackRef(ref string) string {
	if mount := lookupCallbackMounts(ref); mount != "" {
		return mount
	}
	return ref
}

// lookupCallbackMounts returns the mount registered under ref, or "".
func lookupCallbackMounts(ref string) string {
	callbackRefs.mu.RLock()
	defer callbackRefs.mu.RUnlock()
	for mount, known := range callbackRefs.refs {
		if known == ref {
			return mount
		}
	}
	return ""
}

// registerCallbackMounts records the mount -> token pairs of the mounts on
// screen, so the tokens resolve when a button is pressed.
func registerCallbackMounts(mounts []string) {
	for _, mount := range mounts {
		callbackRefFor(mount)
	}
}

// callbackDataFits reports whether payload is within Telegram's limit.
func callbackDataFits(payload string) bool {
	return len(payload) <= telegramCallbackDataLimit
}

// telegramCallbackDataLimit is the hard cap Telegram puts on inline callback
// data.
const telegramCallbackDataLimit = 64
