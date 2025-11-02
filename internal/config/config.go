package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// ConfigLoader defines the interface for loading configuration
type ConfigLoader interface {
	Load() (*Config, error)
}

// Config holds all configuration values for the application
type Config struct {
	Port              string           `json:"port"`
	OpenRouterBaseURL string           `json:"openrouter_base_url"`
	LogLevel          string           `json:"log_level"`
	ShutdownTimeout   time.Duration    `json:"shutdown_timeout"`
	Streaming         *StreamingConfig `json:"streaming"`
}

// LoadFromEnv loads configuration from environment variables with defaults
func LoadFromEnv() (*Config, error) {
	// Load streaming configuration
	streamingConfig, err := LoadStreamingConfigFromEnv()
	if err != nil {
		return nil, fmt.Errorf("failed to load streaming configuration: %w", err)
	}

	config := &Config{
		Port:              getEnvWithDefault("PORT", "8080"),
		OpenRouterBaseURL: getEnvWithDefault("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		LogLevel:          getEnvWithDefault("LOG_LEVEL", "info"),
		ShutdownTimeout:   parseTimeoutWithDefault("SHUTDOWN_TIMEOUT", "30s"),
		Streaming:         streamingConfig,
	}

	// Validate required configuration values
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("configuration validation failed: %w", err)
	}

	return config, nil
}

// validate checks that all required configuration values are present and valid
func (c *Config) validate() error {
	if c.Port == "" {
		return fmt.Errorf("PORT cannot be empty")
	}

	// Validate port is a valid number
	if _, err := strconv.Atoi(c.Port); err != nil {
		return fmt.Errorf("PORT must be a valid number: %w", err)
	}

	if c.OpenRouterBaseURL == "" {
		return fmt.Errorf("OPENROUTER_BASE_URL cannot be empty")
	}

	if c.LogLevel == "" {
		return fmt.Errorf("LOG_LEVEL cannot be empty")
	}

	// Validate log level is one of the supported values
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("LOG_LEVEL must be one of: debug, info, warn, error")
	}

	if c.ShutdownTimeout <= 0 {
		return fmt.Errorf("SHUTDOWN_TIMEOUT must be greater than 0")
	}

	// Validate streaming configuration
	if c.Streaming == nil {
		return fmt.Errorf("streaming configuration is required")
	}

	if err := c.Streaming.Validate(); err != nil {
		return fmt.Errorf("streaming configuration validation failed: %w", err)
	}

	return nil
}

// getEnvWithDefault returns the environment variable value or the default if not set
func getEnvWithDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// parseTimeoutWithDefault parses a duration from environment variable or returns default
func parseTimeoutWithDefault(key, defaultValue string) time.Duration {
	value := getEnvWithDefault(key, defaultValue)
	duration, err := time.ParseDuration(value)
	if err != nil {
		// If parsing fails, use the default
		duration, _ = time.ParseDuration(defaultValue)
	}
	return duration
}

// EnvConfigLoader implements ConfigLoader for environment-based configuration
type EnvConfigLoader struct{}

// NewEnvConfigLoader creates a new environment-based configuration loader
func NewEnvConfigLoader() ConfigLoader {
	return &EnvConfigLoader{}
}

// Load implements the ConfigLoader interface for environment variables
func (e *EnvConfigLoader) Load() (*Config, error) {
	return LoadFromEnv()
}

// DefaultConfigLoader returns the default configuration loader (environment-based)
func DefaultConfigLoader() ConfigLoader {
	return NewEnvConfigLoader()
}

// UpdateStreamingConfig updates the streaming configuration with new values
func (c *Config) UpdateStreamingConfig(updates map[string]interface{}) error {
	if c.Streaming == nil {
		c.Streaming = DefaultStreamingConfig()
	}

	return c.Streaming.Update(updates)
}

// GetStreamingConfig returns the current streaming configuration
func (c *Config) GetStreamingConfig() *StreamingConfig {
	if c.Streaming == nil {
		return DefaultStreamingConfig()
	}
	return c.Streaming
}

// SetStreamingConfig sets the streaming configuration
func (c *Config) SetStreamingConfig(streamingConfig *StreamingConfig) error {
	if streamingConfig == nil {
		return fmt.Errorf("streaming configuration cannot be nil")
	}

	if err := streamingConfig.Validate(); err != nil {
		return fmt.Errorf("invalid streaming configuration: %w", err)
	}

	c.Streaming = streamingConfig
	return nil
}

// ReloadStreamingConfig reloads the streaming configuration from environment variables
func (c *Config) ReloadStreamingConfig() error {
	streamingConfig, err := LoadStreamingConfigFromEnv()
	if err != nil {
		return fmt.Errorf("failed to reload streaming configuration: %w", err)
	}

	c.Streaming = streamingConfig
	return nil
}

// ValidateAtStartup performs comprehensive validation at application startup
func (c *Config) ValidateAtStartup() error {
	// Validate basic configuration
	if err := c.validate(); err != nil {
		return fmt.Errorf("basic configuration validation failed: %w", err)
	}

	// Validate streaming configuration compatibility
	if err := c.validateStreamingCompatibility(); err != nil {
		return fmt.Errorf("streaming configuration compatibility check failed: %w", err)
	}

	return nil
}

// validateStreamingCompatibility checks for configuration compatibility issues
func (c *Config) validateStreamingCompatibility() error {
	if c.Streaming == nil {
		return fmt.Errorf("streaming configuration is required")
	}

	// Check for reasonable timeout relationships
	if c.Streaming.ReadTimeout < c.Streaming.ConnectionTimeout {
		return fmt.Errorf("read timeout (%v) should be greater than or equal to connection timeout (%v)",
			c.Streaming.ReadTimeout, c.Streaming.ConnectionTimeout)
	}

	// Check buffer size relationships
	if c.Streaming.BufferSize > c.Streaming.MaxBufferSize {
		return fmt.Errorf("buffer size (%d) cannot be greater than max buffer size (%d)",
			c.Streaming.BufferSize, c.Streaming.MaxBufferSize)
	}

	// Check connection pool settings
	if c.Streaming.MaxConnsPerHost > c.Streaming.MaxIdleConns {
		return fmt.Errorf("max connections per host (%d) should not exceed max idle connections (%d)",
			c.Streaming.MaxConnsPerHost, c.Streaming.MaxIdleConns)
	}

	// Check retry settings
	if c.Streaming.RetryBackoffBase > c.Streaming.RetryBackoffMax {
		return fmt.Errorf("retry backoff base (%v) cannot be greater than max (%v)",
			c.Streaming.RetryBackoffBase, c.Streaming.RetryBackoffMax)
	}

	// Check circuit breaker settings
	if c.Streaming.CircuitBreakerThreshold <= 0 {
		return fmt.Errorf("circuit breaker threshold must be positive")
	}

	if c.Streaming.RecoveryThreshold > c.Streaming.CircuitBreakerThreshold {
		return fmt.Errorf("recovery threshold (%d) should not exceed circuit breaker threshold (%d)",
			c.Streaming.RecoveryThreshold, c.Streaming.CircuitBreakerThreshold)
	}

	return nil
}
