// Copyright (C) 2026 Techdelight BV

package main

import (
	"fmt"

	"github.com/techdelight/daedalus/core"
	"github.com/techdelight/daedalus/internal/color"
)

// manageForeman handles the "foreman" subcommand.
func manageForeman(cfg *core.Config) error {
	args := cfg.ForemanArgs
	if len(args) == 0 {
		return fmt.Errorf("usage: daedalus foreman <start|stop|status>\n%s available: start, stop, status", color.Cyan("Hint:"))
	}
	switch args[0] {
	case "status":
		return foremanStatus(cfg)
	case "start":
		return foremanStart(cfg)
	case "stop":
		fmt.Println("Foreman stop requires a running web server. Use the Web UI or API.")
		return nil
	default:
		return fmt.Errorf("unknown foreman command %q\n%s available: start, stop, status", args[0], color.Cyan("Hint:"))
	}
}

func foremanStatus(cfg *core.Config) error {
	fmt.Println("Foreman status is available via the Web UI at /api/foreman/status")
	fmt.Println("The Foreman runs as a background process inside 'daedalus web'.")
	return nil
}

func foremanStart(cfg *core.Config) error {
	fmt.Println("The Foreman runs inside 'daedalus web'. Start the web server to use the Foreman:")
	fmt.Println("  daedalus web")
	fmt.Println()
	fmt.Println("Then manage the Foreman via the Web UI or API:")
	fmt.Println("  POST /api/foreman/start    — start the Foreman for a programme")
	fmt.Println("  POST /api/foreman/stop     — stop the Foreman")
	fmt.Println("  GET  /api/foreman/status   — current state and plan")
	return nil
}
