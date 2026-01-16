// XGrabba TUI - Terminal User Interface for XGrabba management
// A comprehensive terminal application for monitoring, managing, and troubleshooting
// XGrabba deployments in Kubernetes environments.
package main

import (
	"fmt"
	"os"

	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/config"
	"github.com/iconidentify/xgrabba/cmd/xgrabba-tui/internal/ui"
)

func main() {
	cfg := config.Load()

	app, err := ui.NewApp(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing TUI: %v\n", err)
		os.Exit(1)
	}

	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
