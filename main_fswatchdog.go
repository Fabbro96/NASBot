//go:build fswatchdog

package main

import "nasbot/internal/app"

func main() {
	app.RunFSWatchdogService()
}
