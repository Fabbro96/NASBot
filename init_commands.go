package main

func SetupCommandRegistry() *CommandRegistry {
	r := NewCommandRegistry()

	// System
	r.Register("status", &StatusCmd{})
	r.Register("start", &StatusCmd{}) // Alias
	r.Register("top", &TopCmd{})
	r.Register("sysinfo", &SysInfoCmd{})
	r.Register("temp", &TempCmd{})
	r.Register("reboot", &PowerCmd{Action: "reboot"})
	r.Register("forcereboot", &PowerCmd{Action: "reboot"})
	r.Register("shutdown", &PowerCmd{Action: "shutdown"})

	// Docker
	r.Register("docker", &DockerMenuCmd{})
	r.Register("dstats", &DockerStatsCmd{})
	r.Register("container", &ContainerCmd{})
	r.Register("restartdocker", &RestartDockerCmd{})
	r.Register("kill", &KillCmd{})

	// Network
	r.Register("net", &NetCmd{})
	r.Register("speedtest", &SpeedtestCmd{})

	// Tools
	r.Register("ping", &PingCmd{})
	r.Register("logs", &LogsCmd{})
	r.Register("logsearch", &LogSearchCmd{})
	r.Register("ask", &AskCmd{})
	r.Register("help", &HelpCmd{})
	r.Register("quick", &QuickCmd{})
	r.Register("q", &QuickCmd{}) // Alias
	r.Register("diskpred", &DiskPredCmd{})
	r.Register("prediction", &DiskPredCmd{}) // Alias
	r.Register("health", &HealthCmd{})
	r.Register("healthchecks", &HealthCmd{}) // Alias

	// Report
	r.Register("report", &ReportCmd{})

	// Settings
	r.Register("config", &ConfigCmd{})
	r.Register("configjson", &ConfigJSONCmd{})
	r.Register("configset", &ConfigSetCmd{})
	r.Register("language", &LanguageCmd{})
	r.Register("settings", &SettingsCmd{})

	return r
}
