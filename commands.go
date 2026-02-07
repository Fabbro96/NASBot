package main

import (
"context"
"io"
"net"
"net/http"
"strings"
"time"
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
)

var (
reportMode          int
reportMorningHour   int
reportMorningMinute int
reportEveningHour   int
reportEveningMinute int
quietHoursEnabled   bool
quietStartHour      int
quietStartMinute    int
quietEndHour        int
quietEndMinute      int
httpClient          = &http.Client{Timeout: netTimeout}
)

func cpuTempStatus(ctx *AppContext, temp float64) (icon, status string) {
	tr := ctx.Tr
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
	icon = "✅"
	status = tr("temp_disk_healthy")
	if strings.Contains(strings.ToUpper(health), "FAIL") {
		icon = "🚨"
		status = tr("temp_disk_fail")
	} else if temp > diskWarmC && temp > 0 {
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

func getPublicIP(ctx *AppContext, reqCtx context.Context) string {
	tr := ctx.Tr
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return tr("net_checking")
	}
	client := httpClient
	if ctx != nil && ctx.HTTP != nil {
		client = ctx.HTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return tr("net_checking")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tr("net_checking")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return tr("net_checking")
	}
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return tr("net_checking")
	}
	return ip
}
