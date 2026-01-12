package config

import (
	"fmt"
	"os"
	"time"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

// Config holds all application configuration.
type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Storage   StorageConfig   `yaml:"storage"`
	Worker    WorkerConfig    `yaml:"worker"`
	Grok      GrokConfig      `yaml:"grok"`
	Whisper   WhisperConfig   `yaml:"whisper"`
	Download  DownloadConfig  `yaml:"download"`
	AI        AIConfig        `yaml:"ai"`
	Bookmarks BookmarksConfig `yaml:"bookmarks"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Host         string        `yaml:"host" envconfig:"SERVER_HOST" default:"0.0.0.0"`
	Port         int           `yaml:"port" envconfig:"SERVER_PORT" default:"9847"`
	APIKey       string        `yaml:"api_key" envconfig:"API_KEY"`
	ReadTimeout  time.Duration `yaml:"read_timeout" envconfig:"SERVER_READ_TIMEOUT" default:"30s"`
	WriteTimeout time.Duration `yaml:"write_timeout" envconfig:"SERVER_WRITE_TIMEOUT" default:"5m"`
}

// StorageConfig holds filesystem storage configuration.
type StorageConfig struct {
	BasePath    string `yaml:"base_path" envconfig:"STORAGE_PATH" default:"/data/videos"`
	TempPath    string `yaml:"temp_path" envconfig:"STORAGE_TEMP_PATH" default:"/data/temp"`
	MaxFileSize int64  `yaml:"max_file_size" envconfig:"MAX_FILE_SIZE" default:"5368709120"` // 5GB
}

// WorkerConfig holds worker pool configuration.
type WorkerConfig struct {
	Count        int           `yaml:"count" envconfig:"WORKER_COUNT" default:"2"`
	PollInterval time.Duration `yaml:"poll_interval" envconfig:"WORKER_POLL_INTERVAL" default:"5s"`
	MaxRetries   int           `yaml:"max_retries" envconfig:"WORKER_MAX_RETRIES" default:"3"`
}

// GrokConfig holds Grok AI configuration.
type GrokConfig struct {
	APIKey  string        `yaml:"api_key" envconfig:"GROK_API_KEY"`
	BaseURL string        `yaml:"base_url" envconfig:"GROK_BASE_URL" default:"https://api.x.ai/v1"`
	Timeout time.Duration `yaml:"timeout" envconfig:"GROK_TIMEOUT" default:"30s"`
	Model   string        `yaml:"model" envconfig:"GROK_MODEL" default:"grok-3"`
}

// WhisperConfig holds OpenAI Whisper transcription configuration.
type WhisperConfig struct {
	APIKey  string        `yaml:"api_key" envconfig:"OPENAI_API_KEY"`
	BaseURL string        `yaml:"base_url" envconfig:"WHISPER_BASE_URL" default:"https://api.openai.com/v1"`
	Timeout time.Duration `yaml:"timeout" envconfig:"WHISPER_TIMEOUT" default:"5m"`
	Model   string        `yaml:"model" envconfig:"WHISPER_MODEL" default:"whisper-1"`
	Enabled bool          `yaml:"enabled" envconfig:"WHISPER_ENABLED" default:"true"`
}

// DownloadConfig holds video download configuration.
type DownloadConfig struct {
	Timeout       time.Duration `yaml:"timeout" envconfig:"DOWNLOAD_TIMEOUT" default:"10m"`
	RetryDelay    time.Duration `yaml:"retry_delay" envconfig:"DOWNLOAD_RETRY_DELAY" default:"5s"`
	MaxRetryDelay time.Duration `yaml:"max_retry_delay" envconfig:"DOWNLOAD_MAX_RETRY_DELAY" default:"60s"`
	UserAgent     string        `yaml:"user_agent" envconfig:"DOWNLOAD_USER_AGENT" default:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"`
}

// AIConfig holds orchestration timeouts for background AI jobs (not per-provider timeouts).
type AIConfig struct {
	// RegenerateTimeout is the max wall-clock time a background regenerate/backfill job is allowed to run.
	// This prevents "AI in progress" from getting stuck forever if an external process hangs.
	RegenerateTimeout time.Duration `yaml:"regenerate_timeout" envconfig:"AI_REGENERATE_TIMEOUT" default:"20m"`
}

// BookmarksConfig controls polling X bookmarks to trigger archiving.
type BookmarksConfig struct {
	Enabled bool   `yaml:"enabled" envconfig:"BOOKMARKS_ENABLED" default:"false"`
	UserID  string `yaml:"user_id" envconfig:"BOOKMARKS_USER_ID"`
	// Static bearer token mode (optional). If provided without refresh token settings, used directly.
	BearerToken string `yaml:"bearer_token" envconfig:"TWITTER_BEARER_TOKEN"`

	// OAuth2 refresh-token mode (recommended for unattended operation)
	OAuthClientID     string `yaml:"oauth_client_id" envconfig:"TWITTER_OAUTH_CLIENT_ID"`
	OAuthClientSecret string `yaml:"oauth_client_secret" envconfig:"TWITTER_OAUTH_CLIENT_SECRET"`
	RefreshToken      string `yaml:"refresh_token" envconfig:"TWITTER_OAUTH_REFRESH_TOKEN"`
	TokenURL          string `yaml:"token_url" envconfig:"TWITTER_OAUTH_TOKEN_URL" default:"https://api.x.com/2/oauth2/token"`
	OAuthStorePath    string `yaml:"oauth_store_path" envconfig:"BOOKMARKS_OAUTH_STORE_PATH" default:"/data/videos/.x_bookmarks_oauth.json"`
	BaseURL           string `yaml:"base_url" envconfig:"BOOKMARKS_BASE_URL" default:"https://api.x.com/2"`
	// Default poll interval is conservative to avoid free-tier rate limits (often 1 req / 15 min).
	PollInterval time.Duration `yaml:"poll_interval" envconfig:"BOOKMARKS_POLL_INTERVAL" default:"20m"`
	// Keep API response small; we only need recent IDs.
	MaxResults    int           `yaml:"max_results" envconfig:"BOOKMARKS_MAX_RESULTS" default:"20"`
	MaxNewPerPoll int           `yaml:"max_new_per_poll" envconfig:"BOOKMARKS_MAX_NEW_PER_POLL" default:"5"`
	SeenTTL       time.Duration `yaml:"seen_ttl" envconfig:"BOOKMARKS_SEEN_TTL" default:"720h"` // 30 days
}

// Load reads configuration from file and environment variables.
// Environment variables override file values.
func Load(configPath string) (*Config, error) {
	cfg := &Config{}

	// Load from YAML file if provided
	if configPath != "" {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read config file: %w", err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file: %w", err)
		}
	}

	// Override with environment variables
	if err := envconfig.Process("", cfg); err != nil {
		return nil, fmt.Errorf("process environment: %w", err)
	}

	// Validate
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

// Validate checks that required configuration values are set.
func (c *Config) Validate() error {
	if c.Server.APIKey == "" {
		return fmt.Errorf("API_KEY is required")
	}
	if c.Grok.APIKey == "" {
		return fmt.Errorf("GROK_API_KEY is required")
	}
	if c.Storage.BasePath == "" {
		return fmt.Errorf("STORAGE_PATH is required")
	}
	if c.Bookmarks.Enabled {
		// We can learn user_id from the OAuth connect flow (stored on disk), so only require it if we
		// don't have OAuth client credentials available.
		if c.Bookmarks.UserID == "" && c.Bookmarks.OAuthClientID == "" {
			return fmt.Errorf("BOOKMARKS_USER_ID is required when BOOKMARKS_ENABLED=true (unless using OAuth connect flow)")
		}
		// Auth can be:
		// - static bearer token
		// - refresh-token mode (client_id + refresh token)
		// - OAuth connect flow (client_id present; refresh token stored on disk)
		if c.Bookmarks.BearerToken == "" && c.Bookmarks.RefreshToken == "" && c.Bookmarks.OAuthClientID == "" {
			return fmt.Errorf("bookmarks auth missing: set TWITTER_BEARER_TOKEN or TWITTER_OAUTH_REFRESH_TOKEN+TWITTER_OAUTH_CLIENT_ID (or TWITTER_OAUTH_CLIENT_ID for connect flow)")
		}
		if c.Bookmarks.RefreshToken != "" && c.Bookmarks.OAuthClientID == "" {
			return fmt.Errorf("TWITTER_OAUTH_CLIENT_ID is required when TWITTER_OAUTH_REFRESH_TOKEN is set")
		}
		if c.Bookmarks.PollInterval < 10*time.Second {
			return fmt.Errorf("BOOKMARKS_POLL_INTERVAL too small (min 10s)")
		}
		if c.Bookmarks.MaxResults <= 0 || c.Bookmarks.MaxResults > 100 {
			return fmt.Errorf("BOOKMARKS_MAX_RESULTS must be 1-100")
		}
		if c.Bookmarks.MaxNewPerPoll <= 0 {
			return fmt.Errorf("BOOKMARKS_MAX_NEW_PER_POLL must be > 0")
		}
	}
	return nil
}

// Address returns the server address in host:port format.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
