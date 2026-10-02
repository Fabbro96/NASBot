package main

import "nasbot/internal/app"

// Version is injected at build time via -ldflags.
var Version = "dev"

func main() {
	app.Version = Version
	app.RunBot()
}
