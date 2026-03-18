package app

import (
	"fmt"
	"strings"
)

func powerSourceFromReason(reason string) string {
	low := strings.ToLower(strings.TrimSpace(reason))
	switch {
	case strings.HasPrefix(low, "manual-force-command"):
		return "command"
	case strings.HasPrefix(low, "manual-force-button"):
		return "button"
	case strings.HasPrefix(low, "network-"):
		return "watchdog"
	case strings.HasPrefix(low, "oom-"):
		return "watchdog"
	default:
		return "system"
	}
}

func addPowerLifecycleEvent(ctx *AppContext, action string, forced bool, source, cmd, reason string) {
	if ctx == nil || ctx.State == nil {
		return
	}
	forcedLabel := "no"
	if forced {
		forcedLabel = "yes"
	}
	msg := fmt.Sprintf("Power event: action=%s forced=%s source=%s cmd=\"%s\" reason=%s", action, forcedLabel, source, cmd, reason)
	ctx.State.AddEvent("action", msg)
}
