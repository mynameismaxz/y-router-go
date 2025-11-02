package services

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"anthropic-openai-gateway/internal/config"
	"anthropic-openai-gateway/internal/models"
)

// RetryConfig holds configuration for retry mechanisms
type RetryConfig struct {
	MaxRetries      int           `json:"max_retries"`
	BaseDelay       time.Duration `json:"base_delay"`
	MaxDelay        time.Duration `json:"max_delay"`
	BackoffFactor   float64       `json:"backoff_factor"`
	JitterEnabled   bool          `json:"jitter_enabled"`
	JitterMaxFactor float64       `json:"jitter_max_factor"`
}

// DefaultRetryConfig returns default retry configuration
func DefaultRetryConfig() *RetryConfig {
	return &RetryConfig{
		MaxRetries:      3,
		BaseDelay:       1 * time.Second,
		MaxDelay:        30 * time.Second,
		BackoffFactor:   2.0,
		JitterEnabled:   true,
		JitterMaxFactor: 0.1, // 10% jitter
	}
}

// RetryStrategy defines different retry strategies for different error types
type RetryStrategy struct {
	ErrorType     string        `json:"error_type"`
	MaxRetries    int           `json:"max_retries"`
	BaseDelay     time.Duration `json:"base_delay"`
	BackoffFactor float64       `json:"backoff_factor"`
	Enabled       bool          `json:"enabled"`
}

// RetryAttempt tracks information about a retry attempt
type RetryAttempt struct {
	AttemptNumber int           `json:"attempt_number"`
	Error         error         `json:"error"`
	Delay         time.Duration `json:"delay"`
	Timestamp     time.Time     `json:"timestamp"`
	ErrorType     string        `json:"error_type"`
}

// RetryManager manages retry logic with exponential backoff and jitter
type RetryManager struct {
	config     *RetryConfig
	strategies map[string]*RetryStrategy
	classifier *ErrorClassifier
}

// NewRetryManager creates a new retry manager with the given configuration
func NewRetryManager(config *RetryConfig) *RetryManager {
	if config == nil {
		config = DefaultRetryConfig()
	}

	rm := &RetryManager{
		config:     config,
		strategies: make(map[string]*RetryStrategy),
		classifier: NewErrorClassifier(),
	}

	// Initialize default retry strategies for different error types
	rm.initializeDefaultStrategies()

	return rm
}

// NewRetryManagerFromStreamingConfig creates a retry manager from streaming configuration
func NewRetryManagerFromStreamingConfig(streamingConfig *config.StreamingConfig) *RetryManager {
	retryConfig := &RetryConfig{
		MaxRetries:      streamingConfig.MaxRetries,
		BaseDelay:       streamingConfig.RetryBackoffBase,
		MaxDelay:        streamingConfig.RetryBackoffMax,
		BackoffFactor:   2.0, // Default exponential backoff
		JitterEnabled:   true,
		JitterMaxFactor: 0.1,
	}

	return NewRetryManager(retryConfig)
}

// initializeDefaultStrategies sets up default retry strategies for different error types
func (rm *RetryManager) initializeDefaultStrategies() {
	// Connection errors - aggressive retry
	rm.strategies["connection_error"] = &RetryStrategy{
		ErrorType:     "connection_error",
		MaxRetries:    5,
		BaseDelay:     500 * time.Millisecond,
		BackoffFactor: 2.0,
		Enabled:       true,
	}

	// Timeout errors - moderate retry
	rm.strategies["timeout_error"] = &RetryStrategy{
		ErrorType:     "timeout_error",
		MaxRetries:    3,
		BaseDelay:     1 * time.Second,
		BackoffFactor: 1.5,
		Enabled:       true,
	}

	// Rate limit errors - conservative retry with longer delays
	rm.strategies["rate_limit_error"] = &RetryStrategy{
		ErrorType:     "rate_limit_error",
		MaxRetries:    3,
		BaseDelay:     5 * time.Second,
		BackoffFactor: 2.0,
		Enabled:       true,
	}

	// Server errors (5xx) - moderate retry
	rm.strategies["server_error"] = &RetryStrategy{
		ErrorType:     "server_error",
		MaxRetries:    3,
		BaseDelay:     2 * time.Second,
		BackoffFactor: 2.0,
		Enabled:       true,
	}

	// Network errors - aggressive retry
	rm.strategies["network_error"] = &RetryStrategy{
		ErrorType:     "network_error",
		MaxRetries:    4,
		BaseDelay:     1 * time.Second,
		BackoffFactor: 2.0,
		Enabled:       true,
	}

	// Authentication errors - no retry (not retryable)
	rm.strategies["authentication_error"] = &RetryStrategy{
		ErrorType:     "authentication_error",
		MaxRetries:    0,
		BaseDelay:     0,
		BackoffFactor: 0,
		Enabled:       false,
	}

	// Client errors (4xx except 429) - no retry
	rm.strategies["client_error"] = &RetryStrategy{
		ErrorType:     "client_error",
		MaxRetries:    0,
		BaseDelay:     0,
		BackoffFactor: 0,
		Enabled:       false,
	}
}

// ExecuteWithRetry executes a function with retry logic
func (rm *RetryManager) ExecuteWithRetry(ctx context.Context, operation func() error) error {
	var attempts []RetryAttempt
	var lastErr error

	for attempt := 0; attempt <= rm.config.MaxRetries; attempt++ {
		// Execute the operation
		err := operation()
		if err == nil {
			return nil // Success
		}

		lastErr = err

		// Classify the error to determine retry strategy
		errorType := rm.classifyError(err)
		strategy := rm.getStrategyForError(errorType)

		// Record the attempt
		attemptInfo := RetryAttempt{
			AttemptNumber: attempt + 1,
			Error:         err,
			Timestamp:     time.Now(),
			ErrorType:     errorType,
		}

		// Check if we should retry this error type
		if !strategy.Enabled || attempt >= strategy.MaxRetries {
			attempts = append(attempts, attemptInfo)
			break
		}

		// Calculate delay for next attempt
		delay := rm.calculateDelay(attempt, strategy)
		attemptInfo.Delay = delay
		attempts = append(attempts, attemptInfo)

		// Don't wait after the last allowed attempt
		if attempt < strategy.MaxRetries && attempt < rm.config.MaxRetries {
			select {
			case <-ctx.Done():
				return fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// Return the last error with retry context
	return &RetryError{
		LastError: lastErr,
		Attempts:  attempts,
		ErrorType: rm.classifyError(lastErr),
	}
}

// ExecuteHTTPWithRetry executes an HTTP operation with retry logic
func (rm *RetryManager) ExecuteHTTPWithRetry(ctx context.Context, operation func() (*http.Response, error)) (*http.Response, error) {
	var attempts []RetryAttempt
	var lastErr error
	var lastResp *http.Response

	for attempt := 0; attempt <= rm.config.MaxRetries; attempt++ {
		// Execute the HTTP operation
		resp, err := operation()
		if err == nil && resp != nil && resp.StatusCode < 500 {
			return resp, nil // Success or client error (don't retry client errors)
		}

		lastErr = err
		lastResp = resp

		// Determine error type from response or error
		var errorType string
		if err != nil {
			errorType = rm.classifyError(err)
		} else if resp != nil {
			errorType = rm.classifyHTTPStatusCode(resp.StatusCode)
		}

		strategy := rm.getStrategyForError(errorType)

		// Record the attempt
		attemptInfo := RetryAttempt{
			AttemptNumber: attempt + 1,
			Error:         err,
			Timestamp:     time.Now(),
			ErrorType:     errorType,
		}

		// Check if we should retry this error type
		if !strategy.Enabled || attempt >= strategy.MaxRetries {
			attempts = append(attempts, attemptInfo)
			break
		}

		// Close response body if present to avoid resource leaks
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		// Calculate delay for next attempt
		delay := rm.calculateDelay(attempt, strategy)
		attemptInfo.Delay = delay
		attempts = append(attempts, attemptInfo)

		// Don't wait after the last allowed attempt
		if attempt < strategy.MaxRetries && attempt < rm.config.MaxRetries {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("retry cancelled: %w", ctx.Err())
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// Return the last response/error with retry context
	if lastErr != nil {
		return lastResp, &RetryError{
			LastError: lastErr,
			Attempts:  attempts,
			ErrorType: rm.classifyError(lastErr),
		}
	}

	return lastResp, nil
}

// calculateDelay calculates the delay for the next retry attempt using exponential backoff with jitter
func (rm *RetryManager) calculateDelay(attempt int, strategy *RetryStrategy) time.Duration {
	// Use strategy-specific base delay if available, otherwise use config default
	baseDelay := strategy.BaseDelay
	if baseDelay == 0 {
		baseDelay = rm.config.BaseDelay
	}

	// Use strategy-specific backoff factor if available, otherwise use config default
	backoffFactor := strategy.BackoffFactor
	if backoffFactor == 0 {
		backoffFactor = rm.config.BackoffFactor
	}

	// Calculate exponential backoff delay
	delay := float64(baseDelay) * math.Pow(backoffFactor, float64(attempt))

	// Apply maximum delay limit
	if time.Duration(delay) > rm.config.MaxDelay {
		delay = float64(rm.config.MaxDelay)
	}

	// Add jitter if enabled
	if rm.config.JitterEnabled {
		jitter := rm.calculateJitter(time.Duration(delay))
		delay += float64(jitter)
	}

	return time.Duration(delay)
}

// calculateJitter calculates jitter to add randomness to retry delays
func (rm *RetryManager) calculateJitter(baseDelay time.Duration) time.Duration {
	if !rm.config.JitterEnabled || rm.config.JitterMaxFactor <= 0 {
		return 0
	}

	// Calculate maximum jitter amount
	maxJitter := float64(baseDelay) * rm.config.JitterMaxFactor

	// Generate random jitter between -maxJitter and +maxJitter
	jitter := (rand.Float64()*2 - 1) * maxJitter

	return time.Duration(jitter)
}

// classifyError classifies an error to determine the appropriate retry strategy
func (rm *RetryManager) classifyError(err error) string {
	if err == nil {
		return "no_error"
	}

	// Check if it's an API error
	if apiErr, ok := err.(*models.APIError); ok {
		return rm.classifyAPIError(apiErr)
	}

	// Check error message patterns
	errMsg := strings.ToLower(err.Error())

	// Connection-related errors
	connectionPatterns := []string{
		"connection refused",
		"connection reset",
		"connection timeout",
		"no route to host",
		"network unreachable",
		"broken pipe",
	}
	for _, pattern := range connectionPatterns {
		if strings.Contains(errMsg, pattern) {
			return "connection_error"
		}
	}

	// Timeout-related errors
	timeoutPatterns := []string{
		"timeout",
		"deadline exceeded",
		"context deadline exceeded",
		"request timeout",
	}
	for _, pattern := range timeoutPatterns {
		if strings.Contains(errMsg, pattern) {
			return "timeout_error"
		}
	}

	// Network-related errors
	networkPatterns := []string{
		"network",
		"dns",
		"host not found",
		"temporary failure",
	}
	for _, pattern := range networkPatterns {
		if strings.Contains(errMsg, pattern) {
			return "network_error"
		}
	}

	return "unknown_error"
}

// classifyAPIError classifies API errors based on status code and type
func (rm *RetryManager) classifyAPIError(apiErr *models.APIError) string {
	switch apiErr.Code {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusRequestTimeout:
		return "timeout_error"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "server_error"
	case http.StatusInternalServerError:
		return "server_error"
	default:
		if apiErr.Code >= 400 && apiErr.Code < 500 {
			return "client_error"
		} else if apiErr.Code >= 500 {
			return "server_error"
		}
	}

	// Check error type if available
	switch apiErr.Type {
	case "timeout_error":
		return "timeout_error"
	case "connection_error":
		return "connection_error"
	case "network_error":
		return "network_error"
	case "authentication_error":
		return "authentication_error"
	}

	return "unknown_error"
}

// classifyHTTPStatusCode classifies HTTP status codes for retry decisions
func (rm *RetryManager) classifyHTTPStatusCode(statusCode int) string {
	switch statusCode {
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusRequestTimeout:
		return "timeout_error"
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "server_error"
	case http.StatusInternalServerError:
		return "server_error"
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	default:
		if statusCode >= 400 && statusCode < 500 {
			return "client_error"
		} else if statusCode >= 500 {
			return "server_error"
		}
	}

	return "unknown_error"
}

// getStrategyForError returns the retry strategy for a given error type
func (rm *RetryManager) getStrategyForError(errorType string) *RetryStrategy {
	if strategy, exists := rm.strategies[errorType]; exists {
		return strategy
	}

	// Return default strategy for unknown error types
	return &RetryStrategy{
		ErrorType:     errorType,
		MaxRetries:    rm.config.MaxRetries,
		BaseDelay:     rm.config.BaseDelay,
		BackoffFactor: rm.config.BackoffFactor,
		Enabled:       true,
	}
}

// SetStrategy sets a custom retry strategy for a specific error type
func (rm *RetryManager) SetStrategy(errorType string, strategy *RetryStrategy) {
	rm.strategies[errorType] = strategy
}

// GetStrategy returns the retry strategy for a specific error type
func (rm *RetryManager) GetStrategy(errorType string) *RetryStrategy {
	return rm.getStrategyForError(errorType)
}

// UpdateConfig updates the retry manager configuration
func (rm *RetryManager) UpdateConfig(config *RetryConfig) {
	rm.config = config
}

// GetConfig returns the current retry configuration
func (rm *RetryManager) GetConfig() *RetryConfig {
	return rm.config
}

// IsRetryableError checks if an error is retryable based on configured strategies
func (rm *RetryManager) IsRetryableError(err error) bool {
	errorType := rm.classifyError(err)
	strategy := rm.getStrategyForError(errorType)
	return strategy.Enabled && strategy.MaxRetries > 0
}

// GetRetryDelay calculates the delay for a specific attempt and error type
func (rm *RetryManager) GetRetryDelay(attempt int, errorType string) time.Duration {
	strategy := rm.getStrategyForError(errorType)
	return rm.calculateDelay(attempt, strategy)
}

// RetryError represents an error that occurred after all retry attempts were exhausted
type RetryError struct {
	LastError error          `json:"last_error"`
	Attempts  []RetryAttempt `json:"attempts"`
	ErrorType string         `json:"error_type"`
}

// Error implements the error interface
func (re *RetryError) Error() string {
	return fmt.Sprintf("retry failed after %d attempts (error type: %s): %v",
		len(re.Attempts), re.ErrorType, re.LastError)
}

// Unwrap returns the underlying error for error unwrapping
func (re *RetryError) Unwrap() error {
	return re.LastError
}

// GetAttemptCount returns the number of retry attempts made
func (re *RetryError) GetAttemptCount() int {
	return len(re.Attempts)
}

// GetTotalRetryTime returns the total time spent on retries
func (re *RetryError) GetTotalRetryTime() time.Duration {
	if len(re.Attempts) < 2 {
		return 0
	}

	first := re.Attempts[0].Timestamp
	last := re.Attempts[len(re.Attempts)-1].Timestamp
	return last.Sub(first)
}

// RetryableHTTPClientWithManager wraps HTTPClient with advanced retry management
type RetryableHTTPClientWithManager struct {
	client       *HTTPClient
	retryManager *RetryManager
}

// NewRetryableHTTPClientWithManager creates a new HTTP client with advanced retry management
func NewRetryableHTTPClientWithManager(client *HTTPClient, retryConfig *RetryConfig) *RetryableHTTPClientWithManager {
	return &RetryableHTTPClientWithManager{
		client:       client,
		retryManager: NewRetryManager(retryConfig),
	}
}

// SendRequest sends a request with advanced retry logic
func (rc *RetryableHTTPClientWithManager) SendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	return rc.retryManager.ExecuteHTTPWithRetry(ctx, func() (*http.Response, error) {
		return rc.client.SendRequest(ctx, req, apiKey)
	})
}

// SendStreamingRequest sends a streaming request (no retry for streaming to maintain state consistency)
func (rc *RetryableHTTPClientWithManager) SendStreamingRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	// Streaming requests are not retried to maintain state consistency
	return rc.client.SendStreamingRequest(ctx, req, apiKey)
}

// GetRetryManager returns the retry manager for configuration access
func (rc *RetryableHTTPClientWithManager) GetRetryManager() *RetryManager {
	return rc.retryManager
}

// GetClient returns the underlying HTTP client
func (rc *RetryableHTTPClientWithManager) GetClient() *HTTPClient {
	return rc.client
}
