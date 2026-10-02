package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"nasbot/internal/format"
)

const (
	publicIPURL     = "https://api.ipify.org"
	hostIPTimeout   = 2 * time.Second
	netTimeout      = 5 * time.Second
	logCmdTimeout   = 5 * time.Second
	psTimeout       = 4 * time.Second
	maxLogLines     = 120
	maxLogChars     = 3500
	maxTopProcesses = 8
	maxProcNameLen  = 16
	cpuWarmC        = 75.0
	cpuHotC         = 85.0
	diskWarmC       = 45

	// Process manager menu (cmd_processes.go) uses its own limits.
	procMenuMaxCount    = 10
	procMenuMaxNameLen  = 12
	maxCmdOutputChars   = 4000
	quickMountNameMax   = 5
	swapVisiblePct      = 5.0
	diskIOVisiblePct    = 10.0
	defaultHealthWarnPc = 90.0
	defaultHealthCritPc = 95.0
	// maxLogLineChars is the per-line cap of /logsearch matches.
	maxLogLineChars = 100

	// defaultGeminiModel is the model shown while a Gemini call is running.
	// The fallback chain lives in internal/app/reports_ai.go; there is no
	// gemini_model field in config.json yet.
	defaultGeminiModel = "gemini-3.1-flash-lite"
)

// iconUnknown marks a reading that does not exist (sensor missing, smartctl not
// in sudoers, disk without SMART support). It must never be a green tick.
const iconUnknown = "❔"

var (
	httpClient = &http.Client{Timeout: netTimeout}
)

func cpuTempStatus(ctx *AppContext, temp float64) (icon, status string) {
	tr := ctx.Tr
	if temp <= 0 {
		return iconUnknown, tr("temp_status_unknown")
	}
	icon = "✅"
	status = tr("temp_status_good")
	if temp > cpuWarmC {
		icon = "🟡"
		status = tr("temp_status_warm")
	}
	if temp > cpuHotC {
		icon = "🔥"
		status = tr("temp_status_hot")
	}
	return
}

func diskTempStatus(ctx *AppContext, temp int, health string) (icon, status string) {
	tr := ctx.Tr
	// readDiskSMART returns (-1, "UNKNOWN") when smartctl is missing from
	// sudoers or the disk has no SMART support: that is "no data", not healthy.
	if temp < 0 || strings.EqualFold(strings.TrimSpace(health), "UNKNOWN") {
		return iconUnknown, tr("temp_status_unknown")
	}
	icon = "✅"
	status = tr("temp_disk_healthy")
	if strings.Contains(strings.ToUpper(health), "FAIL") {
		icon = "🚨"
		status = tr("temp_disk_fail")
	} else if temp > diskWarmC {
		icon = "🟡"
		status = tr("temp_disk_warm")
	}
	return
}

func getLocalIP(ctx context.Context) string {
	out, err := runCommandOutput(ctx, "hostname", "-I")
	if err != nil {
		if ip := getLocalIPFromInterfaces(); ip != "" {
			return ip
		}
		return "n/a"
	}
	ips := strings.Fields(string(out))
	if len(ips) == 0 {
		if ip := getLocalIPFromInterfaces(); ip != "" {
			return ip
		}
		return "n/a"
	}
	return ips[0]
}

func getLocalIPFromInterfaces() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP == nil {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() {
			continue
		}
		if ipv4 := ip.To4(); ipv4 != nil {
			return ipv4.String()
		}
	}
	return ""
}

// getPublicIP returns the public IP address of the NAS, or a translated
// "unavailable" marker carrying the reason.
//
// The lookup is synchronous, so there is no "still checking" state to report:
// every failure (request error, non-2xx status, unreadable body, body that is
// not an IP) is reported as an error instead of being disguised as a result.
func getPublicIP(ctx *AppContext, reqCtx context.Context) string {
	ip, err := fetchPublicIP(ctx, reqCtx)
	if err != nil {
		return trf(ctx.Tr, "net_public_unavailable", err)
	}
	return ip
}

func fetchPublicIP(ctx *AppContext, reqCtx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return "", err
	}
	client := httpClient
	if ctx != nil && ctx.HTTP != nil {
		client = ctx.HTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("unexpected body %q", ip)
	}
	return ip, nil
}

// runeSlice returns the first max runes of s. Byte slicing would split
// multi-byte runes and produce the U+FFFD replacement character.
func runeSlice(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// runeTail returns the last max runes of s. Used where the newest data must be
// preserved (log tails); format.Truncate would drop the head instead.
func runeTail(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[len(r)-max:])
}

// sortedMounts returns the given mount points ordered by their display name
// (mountShortName), ties broken by the full path. Go map iteration is random,
// so without this the disks change order between two messages.
func sortedMounts(mounts []string) []string {
	out := append([]string(nil), mounts...)
	sort.Slice(out, func(i, j int) bool {
		si, sj := mountShortName(out[i]), mountShortName(out[j])
		if si != sj {
			return si < sj
		}
		return out[i] < out[j]
	})
	return out
}

// volumeMounts returns the mount points of vols in display order.
func volumeMounts(vols map[string]VolumeStats) []string {
	mounts := make([]string, 0, len(vols))
	for m := range vols {
		mounts = append(mounts, m)
	}
	return sortedMounts(mounts)
}

// configMounts returns the mount points of a notification config map in
// display order.
func configMounts(cfgs map[string]ResourceConfig) []string {
	mounts := make([]string, 0, len(cfgs))
	for m := range cfgs {
		mounts = append(mounts, m)
	}
	return sortedMounts(mounts)
}

// formatTotalMB formats a cumulative megabyte counter through internal/format.
func formatTotalMB(mb float64) string {
	if mb <= 0 {
		mb = 0
	}
	return format.FormatRAM(uint64(mb))
}
