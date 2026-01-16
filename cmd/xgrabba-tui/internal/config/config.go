// Package config provides configuration management for the XGrabba TUI.
package config

import (
	"os"
	"time"
)

// Config holds the TUI configuration.
type Config struct {
	// Kubernetes configuration
	Namespace   string
	ReleaseName string
	KubeContext string

	// Helm configuration
	HelmRepo string

	// SSH configuration
	SSHUser    string
	SSHKeyPath string

	// Refresh intervals
	StatusRefresh time.Duration
	LogRefresh    time.Duration

	// UI preferences
	ColorScheme string
}

// Load returns configuration from environment variables with sensible defaults.
func Load() *Config {
	return &Config{
		Namespace:     getEnv("XGRABBA_NAMESPACE", "xgrabba"),
		ReleaseName:   getEnv("XGRABBA_RELEASE", "xgrabba"),
		KubeContext:   getEnv("XGRABBA_KUBE_CONTEXT", ""),
		HelmRepo:      getEnv("XGRABBA_HELM_REPO", "oci://ghcr.io/iconidentify/charts/xgrabba"),
		SSHUser:       getEnv("XGRABBA_SSH_USER", os.Getenv("USER")),
		SSHKeyPath:    getEnv("XGRABBA_SSH_KEY", os.ExpandEnv("$HOME/.ssh/id_rsa")),
		StatusRefresh: getDuration("XGRABBA_STATUS_REFRESH", 5*time.Second),
		LogRefresh:    getDuration("XGRABBA_LOG_REFRESH", 1*time.Second),
		ColorScheme:   getEnv("XGRABBA_COLOR_SCHEME", "dark"),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getDuration(key string, defaultVal time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}
