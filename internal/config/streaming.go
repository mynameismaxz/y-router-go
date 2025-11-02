package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// StreamingConfig holds all streaming-specific configuration parameters
type StreamingConfig struct {
	// Connection settings
	ConnectionTimeout   time.Duration `json:"connection_timeout"`
	ReadTimeout         time.Duration `json:"read_timeout"`
	WriteTimeout        time.Duration `json:"write_timeout"`
	KeepAlive           time.Duration `json:"keep_alive"`
	TLSHandshakeTimeout time.Duration `json:"tls_handshake_timeout"`

	// Connection pool settings
	MaxIdleConns        int           `json:"max_idle_conns"`
	MaxConnsPerHost     int           `json:"max_conns_per_host"`
	IdleConnTimeout     time.Duration `json:"idle_conn_timeout"`
	MaxIdleConnsPerHost int           `json:"max_idle_conns_per_host"`

	// HTTP/2 settings
	HTTP2Enabled       bool `json:"http2_enabled"`
	HTTP2MaxConcurrent int  `json:"http2_max_concurrent"`
	HTTP2InitialWindow int  `json:"http2_initial_window"`

	// Retry and circuit breaker settings
	MaxRetries              int           `json:"max_retries"`
	RetryBackoffBase        time.Duration `json:"retry_backoff_base"`
	RetryBackoffMax         time.Duration `json:"retry_backoff_max"`
	CircuitBreakerThreshold int           `json:"circuit_breaker_threshold"`
	CircuitBreakerTimeout   time.Duration `json:"circuit_breaker_timeout"`

	// Buffer management settings
	BufferSize         int           `json:"buffer_size"`
	MaxBufferSize      int           `json:"max_buffer_size"`
	BufferFlushTimeout time.Duration `json:"buffer_flush_timeout"`
	JSONValidation     bool          `json:"json_validation"`

	// Health monitoring settings
	HealthCheckInterval time.Duration `json:"health_check_interval"`
	HealthCheckTimeout  time.Duration `json:"health_check_timeout"`
	FailureThreshold    int           `json:"failure_threshold"`
	RecoveryThreshold   int           `json:"recovery_threshold"`

	// Performance settings
	WriteBufferSize    int  `json:"write_buffer_size"`
	ReadBufferSize     int  `json:"read_buffer_size"`
	DisableCompression bool `json:"disable_compression"`
	DisableKeepAlives  bool `json:"disable_keep_alives"`

	// Monitoring and logging
	EnableMetrics       bool   `json:"enable_metrics"`
	EnableTracing       bool   `json:"enable_tracing"`
	LogLevel            string `json:"log_level"`
	CorrelationIDHeader string `json:"correlation_id_header"`
}

// DefaultStreamingConfig returns the default streaming configuration
func DefaultStreamingConfig() *StreamingConfig {
	return &StreamingConfig{
		// Connection settings
		ConnectionTimeout:   30 * time.Second,
		ReadTimeout:         60 * time.Second,
		WriteTimeout:        30 * time.Second,
		KeepAlive:           30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,

		// Connection pool settings
		MaxIdleConns:        100,
		MaxConnsPerHost:     20,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 10,

		// HTTP/2 settings
		HTTP2Enabled:       true,
		HTTP2MaxConcurrent: 100,
		HTTP2InitialWindow: 65536,

		// Retry and circuit breaker settings
		MaxRetries:              3,
		RetryBackoffBase:        1 * time.Second,
		RetryBackoffMax:         30 * time.Second,
		CircuitBreakerThreshold: 5,
		CircuitBreakerTimeout:   60 * time.Second,

		// Buffer management settings
		BufferSize:         65536,   // 64KB
		MaxBufferSize:      1048576, // 1MB
		BufferFlushTimeout: 5 * time.Second,
		JSONValidation:     true,

		// Health monitoring settings
		HealthCheckInterval: 30 * time.Second,
		HealthCheckTimeout:  10 * time.Second,
		FailureThreshold:    3,
		RecoveryThreshold:   2,

		// Performance settings
		WriteBufferSize:    32768, // 32KB
		ReadBufferSize:     32768, // 32KB
		DisableCompression: false,
		DisableKeepAlives:  false,

		// Monitoring and logging
		EnableMetrics:       true,
		EnableTracing:       false,
		LogLevel:            "info",
		CorrelationIDHeader: "X-Correlation-ID",
	}
}

// LoadStreamingConfigFromEnv loads streaming configuration from environment variables
func LoadStreamingConfigFromEnv() (*StreamingConfig, error) {
	config := DefaultStreamingConfig()

	// Connection settings
	if timeout := getEnvDuration("STREAMING_CONNECTION_TIMEOUT", config.ConnectionTimeout); timeout > 0 {
		config.ConnectionTimeout = timeout
	}
	if timeout := getEnvDuration("STREAMING_READ_TIMEOUT", config.ReadTimeout); timeout > 0 {
		config.ReadTimeout = timeout
	}
	if timeout := getEnvDuration("STREAMING_WRITE_TIMEOUT", config.WriteTimeout); timeout > 0 {
		config.WriteTimeout = timeout
	}
	if timeout := getEnvDuration("STREAMING_KEEP_ALIVE", config.KeepAlive); timeout > 0 {
		config.KeepAlive = timeout
	}
	if timeout := getEnvDuration("STREAMING_TLS_HANDSHAKE_TIMEOUT", config.TLSHandshakeTimeout); timeout > 0 {
		config.TLSHandshakeTimeout = timeout
	}

	// Connection pool settings
	if conns := getEnvInt("STREAMING_MAX_IDLE_CONNS", config.MaxIdleConns); conns > 0 {
		config.MaxIdleConns = conns
	}
	if conns := getEnvInt("STREAMING_MAX_CONNS_PER_HOST", config.MaxConnsPerHost); conns > 0 {
		config.MaxConnsPerHost = conns
	}
	if timeout := getEnvDuration("STREAMING_IDLE_CONN_TIMEOUT", config.IdleConnTimeout); timeout > 0 {
		config.IdleConnTimeout = timeout
	}
	if conns := getEnvInt("STREAMING_MAX_IDLE_CONNS_PER_HOST", config.MaxIdleConnsPerHost); conns > 0 {
		config.MaxIdleConnsPerHost = conns
	}

	// HTTP/2 settings
	config.HTTP2Enabled = getEnvBool("STREAMING_HTTP2_ENABLED", config.HTTP2Enabled)
	if concurrent := getEnvInt("STREAMING_HTTP2_MAX_CONCURRENT", config.HTTP2MaxConcurrent); concurrent > 0 {
		config.HTTP2MaxConcurrent = concurrent
	}
	if window := getEnvInt("STREAMING_HTTP2_INITIAL_WINDOW", config.HTTP2InitialWindow); window > 0 {
		config.HTTP2InitialWindow = window
	}

	// Retry and circuit breaker settings
	if retries := getEnvInt("STREAMING_MAX_RETRIES", config.MaxRetries); retries >= 0 {
		config.MaxRetries = retries
	}
	if backoff := getEnvDuration("STREAMING_RETRY_BACKOFF_BASE", config.RetryBackoffBase); backoff > 0 {
		config.RetryBackoffBase = backoff
	}
	if backoff := getEnvDuration("STREAMING_RETRY_BACKOFF_MAX", config.RetryBackoffMax); backoff > 0 {
		config.RetryBackoffMax = backoff
	}
	if threshold := getEnvInt("STREAMING_CIRCUIT_BREAKER_THRESHOLD", config.CircuitBreakerThreshold); threshold > 0 {
		config.CircuitBreakerThreshold = threshold
	}
	if timeout := getEnvDuration("STREAMING_CIRCUIT_BREAKER_TIMEOUT", config.CircuitBreakerTimeout); timeout > 0 {
		config.CircuitBreakerTimeout = timeout
	}

	// Buffer management settings
	if size := getEnvInt("STREAMING_BUFFER_SIZE", config.BufferSize); size > 0 {
		config.BufferSize = size
	}
	if size := getEnvInt("STREAMING_MAX_BUFFER_SIZE", config.MaxBufferSize); size > 0 {
		config.MaxBufferSize = size
	}
	if timeout := getEnvDuration("STREAMING_BUFFER_FLUSH_TIMEOUT", config.BufferFlushTimeout); timeout > 0 {
		config.BufferFlushTimeout = timeout
	}
	config.JSONValidation = getEnvBool("STREAMING_JSON_VALIDATION", config.JSONValidation)

	// Health monitoring settings
	if interval := getEnvDuration("STREAMING_HEALTH_CHECK_INTERVAL", config.HealthCheckInterval); interval > 0 {
		config.HealthCheckInterval = interval
	}
	if timeout := getEnvDuration("STREAMING_HEALTH_CHECK_TIMEOUT", config.HealthCheckTimeout); timeout > 0 {
		config.HealthCheckTimeout = timeout
	}
	if threshold := getEnvInt("STREAMING_FAILURE_THRESHOLD", config.FailureThreshold); threshold > 0 {
		config.FailureThreshold = threshold
	}
	if threshold := getEnvInt("STREAMING_RECOVERY_THRESHOLD", config.RecoveryThreshold); threshold > 0 {
		config.RecoveryThreshold = threshold
	}

	// Performance settings
	if size := getEnvInt("STREAMING_WRITE_BUFFER_SIZE", config.WriteBufferSize); size > 0 {
		config.WriteBufferSize = size
	}
	if size := getEnvInt("STREAMING_READ_BUFFER_SIZE", config.ReadBufferSize); size > 0 {
		config.ReadBufferSize = size
	}
	config.DisableCompression = getEnvBool("STREAMING_DISABLE_COMPRESSION", config.DisableCompression)
	config.DisableKeepAlives = getEnvBool("STREAMING_DISABLE_KEEP_ALIVES", config.DisableKeepAlives)

	// Monitoring and logging
	config.EnableMetrics = getEnvBool("STREAMING_ENABLE_METRICS", config.EnableMetrics)
	config.EnableTracing = getEnvBool("STREAMING_ENABLE_TRACING", config.EnableTracing)
	if logLevel := getEnvWithDefault("STREAMING_LOG_LEVEL", config.LogLevel); logLevel != "" {
		config.LogLevel = logLevel
	}
	if header := getEnvWithDefault("STREAMING_CORRELATION_ID_HEADER", config.CorrelationIDHeader); header != "" {
		config.CorrelationIDHeader = header
	}

	// Validate the configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("streaming configuration validation failed: %w", err)
	}

	return config, nil
}

// Validate validates the streaming configuration values
func (sc *StreamingConfig) Validate() error {
	// Validate timeouts
	if sc.ConnectionTimeout <= 0 {
		return fmt.Errorf("connection timeout must be greater than 0")
	}
	if sc.ReadTimeout <= 0 {
		return fmt.Errorf("read timeout must be greater than 0")
	}
	if sc.WriteTimeout <= 0 {
		return fmt.Errorf("write timeout must be greater than 0")
	}
	if sc.KeepAlive <= 0 {
		return fmt.Errorf("keep alive must be greater than 0")
	}
	if sc.TLSHandshakeTimeout <= 0 {
		return fmt.Errorf("TLS handshake timeout must be greater than 0")
	}

	// Validate connection pool settings
	if sc.MaxIdleConns <= 0 {
		return fmt.Errorf("max idle connections must be greater than 0")
	}
	if sc.MaxConnsPerHost <= 0 {
		return fmt.Errorf("max connections per host must be greater than 0")
	}
	if sc.IdleConnTimeout <= 0 {
		return fmt.Errorf("idle connection timeout must be greater than 0")
	}
	if sc.MaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("max idle connections per host must be greater than 0")
	}

	// Validate HTTP/2 settings
	if sc.HTTP2MaxConcurrent <= 0 {
		return fmt.Errorf("HTTP/2 max concurrent streams must be greater than 0")
	}
	if sc.HTTP2InitialWindow <= 0 {
		return fmt.Errorf("HTTP/2 initial window size must be greater than 0")
	}

	// Validate retry settings
	if sc.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}
	if sc.RetryBackoffBase <= 0 {
		return fmt.Errorf("retry backoff base must be greater than 0")
	}
	if sc.RetryBackoffMax <= 0 {
		return fmt.Errorf("retry backoff max must be greater than 0")
	}
	if sc.RetryBackoffMax < sc.RetryBackoffBase {
		return fmt.Errorf("retry backoff max must be greater than or equal to base")
	}

	// Validate circuit breaker settings
	if sc.CircuitBreakerThreshold <= 0 {
		return fmt.Errorf("circuit breaker threshold must be greater than 0")
	}
	if sc.CircuitBreakerTimeout <= 0 {
		return fmt.Errorf("circuit breaker timeout must be greater than 0")
	}

	// Validate buffer settings
	if sc.BufferSize <= 0 {
		return fmt.Errorf("buffer size must be greater than 0")
	}
	if sc.MaxBufferSize <= 0 {
		return fmt.Errorf("max buffer size must be greater than 0")
	}
	if sc.MaxBufferSize < sc.BufferSize {
		return fmt.Errorf("max buffer size must be greater than or equal to buffer size")
	}
	if sc.BufferFlushTimeout <= 0 {
		return fmt.Errorf("buffer flush timeout must be greater than 0")
	}

	// Validate health monitoring settings
	if sc.HealthCheckInterval <= 0 {
		return fmt.Errorf("health check interval must be greater than 0")
	}
	if sc.HealthCheckTimeout <= 0 {
		return fmt.Errorf("health check timeout must be greater than 0")
	}
	if sc.FailureThreshold <= 0 {
		return fmt.Errorf("failure threshold must be greater than 0")
	}
	if sc.RecoveryThreshold <= 0 {
		return fmt.Errorf("recovery threshold must be greater than 0")
	}

	// Validate performance settings
	if sc.WriteBufferSize <= 0 {
		return fmt.Errorf("write buffer size must be greater than 0")
	}
	if sc.ReadBufferSize <= 0 {
		return fmt.Errorf("read buffer size must be greater than 0")
	}

	// Validate log level
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[sc.LogLevel] {
		return fmt.Errorf("log level must be one of: debug, info, warn, error")
	}

	// Validate correlation ID header
	if sc.CorrelationIDHeader == "" {
		return fmt.Errorf("correlation ID header cannot be empty")
	}

	return nil
}

// Update updates the streaming configuration with new values
func (sc *StreamingConfig) Update(updates map[string]interface{}) error {
	for key, value := range updates {
		if err := sc.updateField(key, value); err != nil {
			return fmt.Errorf("failed to update field %s: %w", key, err)
		}
	}

	// Validate after updates
	return sc.Validate()
}

// updateField updates a specific configuration field
func (sc *StreamingConfig) updateField(field string, value interface{}) error {
	switch field {
	// Connection settings
	case "connection_timeout":
		if duration, ok := value.(time.Duration); ok {
			sc.ConnectionTimeout = duration
		} else {
			return fmt.Errorf("connection_timeout must be a duration")
		}
	case "read_timeout":
		if duration, ok := value.(time.Duration); ok {
			sc.ReadTimeout = duration
		} else {
			return fmt.Errorf("read_timeout must be a duration")
		}
	case "write_timeout":
		if duration, ok := value.(time.Duration); ok {
			sc.WriteTimeout = duration
		} else {
			return fmt.Errorf("write_timeout must be a duration")
		}
	case "keep_alive":
		if duration, ok := value.(time.Duration); ok {
			sc.KeepAlive = duration
		} else {
			return fmt.Errorf("keep_alive must be a duration")
		}
	case "tls_handshake_timeout":
		if duration, ok := value.(time.Duration); ok {
			sc.TLSHandshakeTimeout = duration
		} else {
			return fmt.Errorf("tls_handshake_timeout must be a duration")
		}

	// Connection pool settings
	case "max_idle_conns":
		if count, ok := value.(int); ok {
			sc.MaxIdleConns = count
		} else {
			return fmt.Errorf("max_idle_conns must be an integer")
		}
	case "max_conns_per_host":
		if count, ok := value.(int); ok {
			sc.MaxConnsPerHost = count
		} else {
			return fmt.Errorf("max_conns_per_host must be an integer")
		}
	case "idle_conn_timeout":
		if duration, ok := value.(time.Duration); ok {
			sc.IdleConnTimeout = duration
		} else {
			return fmt.Errorf("idle_conn_timeout must be a duration")
		}

	// HTTP/2 settings
	case "http2_enabled":
		if enabled, ok := value.(bool); ok {
			sc.HTTP2Enabled = enabled
		} else {
			return fmt.Errorf("http2_enabled must be a boolean")
		}

	// Retry settings
	case "max_retries":
		if retries, ok := value.(int); ok {
			sc.MaxRetries = retries
		} else {
			return fmt.Errorf("max_retries must be an integer")
		}

	// Buffer settings
	case "buffer_size":
		if size, ok := value.(int); ok {
			sc.BufferSize = size
		} else {
			return fmt.Errorf("buffer_size must be an integer")
		}
	case "max_buffer_size":
		if size, ok := value.(int); ok {
			sc.MaxBufferSize = size
		} else {
			return fmt.Errorf("max_buffer_size must be an integer")
		}
	case "json_validation":
		if enabled, ok := value.(bool); ok {
			sc.JSONValidation = enabled
		} else {
			return fmt.Errorf("json_validation must be a boolean")
		}

	// Monitoring settings
	case "enable_metrics":
		if enabled, ok := value.(bool); ok {
			sc.EnableMetrics = enabled
		} else {
			return fmt.Errorf("enable_metrics must be a boolean")
		}
	case "enable_tracing":
		if enabled, ok := value.(bool); ok {
			sc.EnableTracing = enabled
		} else {
			return fmt.Errorf("enable_tracing must be a boolean")
		}
	case "log_level":
		if level, ok := value.(string); ok {
			sc.LogLevel = level
		} else {
			return fmt.Errorf("log_level must be a string")
		}

	default:
		return fmt.Errorf("unknown configuration field: %s", field)
	}

	return nil
}

// ToMap converts the streaming configuration to a map for serialization
func (sc *StreamingConfig) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"connection_timeout":        sc.ConnectionTimeout,
		"read_timeout":              sc.ReadTimeout,
		"write_timeout":             sc.WriteTimeout,
		"keep_alive":                sc.KeepAlive,
		"tls_handshake_timeout":     sc.TLSHandshakeTimeout,
		"max_idle_conns":            sc.MaxIdleConns,
		"max_conns_per_host":        sc.MaxConnsPerHost,
		"idle_conn_timeout":         sc.IdleConnTimeout,
		"max_idle_conns_per_host":   sc.MaxIdleConnsPerHost,
		"http2_enabled":             sc.HTTP2Enabled,
		"http2_max_concurrent":      sc.HTTP2MaxConcurrent,
		"http2_initial_window":      sc.HTTP2InitialWindow,
		"max_retries":               sc.MaxRetries,
		"retry_backoff_base":        sc.RetryBackoffBase,
		"retry_backoff_max":         sc.RetryBackoffMax,
		"circuit_breaker_threshold": sc.CircuitBreakerThreshold,
		"circuit_breaker_timeout":   sc.CircuitBreakerTimeout,
		"buffer_size":               sc.BufferSize,
		"max_buffer_size":           sc.MaxBufferSize,
		"buffer_flush_timeout":      sc.BufferFlushTimeout,
		"json_validation":           sc.JSONValidation,
		"health_check_interval":     sc.HealthCheckInterval,
		"health_check_timeout":      sc.HealthCheckTimeout,
		"failure_threshold":         sc.FailureThreshold,
		"recovery_threshold":        sc.RecoveryThreshold,
		"write_buffer_size":         sc.WriteBufferSize,
		"read_buffer_size":          sc.ReadBufferSize,
		"disable_compression":       sc.DisableCompression,
		"disable_keep_alives":       sc.DisableKeepAlives,
		"enable_metrics":            sc.EnableMetrics,
		"enable_tracing":            sc.EnableTracing,
		"log_level":                 sc.LogLevel,
		"correlation_id_header":     sc.CorrelationIDHeader,
	}
}

// Helper functions for environment variable parsing

// getEnvInt parses an integer from environment variable with fallback to default
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

// getEnvBool parses a boolean from environment variable with fallback to default
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolValue, err := strconv.ParseBool(value); err == nil {
			return boolValue
		}
	}
	return defaultValue
}

// getEnvDuration parses a duration from environment variable with fallback to default
func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}
