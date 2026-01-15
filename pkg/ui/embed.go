// Package ui provides shared embedded UI assets for xgrabba.
//
// This package is used by both the main server and the standalone viewer
// to ensure UI consistency. The main index.html has built-in offline mode
// support that activates when window.OFFLINE_DATA is present.
package ui

import (
	_ "embed"
)

// IndexHTML is the main archive browser UI.
// It supports both online (API) and offline (embedded data) modes.
// Offline mode is activated by setting window.OFFLINE_DATA before the script runs.
//
//go:embed index.html
var IndexHTML []byte

// QuickHTML is the mobile-optimized quick archive page.
// This is a simpler interface for quickly archiving tweets on mobile devices.
//
//go:embed quick.html
var QuickHTML []byte

// AdminEventsHTML is the admin activity log page.
// This is only used in online server mode.
//
//go:embed admin_events.html
var AdminEventsHTML []byte
