package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	// Save and clear environment
	origNamespace := os.Getenv("XGRABBA_NAMESPACE")
	origRelease := os.Getenv("XGRABBA_RELEASE")
	defer func() {
		os.Setenv("XGRABBA_NAMESPACE", origNamespace)
		os.Setenv("XGRABBA_RELEASE", origRelease)
	}()
	os.Unsetenv("XGRABBA_NAMESPACE")
	os.Unsetenv("XGRABBA_RELEASE")

	cfg := Load()

	// Check defaults
	if cfg.Namespace != "xgrabba" {
		t.Errorf("expected default namespace 'xgrabba', got '%s'", cfg.Namespace)
	}
	if cfg.ReleaseName != "xgrabba" {
		t.Errorf("expected default release 'xgrabba', got '%s'", cfg.ReleaseName)
	}
}

func TestLoadWithEnvOverrides(t *testing.T) {
	// Save and set environment
	origNamespace := os.Getenv("XGRABBA_NAMESPACE")
	origRelease := os.Getenv("XGRABBA_RELEASE")
	defer func() {
		os.Setenv("XGRABBA_NAMESPACE", origNamespace)
		os.Setenv("XGRABBA_RELEASE", origRelease)
	}()

	os.Setenv("XGRABBA_NAMESPACE", "custom-ns")
	os.Setenv("XGRABBA_RELEASE", "custom-release")

	cfg := Load()

	if cfg.Namespace != "custom-ns" {
		t.Errorf("expected namespace 'custom-ns', got '%s'", cfg.Namespace)
	}
	if cfg.ReleaseName != "custom-release" {
		t.Errorf("expected release 'custom-release', got '%s'", cfg.ReleaseName)
	}
}

func TestLoadHelmRepo(t *testing.T) {
	origRepo := os.Getenv("XGRABBA_HELM_REPO")
	defer os.Setenv("XGRABBA_HELM_REPO", origRepo)
	os.Unsetenv("XGRABBA_HELM_REPO")

	cfg := Load()

	if cfg.HelmRepo != "oci://ghcr.io/iconidentify/charts/xgrabba" {
		t.Errorf("unexpected default helm repo: %s", cfg.HelmRepo)
	}
}

func TestLoadCustomHelmRepo(t *testing.T) {
	origRepo := os.Getenv("XGRABBA_HELM_REPO")
	defer os.Setenv("XGRABBA_HELM_REPO", origRepo)

	os.Setenv("XGRABBA_HELM_REPO", "oci://custom.registry/charts/xgrabba")

	cfg := Load()

	if cfg.HelmRepo != "oci://custom.registry/charts/xgrabba" {
		t.Errorf("expected custom helm repo, got '%s'", cfg.HelmRepo)
	}
}

func TestLoadStatusRefresh(t *testing.T) {
	origRefresh := os.Getenv("XGRABBA_STATUS_REFRESH")
	defer os.Setenv("XGRABBA_STATUS_REFRESH", origRefresh)
	os.Unsetenv("XGRABBA_STATUS_REFRESH")

	cfg := Load()

	if cfg.StatusRefresh != 5*time.Second {
		t.Errorf("expected default status refresh 5s, got %v", cfg.StatusRefresh)
	}
}

func TestLoadCustomStatusRefresh(t *testing.T) {
	origRefresh := os.Getenv("XGRABBA_STATUS_REFRESH")
	defer os.Setenv("XGRABBA_STATUS_REFRESH", origRefresh)

	os.Setenv("XGRABBA_STATUS_REFRESH", "10s")

	cfg := Load()

	if cfg.StatusRefresh != 10*time.Second {
		t.Errorf("expected status refresh 10s, got %v", cfg.StatusRefresh)
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	origRefresh := os.Getenv("XGRABBA_STATUS_REFRESH")
	defer os.Setenv("XGRABBA_STATUS_REFRESH", origRefresh)

	os.Setenv("XGRABBA_STATUS_REFRESH", "invalid")

	cfg := Load()

	// Should fallback to default
	if cfg.StatusRefresh != 5*time.Second {
		t.Errorf("expected default on invalid duration, got %v", cfg.StatusRefresh)
	}
}

func TestLoadLogRefresh(t *testing.T) {
	origRefresh := os.Getenv("XGRABBA_LOG_REFRESH")
	defer os.Setenv("XGRABBA_LOG_REFRESH", origRefresh)
	os.Unsetenv("XGRABBA_LOG_REFRESH")

	cfg := Load()

	if cfg.LogRefresh != 1*time.Second {
		t.Errorf("expected default log refresh 1s, got %v", cfg.LogRefresh)
	}
}

func TestLoadColorScheme(t *testing.T) {
	origScheme := os.Getenv("XGRABBA_COLOR_SCHEME")
	defer os.Setenv("XGRABBA_COLOR_SCHEME", origScheme)
	os.Unsetenv("XGRABBA_COLOR_SCHEME")

	cfg := Load()

	if cfg.ColorScheme != "dark" {
		t.Errorf("expected default color scheme 'dark', got '%s'", cfg.ColorScheme)
	}
}

func TestLoadKubeContext(t *testing.T) {
	origContext := os.Getenv("XGRABBA_KUBE_CONTEXT")
	defer os.Setenv("XGRABBA_KUBE_CONTEXT", origContext)

	os.Setenv("XGRABBA_KUBE_CONTEXT", "my-cluster-context")

	cfg := Load()

	if cfg.KubeContext != "my-cluster-context" {
		t.Errorf("expected kube context 'my-cluster-context', got '%s'", cfg.KubeContext)
	}
}

func TestGetEnv(t *testing.T) {
	os.Setenv("TEST_KEY", "test_value")
	defer os.Unsetenv("TEST_KEY")

	result := getEnv("TEST_KEY", "default")
	if result != "test_value" {
		t.Errorf("expected 'test_value', got '%s'", result)
	}
}

func TestGetEnvDefault(t *testing.T) {
	os.Unsetenv("NONEXISTENT_KEY")

	result := getEnv("NONEXISTENT_KEY", "default_value")
	if result != "default_value" {
		t.Errorf("expected 'default_value', got '%s'", result)
	}
}

func TestGetDuration(t *testing.T) {
	os.Setenv("TEST_DURATION", "30s")
	defer os.Unsetenv("TEST_DURATION")

	result := getDuration("TEST_DURATION", 10*time.Second)
	if result != 30*time.Second {
		t.Errorf("expected 30s, got %v", result)
	}
}

func TestGetDurationDefault(t *testing.T) {
	os.Unsetenv("NONEXISTENT_DURATION")

	result := getDuration("NONEXISTENT_DURATION", 15*time.Second)
	if result != 15*time.Second {
		t.Errorf("expected 15s, got %v", result)
	}
}

func TestGetDurationInvalid(t *testing.T) {
	os.Setenv("INVALID_DURATION", "not-a-duration")
	defer os.Unsetenv("INVALID_DURATION")

	result := getDuration("INVALID_DURATION", 20*time.Second)
	if result != 20*time.Second {
		t.Errorf("expected default 20s for invalid, got %v", result)
	}
}

func TestConfigFields(t *testing.T) {
	cfg := &Config{
		Namespace:     "test-ns",
		ReleaseName:   "test-release",
		KubeContext:   "test-context",
		HelmRepo:      "oci://test/repo",
		SSHUser:       "testuser",
		SSHKeyPath:    "/path/to/key",
		StatusRefresh: 10 * time.Second,
		LogRefresh:    2 * time.Second,
		ColorScheme:   "light",
	}

	if cfg.Namespace != "test-ns" {
		t.Errorf("unexpected namespace: %s", cfg.Namespace)
	}
	if cfg.ReleaseName != "test-release" {
		t.Errorf("unexpected release: %s", cfg.ReleaseName)
	}
	if cfg.KubeContext != "test-context" {
		t.Errorf("unexpected context: %s", cfg.KubeContext)
	}
	if cfg.HelmRepo != "oci://test/repo" {
		t.Errorf("unexpected helm repo: %s", cfg.HelmRepo)
	}
	if cfg.SSHUser != "testuser" {
		t.Errorf("unexpected ssh user: %s", cfg.SSHUser)
	}
	if cfg.SSHKeyPath != "/path/to/key" {
		t.Errorf("unexpected ssh key path: %s", cfg.SSHKeyPath)
	}
	if cfg.StatusRefresh != 10*time.Second {
		t.Errorf("unexpected status refresh: %v", cfg.StatusRefresh)
	}
	if cfg.LogRefresh != 2*time.Second {
		t.Errorf("unexpected log refresh: %v", cfg.LogRefresh)
	}
	if cfg.ColorScheme != "light" {
		t.Errorf("unexpected color scheme: %s", cfg.ColorScheme)
	}
}
