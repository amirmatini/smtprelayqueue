// Copyright (c) 2024 SMTP Relay Contributors
// Licensed under the MIT License

package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the main configuration structure
type Config struct {
	Incoming  IncomingConfig  `yaml:"incoming"`
	Outgoing  OutgoingConfig  `yaml:"outgoing"`
	Storage   StorageConfig   `yaml:"storage"`
	Logging   LoggingConfig   `yaml:"logging"`
	RateLimit RateLimitConfig `yaml:"rate_limit"`
	Retry     RetryConfig     `yaml:"retry"`
}

// IncomingConfig represents the incoming SMTP server configuration
type IncomingConfig struct {
	Host string     `yaml:"host"`
	Port int        `yaml:"port"`
	TLS  TLSConfig  `yaml:"tls"`
	Auth AuthConfig `yaml:"auth"`
	// Server limits to prevent hanging
	MaxConnections    int           `yaml:"max_connections"`
	ConnectionTimeout time.Duration `yaml:"connection_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
}

// OutgoingConfig represents the outgoing SMTP server configuration
type OutgoingConfig struct {
	Host string     `yaml:"host"`
	Port int        `yaml:"port"`
	TLS  TLSConfig  `yaml:"tls"`
	Auth AuthConfig `yaml:"auth"`
}

// TLSConfig represents TLS configuration
type TLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	Mode       string `yaml:"mode"` // starttls, ssl, none
	SkipVerify bool   `yaml:"skip_verify"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
}

// AuthConfig represents authentication configuration
type AuthConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Method   string `yaml:"method"` // plain, login, cram-md5
}

// StorageConfig represents storage configuration
type StorageConfig struct {
	Type   string     `yaml:"type"` // file, memory
	File   FileConfig `yaml:"file"`
	Memory MemConfig  `yaml:"memory"`
}

// FileConfig represents file storage configuration
type FileConfig struct {
	Path    string `yaml:"path"`
	MaxSize string `yaml:"max_size"`
}

// MemConfig represents memory storage configuration
type MemConfig struct {
	MaxMessages int `yaml:"max_messages"`
}

// LoggingConfig represents logging configuration
type LoggingConfig struct {
	Level  string `yaml:"level"`  // debug, info, warn, error
	Format string `yaml:"format"` // json, text
	File   string `yaml:"file"`
}

// RateLimitConfig represents rate limiting configuration
type RateLimitConfig struct {
	Enabled           bool `yaml:"enabled"`
	RequestsPerMinute int  `yaml:"requests_per_minute"`
	Burst             int  `yaml:"burst"`
}

// RetryConfig represents retry configuration for failed emails
type RetryConfig struct {
	Enabled           bool          `yaml:"enabled"`
	MaxAttempts       int           `yaml:"max_attempts"`
	InitialDelay      time.Duration `yaml:"initial_delay"`
	MaxDelay          time.Duration `yaml:"max_delay"`
	BackoffMultiplier float64       `yaml:"backoff_multiplier"`
	RetryQueueSize    int           `yaml:"retry_queue_size"`
	// New options for better control
	CleanupFailedAfter time.Duration `yaml:"cleanup_failed_after"`
	MaxRetryHistory    int           `yaml:"max_retry_history"`
	RetryForever       bool          `yaml:"retry_forever"`
}

// Load loads configuration from a YAML file
func Load(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	if err := validate(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// validate validates the configuration
func validate(config *Config) error {
	if config.Incoming.Port <= 0 || config.Incoming.Port > 65535 {
		return fmt.Errorf("invalid incoming port: %d", config.Incoming.Port)
	}

	if config.Outgoing.Port <= 0 || config.Outgoing.Port > 65535 {
		return fmt.Errorf("invalid outgoing port: %d", config.Outgoing.Port)
	}

	if config.Incoming.TLS.Enabled {
		if config.Incoming.TLS.CertFile == "" || config.Incoming.TLS.KeyFile == "" {
			return fmt.Errorf("TLS enabled but certificate files not specified")
		}
	}

	if config.Outgoing.TLS.Enabled {
		if config.Outgoing.TLS.Mode != "starttls" && config.Outgoing.TLS.Mode != "ssl" && config.Outgoing.TLS.Mode != "none" {
			return fmt.Errorf("invalid TLS mode: %s", config.Outgoing.TLS.Mode)
		}
	}

	if config.Incoming.Auth.Enabled {
		if config.Incoming.Auth.Username == "" || config.Incoming.Auth.Password == "" {
			return fmt.Errorf("authentication enabled but credentials not specified")
		}
	}

	if config.Outgoing.Auth.Enabled {
		if config.Outgoing.Auth.Username == "" || config.Outgoing.Auth.Password == "" {
			return fmt.Errorf("authentication enabled but credentials not specified")
		}
	}

	if config.Storage.Type != "file" && config.Storage.Type != "memory" {
		return fmt.Errorf("invalid storage type: %s", config.Storage.Type)
	}

	// Validate retry configuration
	if config.Retry.Enabled {
		if config.Retry.MaxAttempts <= 0 {
			return fmt.Errorf("retry max_attempts must be greater than 0")
		}
		if config.Retry.InitialDelay < 0 {
			return fmt.Errorf("retry initial_delay cannot be negative")
		}
		if config.Retry.MaxDelay < config.Retry.InitialDelay {
			return fmt.Errorf("retry max_delay must be greater than or equal to initial_delay")
		}
		if config.Retry.BackoffMultiplier <= 0 {
			return fmt.Errorf("retry backoff_multiplier must be greater than 0")
		}
		if config.Retry.RetryQueueSize <= 0 {
			return fmt.Errorf("retry queue_size must be greater than 0")
		}
	}

	return nil
}
