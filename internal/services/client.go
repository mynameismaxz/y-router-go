package services

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"anthropic-openai-gateway/internal/config"
	"anthropic-openai-gateway/internal/models"
)

// OpenRouterClient defines the interface for making requests to OpenRouter API
type OpenRouterClient interface {
	SendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error)
	SendStreamingRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error)
}

// ConnectionHealth represents the health status of HTTP connections
type ConnectionHealth struct {
	IsHealthy       bool
	LastSuccessTime time.Time
	FailureCount    int
	LatencyMs       int64
	ActiveStreams   int
	ConnectionID    string
	LastError       error
}

// HTTPClient implements the OpenRouterClient interface with enhanced streaming support
type HTTPClient struct {
	client           *http.Client
	streamingClient  *http.Client
	baseURL          string
	requestTimeout   time.Duration
	streamingConfig  *config.StreamingConfig
	connectionHealth *ConnectionHealth
	transport        *http.Transport
	config           *config.Config
	logger           StructuredLogger
}

// NewHTTPClient creates a new HTTP client for OpenRouter API with enhanced streaming support
func NewHTTPClient(cfg *config.Config) *HTTPClient {
	return NewHTTPClientWithStreamingConfig(cfg, cfg.GetStreamingConfig())
}

// NewHTTPClientWithStreamingConfig creates a new HTTP client with custom streaming configuration
func NewHTTPClientWithStreamingConfig(cfg *config.Config, streamingCfg *config.StreamingConfig) *HTTPClient {
	// Create enhanced transport with streaming optimizations
	transport := &http.Transport{
		MaxIdleConns:          streamingCfg.MaxIdleConns,
		MaxIdleConnsPerHost:   streamingCfg.MaxConnsPerHost,
		IdleConnTimeout:       streamingCfg.IdleConnTimeout,
		TLSHandshakeTimeout:   streamingCfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: streamingCfg.ReadTimeout,
		DisableCompression:    false,
		DisableKeepAlives:     false,
		MaxConnsPerHost:       streamingCfg.MaxConnsPerHost,

		// Enable HTTP/2 if configured
		ForceAttemptHTTP2: streamingCfg.HTTP2Enabled,

		// Connection reuse optimizations
		WriteBufferSize: 32 * 1024, // 32KB write buffer
		ReadBufferSize:  32 * 1024, // 32KB read buffer
	}

	// Client for regular requests with timeout
	client := &http.Client{
		Timeout:   streamingCfg.ConnectionTimeout,
		Transport: transport,
	}

	// Client for streaming requests with longer timeouts
	streamingClient := &http.Client{
		Transport: transport,
		Timeout:   0, // No timeout for streaming to allow long-running streams
	}

	// Initialize connection health
	connectionHealth := &ConnectionHealth{
		IsHealthy:       true,
		LastSuccessTime: time.Now(),
		FailureCount:    0,
		LatencyMs:       0,
		ActiveStreams:   0,
		ConnectionID:    generateConnectionID(),
	}

	return &HTTPClient{
		client:           client,
		streamingClient:  streamingClient,
		baseURL:          strings.TrimSuffix(cfg.OpenRouterBaseURL, "/"),
		requestTimeout:   streamingCfg.ConnectionTimeout,
		streamingConfig:  streamingCfg,
		connectionHealth: connectionHealth,
		transport:        transport,
		config:           cfg,
		logger:           NewStructuredLogger(nil),
	}
}

// SendRequest sends a non-streaming request to OpenRouter API
func (c *HTTPClient) SendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	// Ensure streaming is disabled for this method
	req.Stream = false

	return c.sendRequest(ctx, req, apiKey, false)
}

// logDebugRequest logs HTTP request details when log level is debug
func (c *HTTPClient) logDebugRequest(httpReq *http.Request, reqBody []byte, requestID string) {
	if strings.ToLower(c.config.LogLevel) != "debug" {
		return
	}

	logEntry := map[string]interface{}{
		"timestamp":    time.Now().Format(time.RFC3339),
		"level":        "debug",
		"component":    "http_client",
		"event":        "http_request",
		"request_id":   requestID,
		"method":       httpReq.Method,
		"url":          httpReq.URL.String(),
		"content_type": httpReq.Header.Get("Content-Type"),
	}

	// Add headers (excluding sensitive ones)
	headers := make(map[string]string)
	for name, values := range httpReq.Header {
		if !isSensitiveHeader(name) {
			headers[name] = strings.Join(values, ", ")
		} else {
			headers[name] = "[REDACTED]"
		}
	}
	logEntry["headers"] = headers

	// Add request body for smaller requests
	if len(reqBody) > 0 && len(reqBody) < 10240 { // Only log bodies smaller than 10KB
		var jsonBody interface{}
		if err := json.Unmarshal(reqBody, &jsonBody); err == nil {
			logEntry["request_body"] = jsonBody
		} else {
			logEntry["request_body"] = string(reqBody)
		}
	} else if len(reqBody) >= 10240 {
		logEntry["request_body"] = fmt.Sprintf("[LARGE_BODY: %d bytes]", len(reqBody))
	}

	if c.logger != nil {
		c.logger.Log(LogLevelDebug, "http_client", "sending HTTP request", logEntry)
	}
}

// logDebugResponse logs HTTP response details when log level is debug
func (c *HTTPClient) logDebugResponse(resp *http.Response, responseBody []byte, requestID string, duration time.Duration) {
	if strings.ToLower(c.config.LogLevel) != "debug" {
		return
	}

	logEntry := map[string]interface{}{
		"timestamp":     time.Now().Format(time.RFC3339),
		"level":         "debug",
		"component":     "http_client",
		"event":         "http_response",
		"request_id":    requestID,
		"status_code":   resp.StatusCode,
		"status":        resp.Status,
		"duration_ms":   duration.Milliseconds(),
		"content_type":  resp.Header.Get("Content-Type"),
		"response_size": len(responseBody),
	}

	// Add response headers (excluding sensitive ones)
	headers := make(map[string]string)
	for name, values := range resp.Header {
		if !isSensitiveHeader(name) {
			headers[name] = strings.Join(values, ", ")
		} else {
			headers[name] = "[REDACTED]"
		}
	}
	logEntry["headers"] = headers

	// Add response body for smaller responses and non-streaming
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") && len(responseBody) > 0 && len(responseBody) < 10240 {
		var jsonBody interface{}
		if err := json.Unmarshal(responseBody, &jsonBody); err == nil {
			logEntry["response_body"] = jsonBody
		} else {
			logEntry["response_body"] = string(responseBody)
		}
	} else if len(responseBody) >= 10240 {
		logEntry["response_body"] = fmt.Sprintf("[LARGE_RESPONSE: %d bytes]", len(responseBody))
	} else if strings.Contains(contentType, "text/event-stream") {
		logEntry["response_body"] = "[STREAMING_RESPONSE]"
	}

	if c.logger != nil {
		c.logger.Log(LogLevelDebug, "http_client", "received HTTP response", logEntry)
	}
}

// isSensitiveHeader checks if a header contains sensitive information
func isSensitiveHeader(header string) bool {
	sensitiveHeaders := []string{
		"authorization",
		"x-api-key",
		"x-auth-token",
		"cookie",
		"set-cookie",
		"x-access-token",
		"x-refresh-token",
	}

	headerLower := strings.ToLower(header)
	for _, sensitive := range sensitiveHeaders {
		if headerLower == sensitive {
			return true
		}
	}
	return false
}

// SendStreamingRequest sends a streaming request to OpenRouter API with enhanced connection management
func (c *HTTPClient) SendStreamingRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	// Ensure streaming is enabled for this method
	req.Stream = true

	// Track active stream
	c.incrementActiveStreams()

	// Record start time for latency measurement
	startTime := time.Now()

	resp, err := c.sendRequest(ctx, req, apiKey, true)

	// Update connection health based on result
	latency := time.Since(startTime)
	c.updateConnectionHealth(err == nil, latency, err)

	if err != nil {
		c.decrementActiveStreams()
		return nil, err
	}

	// Wrap response to track when stream completes
	return c.wrapStreamingResponse(resp), nil
}

// sendRequest is the internal method that handles both streaming and non-streaming requests
func (c *HTTPClient) sendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string, isStreaming bool) (*http.Response, error) {
	// Validate the request
	if err := req.Validate(); err != nil {
		return nil, &models.APIError{
			Code:    http.StatusBadRequest,
			Message: fmt.Sprintf("invalid request: %s", err.Error()),
			Type:    "invalid_request_error",
		}
	}

	// Validate API key
	if err := c.validateAPIKey(apiKey); err != nil {
		return nil, err
	}

	// Build request body with provider configuration
	reqBody, err := c.buildRequestBody(req)
	if err != nil {
		return nil, &models.APIError{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to build request body: %s", err.Error()),
			Type:    "internal_error",
		}
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/chat/completions", c.baseURL)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, &models.APIError{
			Code:    http.StatusInternalServerError,
			Message: fmt.Sprintf("failed to create HTTP request: %s", err.Error()),
			Type:    "internal_error",
		}
	}

	// Set headers
	c.setRequestHeaders(httpReq, apiKey, isStreaming)

	// Generate request ID for debug logging
	requestID := generateRequestID()

	// Log request details for debug
	c.logDebugRequest(httpReq, reqBody, requestID)

	// Use appropriate client based on streaming
	client := c.client
	if isStreaming {
		client = c.streamingClient
	}

	// Send the request with timing
	start := time.Now()
	resp, err := client.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		return nil, c.handleRequestError(err)
	}

	// For non-streaming responses, read and log the response body
	if !isStreaming && strings.ToLower(c.config.LogLevel) == "debug" {
		// Read response body for logging
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr == nil {
			// Log response details
			c.logDebugResponse(resp, responseBody, requestID, duration)

			// Replace the body with a new reader
			resp.Body = io.NopCloser(bytes.NewReader(responseBody))
		}
	} else if isStreaming {
		// For streaming responses, just log headers and status
		c.logDebugResponse(resp, nil, requestID, duration)
	}

	// Handle error responses
	if resp.StatusCode >= 400 {
		return resp, c.handleErrorResponse(resp)
	}

	return resp, nil
}

// buildRequestBody builds the request body with optional provider configuration
func (c *HTTPClient) buildRequestBody(req *models.OpenAIRequest) ([]byte, error) {
	// Set provider configuration if available and not already set
	if c.config != nil && len(c.config.ProviderOrder) > 0 && req.Provider == nil {
		req.Provider = &models.ProviderConfig{
			Order: c.config.ProviderOrder,
		}
	}

	return json.Marshal(req)
}

// setRequestHeaders sets the appropriate headers for the request
func (c *HTTPClient) setRequestHeaders(req *http.Request, apiKey string, isStreaming bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("User-Agent", "anthropic-openai-gateway/1.0")

	// OpenRouter-specific headers for rankings and identification
	req.Header.Set("HTTP-Referer", "https://github.com/mynameismaxz/y-router-go")
	req.Header.Set("X-Title", "Y-Router-Go")

	if isStreaming {
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Cache-Control", "no-cache")
	} else {
		req.Header.Set("Accept", "application/json")
	}
}

// validateAPIKey validates the API key
func (c *HTTPClient) validateAPIKey(apiKey string) error {
	if apiKey == "" {
		return &models.APIError{
			Code:    http.StatusUnauthorized,
			Message: "x-api-key header is required",
			Type:    "authentication_error",
		}
	}

	if strings.TrimSpace(apiKey) == "" {
		return &models.APIError{
			Code:    http.StatusUnauthorized,
			Message: "x-api-key cannot be empty",
			Type:    "authentication_error",
		}
	}

	return nil
}

// handleRequestError handles errors that occur during the HTTP request
func (c *HTTPClient) handleRequestError(err error) error {
	if err == nil {
		return nil
	}

	// Check for context cancellation
	if ctx := context.Background(); ctx.Err() != nil {
		return &models.APIError{
			Code:    http.StatusRequestTimeout,
			Message: "request cancelled or timed out",
			Type:    "timeout_error",
		}
	}

	// Check for timeout errors
	if strings.Contains(err.Error(), "timeout") {
		return &models.APIError{
			Code:    http.StatusRequestTimeout,
			Message: "request timed out",
			Type:    "timeout_error",
		}
	}

	// Check for connection errors
	if strings.Contains(err.Error(), "connection") {
		return &models.APIError{
			Code:    http.StatusBadGateway,
			Message: "failed to connect to upstream service",
			Type:    "connection_error",
		}
	}

	// Generic network error
	return &models.APIError{
		Code:    http.StatusBadGateway,
		Message: fmt.Sprintf("network error: %s", err.Error()),
		Type:    "network_error",
	}
}

// handleErrorResponse handles error responses from the upstream API
func (c *HTTPClient) handleErrorResponse(resp *http.Response) error {
	defer resp.Body.Close()

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &models.APIError{
			Code:    resp.StatusCode,
			Message: "failed to read error response",
			Type:    "upstream_error",
		}
	}

	// Try to parse as OpenAI error format
	var openAIError models.OpenAIError
	if err := json.Unmarshal(body, &openAIError); err == nil && openAIError.Error.Message != "" {
		return &models.APIError{
			Code:    resp.StatusCode,
			Message: openAIError.Error.Message,
			Type:    openAIError.Error.Type,
		}
	}

	// Try to parse as generic error
	var genericError map[string]interface{}
	if err := json.Unmarshal(body, &genericError); err == nil {
		if message, exists := genericError["message"]; exists {
			if msgStr, ok := message.(string); ok {
				errorType := "upstream_error"
				if typeVal, typeExists := genericError["type"]; typeExists {
					if typeStr, ok := typeVal.(string); ok {
						errorType = typeStr
					}
				}

				return &models.APIError{
					Code:    resp.StatusCode,
					Message: msgStr,
					Type:    errorType,
				}
			}
		}
	}

	// Fallback to generic error with status code
	statusText := http.StatusText(resp.StatusCode)
	if statusText == "" {
		statusText = "Unknown Error"
	}

	return &models.APIError{
		Code:    resp.StatusCode,
		Message: fmt.Sprintf("upstream API error: %s", statusText),
		Type:    "upstream_error",
	}
}

// RetryableHTTPClient wraps HTTPClient with retry logic
type RetryableHTTPClient struct {
	client      *HTTPClient
	maxRetries  int
	retryDelay  time.Duration
	backoffFunc func(attempt int) time.Duration
}

// NewRetryableHTTPClient creates a new HTTP client with retry capabilities
func NewRetryableHTTPClient(cfg *config.Config, maxRetries int) *RetryableHTTPClient {
	return &RetryableHTTPClient{
		client:     NewHTTPClient(cfg),
		maxRetries: maxRetries,
		retryDelay: 1 * time.Second,
		backoffFunc: func(attempt int) time.Duration {
			// Exponential backoff: 1s, 2s, 4s, 8s, etc.
			return time.Duration(1<<uint(attempt)) * time.Second
		},
	}
}

// SendRequest sends a request with retry logic for non-streaming requests
func (rc *RetryableHTTPClient) SendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= rc.maxRetries; attempt++ {
		resp, err := rc.client.SendRequest(ctx, req, apiKey)

		// If successful or non-retryable error, return immediately
		if err == nil || !rc.isRetryableError(err) {
			return resp, err
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < rc.maxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(rc.backoffFunc(attempt)):
				// Continue to next attempt
			}
		}
	}

	return nil, lastErr
}

// SendStreamingRequest sends a streaming request (no retry for streaming)
func (rc *RetryableHTTPClient) SendStreamingRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	// No retry for streaming requests as they are stateful
	return rc.client.SendStreamingRequest(ctx, req, apiKey)
}

// isRetryableError determines if an error is retryable
func (rc *RetryableHTTPClient) isRetryableError(err error) bool {
	if apiErr, ok := err.(*models.APIError); ok {
		switch apiErr.Code {
		case http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		case http.StatusTooManyRequests:
			return true // Rate limiting is retryable
		default:
			return false
		}
	}

	// Network errors are generally retryable
	if strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "connection") {
		return true
	}

	return false
}

// ClientConfig holds configuration for HTTP client behavior
type ClientConfig struct {
	RequestTimeout      time.Duration
	MaxRetries          int
	RetryDelay          time.Duration
	MaxIdleConns        int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration
	TLSHandshakeTimeout time.Duration
}

// DefaultClientConfig returns default configuration for HTTP client
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		RequestTimeout:      60 * time.Second,
		MaxRetries:          3,
		RetryDelay:          1 * time.Second,
		MaxIdleConns:        100,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

// NewHTTPClientWithConfig creates a new HTTP client with custom configuration
func NewHTTPClientWithConfig(cfg *config.Config, clientCfg *ClientConfig) *HTTPClient {
	// Create shared transport with custom configuration
	transport := &http.Transport{
		MaxIdleConns:          clientCfg.MaxIdleConns,
		MaxIdleConnsPerHost:   clientCfg.MaxConnsPerHost,
		IdleConnTimeout:       clientCfg.IdleConnTimeout,
		TLSHandshakeTimeout:   clientCfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    false,
	}

	// Client for regular requests with timeout
	client := &http.Client{
		Timeout:   clientCfg.RequestTimeout,
		Transport: transport,
	}

	// Client for streaming requests without timeout
	streamingClient := &http.Client{
		Transport: transport,
		// No timeout for streaming requests
	}

	return &HTTPClient{
		client:          client,
		streamingClient: streamingClient,
		baseURL:         strings.TrimSuffix(cfg.OpenRouterBaseURL, "/"),
		requestTimeout:  clientCfg.RequestTimeout,
		config:          cfg,
	}
}

// SetRequestTimeout updates the request timeout for non-streaming requests
func (c *HTTPClient) SetRequestTimeout(timeout time.Duration) {
	c.requestTimeout = timeout
	c.client.Timeout = timeout
}

// GetRequestTimeout returns the current request timeout
func (c *HTTPClient) GetRequestTimeout() time.Duration {
	return c.requestTimeout
}

// SendRequestWithTimeout sends a request with a custom timeout
func (c *HTTPClient) SendRequestWithTimeout(ctx context.Context, req *models.OpenAIRequest, apiKey string, timeout time.Duration) (*http.Response, error) {
	// Create a context with the specified timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return c.SendRequest(timeoutCtx, req, apiKey)
}

// HealthCheck performs a health check against the OpenRouter API
func (c *HTTPClient) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/models", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	req.Header.Set("User-Agent", "anthropic-openai-gateway/1.0")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check failed with status %d", resp.StatusCode)
	}

	return nil
}

// ErrorClassifier provides methods to classify different types of errors
type ErrorClassifier struct{}

// NewErrorClassifier creates a new error classifier
func NewErrorClassifier() *ErrorClassifier {
	return &ErrorClassifier{}
}

// IsRetryableError determines if an error should be retried
func (ec *ErrorClassifier) IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if apiErr, ok := err.(*models.APIError); ok {
		return ec.isRetryableStatusCode(apiErr.Code)
	}

	// Check error message for retryable conditions
	errMsg := strings.ToLower(err.Error())
	retryablePatterns := []string{
		"timeout",
		"connection",
		"network",
		"temporary",
		"unavailable",
		"reset",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(errMsg, pattern) {
			return true
		}
	}

	return false
}

// isRetryableStatusCode determines if an HTTP status code indicates a retryable error
func (ec *ErrorClassifier) isRetryableStatusCode(statusCode int) bool {
	switch statusCode {
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		http.StatusTooManyRequests:
		return true
	default:
		return false
	}
}

// IsAuthenticationError checks if an error is related to authentication
func (ec *ErrorClassifier) IsAuthenticationError(err error) bool {
	if apiErr, ok := err.(*models.APIError); ok {
		return apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden
	}
	return false
}

// IsRateLimitError checks if an error is due to rate limiting
func (ec *ErrorClassifier) IsRateLimitError(err error) bool {
	if apiErr, ok := err.(*models.APIError); ok {
		return apiErr.Code == http.StatusTooManyRequests
	}
	return false
}

// IsTimeoutError checks if an error is due to timeout
func (ec *ErrorClassifier) IsTimeoutError(err error) bool {
	if apiErr, ok := err.(*models.APIError); ok {
		return apiErr.Code == http.StatusRequestTimeout || apiErr.Type == "timeout_error"
	}

	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded")
}

// Note: CircuitBreaker implementation moved to circuit_breaker.go for enhanced functionality

// generateConnectionID generates a unique connection identifier
func generateConnectionID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// generateRequestID generates a unique request identifier
func generateRequestID() string {
	return fmt.Sprintf("req_%d_%s", time.Now().UnixNano(), generateConnectionID()[:8])
}

// Connection management methods for HTTPClient

var (
	activeStreamsMutex sync.RWMutex
	healthMutex        sync.RWMutex
)

// incrementActiveStreams safely increments the active streams counter
func (c *HTTPClient) incrementActiveStreams() {
	activeStreamsMutex.Lock()
	defer activeStreamsMutex.Unlock()
	c.connectionHealth.ActiveStreams++
}

// decrementActiveStreams safely decrements the active streams counter
func (c *HTTPClient) decrementActiveStreams() {
	activeStreamsMutex.Lock()
	defer activeStreamsMutex.Unlock()
	if c.connectionHealth.ActiveStreams > 0 {
		c.connectionHealth.ActiveStreams--
	}
}

// updateConnectionHealth updates the connection health metrics
func (c *HTTPClient) updateConnectionHealth(success bool, latency time.Duration, err error) {
	healthMutex.Lock()
	defer healthMutex.Unlock()

	c.connectionHealth.LatencyMs = latency.Milliseconds()

	if success {
		c.connectionHealth.IsHealthy = true
		c.connectionHealth.LastSuccessTime = time.Now()
		c.connectionHealth.FailureCount = 0
		c.connectionHealth.LastError = nil
	} else {
		c.connectionHealth.FailureCount++
		c.connectionHealth.LastError = err

		// Mark as unhealthy after 3 consecutive failures
		if c.connectionHealth.FailureCount >= 3 {
			c.connectionHealth.IsHealthy = false
		}
	}
}

// GetConnectionHealth returns the current connection health status
func (c *HTTPClient) GetConnectionHealth() *ConnectionHealth {
	healthMutex.RLock()
	defer healthMutex.RUnlock()

	// Return a copy to avoid race conditions
	return &ConnectionHealth{
		IsHealthy:       c.connectionHealth.IsHealthy,
		LastSuccessTime: c.connectionHealth.LastSuccessTime,
		FailureCount:    c.connectionHealth.FailureCount,
		LatencyMs:       c.connectionHealth.LatencyMs,
		ActiveStreams:   c.connectionHealth.ActiveStreams,
		ConnectionID:    c.connectionHealth.ConnectionID,
		LastError:       c.connectionHealth.LastError,
	}
}

// UpdateStreamingConfig updates the streaming configuration
func (c *HTTPClient) UpdateStreamingConfig(config *config.StreamingConfig) {
	c.streamingConfig = config

	// Update transport settings
	c.transport.MaxIdleConns = config.MaxIdleConns
	c.transport.MaxIdleConnsPerHost = config.MaxConnsPerHost
	c.transport.IdleConnTimeout = config.IdleConnTimeout
	c.transport.TLSHandshakeTimeout = config.TLSHandshakeTimeout
	c.transport.ResponseHeaderTimeout = config.ReadTimeout
	c.transport.ForceAttemptHTTP2 = config.HTTP2Enabled

	// Update client timeouts
	c.client.Timeout = config.ConnectionTimeout
	c.requestTimeout = config.ConnectionTimeout
}

// GetStreamingConfig returns the current streaming configuration
func (c *HTTPClient) GetStreamingConfig() *config.StreamingConfig {
	return c.streamingConfig
}

// streamingResponseWrapper wraps an HTTP response to track stream completion
type streamingResponseWrapper struct {
	*http.Response
	client *HTTPClient
}

// wrapStreamingResponse wraps a response to track when the stream is closed
func (c *HTTPClient) wrapStreamingResponse(resp *http.Response) *http.Response {
	wrapper := &streamingResponseWrapper{
		Response: resp,
		client:   c,
	}

	// Replace the body with a wrapper that tracks closure
	wrapper.Body = &streamingBodyWrapper{
		ReadCloser: resp.Body,
		client:     c,
	}

	return wrapper.Response
}

// streamingBodyWrapper wraps the response body to track when it's closed
type streamingBodyWrapper struct {
	io.ReadCloser
	client *HTTPClient
	closed bool
}

// Close decrements the active streams counter when the body is closed
func (w *streamingBodyWrapper) Close() error {
	if !w.closed {
		w.client.decrementActiveStreams()
		w.closed = true
	}
	return w.ReadCloser.Close()
}

// IsConnectionHealthy checks if the connection is currently healthy
func (c *HTTPClient) IsConnectionHealthy() bool {
	health := c.GetConnectionHealth()
	return health.IsHealthy && health.FailureCount < 3
}

// ResetConnectionHealth resets the connection health metrics
func (c *HTTPClient) ResetConnectionHealth() {
	healthMutex.Lock()
	defer healthMutex.Unlock()

	c.connectionHealth.IsHealthy = true
	c.connectionHealth.FailureCount = 0
	c.connectionHealth.LastError = nil
	c.connectionHealth.ConnectionID = generateConnectionID()
}

// ConnectionHealthMonitor provides periodic health monitoring for HTTP connections
type ConnectionHealthMonitor struct {
	client          *HTTPClient
	checkInterval   time.Duration
	healthThreshold time.Duration
	stopChan        chan struct{}
	running         bool
	mutex           sync.RWMutex
}

// NewConnectionHealthMonitor creates a new connection health monitor
func NewConnectionHealthMonitor(client *HTTPClient, checkInterval time.Duration) *ConnectionHealthMonitor {
	return &ConnectionHealthMonitor{
		client:          client,
		checkInterval:   checkInterval,
		healthThreshold: 5 * time.Minute, // Consider connection stale after 5 minutes of inactivity
		stopChan:        make(chan struct{}),
		running:         false,
	}
}

// Start begins periodic health monitoring
func (m *ConnectionHealthMonitor) Start(ctx context.Context) {
	m.mutex.Lock()
	if m.running {
		m.mutex.Unlock()
		return
	}
	m.running = true
	m.mutex.Unlock()

	go m.monitorLoop(ctx)
}

// Stop stops the health monitoring
func (m *ConnectionHealthMonitor) Stop() {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if !m.running {
		return
	}

	m.running = false
	close(m.stopChan)
}

// monitorLoop runs the periodic health check loop
func (m *ConnectionHealthMonitor) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(m.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopChan:
			return
		case <-ticker.C:
			m.performHealthCheck(ctx)
		}
	}
}

// performHealthCheck executes a health check and updates connection status
func (m *ConnectionHealthMonitor) performHealthCheck(ctx context.Context) {
	health := m.client.GetConnectionHealth()

	// Check if connection has been inactive for too long
	if time.Since(health.LastSuccessTime) > m.healthThreshold {
		// Perform a lightweight health check
		if err := m.client.HealthCheck(ctx); err != nil {
			m.client.updateConnectionHealth(false, 0, err)

			// Attempt connection recovery
			m.attemptConnectionRecovery(ctx)
		} else {
			m.client.updateConnectionHealth(true, 0, nil)
		}
	}

	// Check for excessive failure rate
	if health.FailureCount >= 5 {
		m.attemptConnectionRecovery(ctx)
	}
}

// attemptConnectionRecovery attempts to recover from connection issues
func (m *ConnectionHealthMonitor) attemptConnectionRecovery(ctx context.Context) {
	// Reset connection health to trigger fresh connections
	m.client.ResetConnectionHealth()

	// Force close idle connections to establish fresh ones
	m.client.transport.CloseIdleConnections()

	// Perform a test request to verify recovery
	testCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if err := m.client.HealthCheck(testCtx); err == nil {
		m.client.updateConnectionHealth(true, 0, nil)
	}
}

// IsRunning returns whether the monitor is currently running
func (m *ConnectionHealthMonitor) IsRunning() bool {
	m.mutex.RLock()
	defer m.mutex.RUnlock()
	return m.running
}

// ConnectionFailureDetector detects and classifies connection failures
type ConnectionFailureDetector struct {
	failurePatterns map[string][]string
	recoveryActions map[string]func(*HTTPClient) error
}

// NewConnectionFailureDetector creates a new failure detector
func NewConnectionFailureDetector() *ConnectionFailureDetector {
	detector := &ConnectionFailureDetector{
		failurePatterns: make(map[string][]string),
		recoveryActions: make(map[string]func(*HTTPClient) error),
	}

	// Define failure patterns
	detector.failurePatterns["connection_reset"] = []string{
		"connection reset by peer",
		"broken pipe",
		"connection refused",
	}

	detector.failurePatterns["timeout"] = []string{
		"timeout",
		"deadline exceeded",
		"context deadline exceeded",
	}

	detector.failurePatterns["http2_error"] = []string{
		"http2:",
		"stream error",
		"connection error",
		"protocol error",
	}

	detector.failurePatterns["tls_error"] = []string{
		"tls:",
		"certificate",
		"handshake",
	}

	// Define recovery actions
	detector.recoveryActions["connection_reset"] = func(client *HTTPClient) error {
		client.transport.CloseIdleConnections()
		client.ResetConnectionHealth()
		return nil
	}

	detector.recoveryActions["timeout"] = func(client *HTTPClient) error {
		// Increase timeout temporarily
		config := client.GetStreamingConfig()
		config.ConnectionTimeout = config.ConnectionTimeout * 2
		client.UpdateStreamingConfig(config)
		return nil
	}

	detector.recoveryActions["http2_error"] = func(client *HTTPClient) error {
		// Disable HTTP/2 temporarily and fall back to HTTP/1.1
		config := client.GetStreamingConfig()
		config.HTTP2Enabled = false
		client.UpdateStreamingConfig(config)
		client.transport.CloseIdleConnections()
		return nil
	}

	detector.recoveryActions["tls_error"] = func(client *HTTPClient) error {
		client.transport.CloseIdleConnections()
		client.ResetConnectionHealth()
		return nil
	}

	return detector
}

// DetectFailureType analyzes an error and returns the failure type
func (d *ConnectionFailureDetector) DetectFailureType(err error) string {
	if err == nil {
		return ""
	}

	errMsg := strings.ToLower(err.Error())

	for failureType, patterns := range d.failurePatterns {
		for _, pattern := range patterns {
			if strings.Contains(errMsg, pattern) {
				return failureType
			}
		}
	}

	return "unknown"
}

// AttemptRecovery attempts to recover from a detected failure
func (d *ConnectionFailureDetector) AttemptRecovery(client *HTTPClient, failureType string) error {
	if action, exists := d.recoveryActions[failureType]; exists {
		return action(client)
	}

	// Default recovery action
	client.ResetConnectionHealth()
	return nil
}

// EnhancedHTTPClient wraps HTTPClient with health monitoring and failure detection
type EnhancedHTTPClient struct {
	*HTTPClient
	healthMonitor   *ConnectionHealthMonitor
	failureDetector *ConnectionFailureDetector
}

// NewEnhancedHTTPClient creates a new enhanced HTTP client with monitoring
func NewEnhancedHTTPClient(cfg *config.Config, streamingCfg *config.StreamingConfig) *EnhancedHTTPClient {
	client := NewHTTPClientWithStreamingConfig(cfg, streamingCfg)

	healthMonitor := NewConnectionHealthMonitor(client, 30*time.Second)
	failureDetector := NewConnectionFailureDetector()

	return &EnhancedHTTPClient{
		HTTPClient:      client,
		healthMonitor:   healthMonitor,
		failureDetector: failureDetector,
	}
}

// StartHealthMonitoring starts the connection health monitoring
func (e *EnhancedHTTPClient) StartHealthMonitoring(ctx context.Context) {
	e.healthMonitor.Start(ctx)
}

// StopHealthMonitoring stops the connection health monitoring
func (e *EnhancedHTTPClient) StopHealthMonitoring() {
	e.healthMonitor.Stop()
}

// SendStreamingRequestWithRecovery sends a streaming request with automatic failure recovery
func (e *EnhancedHTTPClient) SendStreamingRequestWithRecovery(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	resp, err := e.SendStreamingRequest(ctx, req, apiKey)

	if err != nil {
		// Detect failure type and attempt recovery
		failureType := e.failureDetector.DetectFailureType(err)
		if failureType != "" && failureType != "unknown" {
			if recoveryErr := e.failureDetector.AttemptRecovery(e.HTTPClient, failureType); recoveryErr == nil {
				// Retry once after recovery
				return e.SendStreamingRequest(ctx, req, apiKey)
			}
		}
	}

	return resp, err
}
