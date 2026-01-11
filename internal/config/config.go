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
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Worker   WorkerConfig   `yaml:"worker"`
	Grok     GrokConfig     `yaml:"grok"`
	Download DownloadConfig `yaml:"download"`
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
	Model   string        `yaml:"model" envconfig:"GROK_MODEL" default:"grok-beta"`
}

// DownloadConfig holds video download configuration.
type DownloadConfig struct {
	Timeout       time.Duration `yaml:"timeout" envconfig:"DOWNLOAD_TIMEOUT" default:"10m"`
	RetryDelay    time.Duration `yaml:"retry_delay" envconfig:"DOWNLOAD_RETRY_DELAY" default:"5s"`
	MaxRetryDelay time.Duration `yaml:"max_retry_delay" envconfig:"DOWNLOAD_MAX_RETRY_DELAY" default:"60s"`
	UserAgent     string        `yaml:"user_agent" envconfig:"DOWNLOAD_USER_AGENT" default:"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"`
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
	return nil
}

// Address returns the server address in host:port format.
func (c *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}
