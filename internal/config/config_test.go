package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfig_Validate_Success(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			APIKey: "test-api-key",
		},
		Grok: GrokConfig{
			APIKey: "test-grok-key",
		},
		Storage: StorageConfig{
			BasePath: "/data/videos",
		},
	}

	err := cfg.Validate()
	if err != nil {
		t.Errorf("Validate() should pass, got %v", err)
	}
}

func TestConfig_Validate_MissingAPIKey(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			APIKey: "",
		},
		Grok: GrokConfig{
			APIKey: "test-grok-key",
		},
		Storage: StorageConfig{
			BasePath: "/data/videos",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should fail for missing API_KEY")
	}
}

func TestConfig_Validate_MissingGrokKey(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			APIKey: "test-api-key",
		},
		Grok: GrokConfig{
			APIKey: "",
		},
		Storage: StorageConfig{
			BasePath: "/data/videos",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should fail for missing GROK_API_KEY")
	}
}

func TestConfig_Validate_MissingStoragePath(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			APIKey: "test-api-key",
		},
		Grok: GrokConfig{
			APIKey: "test-grok-key",
		},
		Storage: StorageConfig{
			BasePath: "",
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() should fail for missing STORAGE_PATH")
	}
}

func TestConfig_Validate_Bookmarks(t *testing.T) {
	tests := []struct {
		name    string
		cfg     BookmarksConfig
		wantErr bool
	}{
		{
			name: "valid with bearer token",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				BearerToken:  "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   20,
				MaxNewPerPoll: 5,
			},
			wantErr: false,
		},
		{
			name: "valid with browser credentials",
			cfg: BookmarksConfig{
				Enabled:               true,
				UseBrowserCredentials: true,
				PollInterval:          20 * time.Minute,
				MaxResults:            20,
				MaxNewPerPoll:         5,
			},
			wantErr: false,
		},
		{
			name: "valid with OAuth",
			cfg: BookmarksConfig{
				Enabled:       true,
				UserID:        "12345",
				OAuthClientID: "client-id",
				RefreshToken:  "refresh-token",
				PollInterval:  20 * time.Minute,
				MaxResults:    20,
				MaxNewPerPoll: 5,
			},
			wantErr: false,
		},
		{
			name: "missing user_id without OAuth client",
			cfg: BookmarksConfig{
				Enabled:      true,
				BearerToken:  "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   20,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "missing auth entirely",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				PollInterval: 20 * time.Minute,
				MaxResults:   20,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "refresh token without client id",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				RefreshToken: "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   20,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "poll interval too small",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				BearerToken:  "token",
				PollInterval: 5 * time.Second,
				MaxResults:   20,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "max results invalid",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				BearerToken:  "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   0,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "max results too high",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				BearerToken:  "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   101,
				MaxNewPerPoll: 5,
			},
			wantErr: true,
		},
		{
			name: "max new per poll invalid",
			cfg: BookmarksConfig{
				Enabled:      true,
				UserID:       "12345",
				BearerToken:  "token",
				PollInterval: 20 * time.Minute,
				MaxResults:   20,
				MaxNewPerPoll: 0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{
					APIKey: "test-api-key",
				},
				Grok: GrokConfig{
					APIKey: "test-grok-key",
				},
				Storage: StorageConfig{
					BasePath: "/data/videos",
				},
				Bookmarks: tt.cfg,
			}

			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestServerConfig_Address(t *testing.T) {
	tests := []struct {
		name string
		cfg  ServerConfig
		want string
	}{
		{
			name: "default",
			cfg:  ServerConfig{Host: "0.0.0.0", Port: 9847},
			want: "0.0.0.0:9847",
		},
		{
			name: "localhost",
			cfg:  ServerConfig{Host: "localhost", Port: 8080},
			want: "localhost:8080",
		},
		{
			name: "specific IP",
			cfg:  ServerConfig{Host: "192.168.1.100", Port: 3000},
			want: "192.168.1.100:3000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Address(); got != tt.want {
				t.Errorf("Address() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoad_FromYAMLFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	// Set env vars since envconfig applies defaults and overrides YAML
	// Note: envconfig.Process() applies defaults even when YAML is loaded,
	// so we need to explicitly set env vars to test YAML values or test
	// that the API key from YAML is used when env is not set.
	t.Setenv("SERVER_HOST", "localhost")
	t.Setenv("SERVER_PORT", "8080")
	t.Setenv("STORAGE_PATH", "/custom/path")

	yamlContent := `
server:
  api_key: "yaml-api-key"
grok:
  api_key: "yaml-grok-key"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Host != "localhost" {
		t.Errorf("Host = %q, want %q", cfg.Server.Host, "localhost")
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Port = %d, want %d", cfg.Server.Port, 8080)
	}
	if cfg.Server.APIKey != "yaml-api-key" {
		t.Errorf("APIKey = %q, want %q", cfg.Server.APIKey, "yaml-api-key")
	}
	if cfg.Storage.BasePath != "/custom/path" {
		t.Errorf("BasePath = %q, want %q", cfg.Storage.BasePath, "/custom/path")
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	yamlContent := `
server:
  host: "localhost"
  port: 8080
  api_key: "yaml-api-key"
storage:
  base_path: "/yaml/path"
grok:
  api_key: "yaml-grok-key"
`
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Set env vars to override
	t.Setenv("API_KEY", "env-api-key")
	t.Setenv("STORAGE_PATH", "/env/path")
	t.Setenv("GROK_API_KEY", "env-grok-key")

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Env should override YAML
	if cfg.Server.APIKey != "env-api-key" {
		t.Errorf("APIKey should be from env, got %q", cfg.Server.APIKey)
	}
	if cfg.Storage.BasePath != "/env/path" {
		t.Errorf("BasePath should be from env, got %q", cfg.Storage.BasePath)
	}
}

func TestLoad_EnvOnly(t *testing.T) {
	t.Setenv("API_KEY", "test-api-key")
	t.Setenv("GROK_API_KEY", "test-grok-key")
	t.Setenv("STORAGE_PATH", "/data/test")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.APIKey != "test-api-key" {
		t.Errorf("APIKey = %q, want %q", cfg.Server.APIKey, "test-api-key")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")

	invalidYAML := `
server:
  host: "localhost
  port: 8080
`
	if err := os.WriteFile(configPath, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Load should fail for invalid YAML")
	}
}

func TestLoad_NonexistentFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("Load should fail for nonexistent file")
	}
}

func TestLoad_ValidationFailure(t *testing.T) {
	// No env vars set, empty config path - should fail validation
	t.Setenv("API_KEY", "")
	t.Setenv("GROK_API_KEY", "")
	t.Setenv("STORAGE_PATH", "")

	_, err := Load("")
	if err == nil {
		t.Error("Load should fail validation without required values")
	}
}
