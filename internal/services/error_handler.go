package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// ErrorType represents different categories of streaming errors
type ErrorType string

const (
	ConnectionError ErrorType = "connection_error"
	TimeoutError    ErrorType = "timeout_error"
	ProcessingError ErrorType = "processing_error"
	UpstreamError   ErrorType = "upstream_error"
	BufferError     ErrorType = "buffer_error"
	ContextError    ErrorType = "context_error"
)

// ErrorSeverity represents the severity level of an error
type ErrorSeverity string

const (
	SeverityLow      ErrorSeverity = "low"
	SeverityMedium   ErrorSeverity = "medium"
	SeverityHigh     ErrorSeverity = "high"
	SeverityCritical ErrorSeverity = "critical"
)

// ErrorClassification contains detailed information about a classified error
type ErrorClassification struct {
	Type       ErrorType     `json:"type"`
	Severity   ErrorSeverity `json:"severity"`
	Retryable  bool          `json:"retryable"`
	UserFacing bool          `json:"user_facing"`
	Cause      string        `json:"cause"`
	HTTPStatus int           `json:"http_status"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
}

// Note: StreamContext is defined in stream_manager.go

// StreamErrorHandler defines the interface for streaming-specific error management
type StreamErrorHandler interface {
	HandleConnectionError(err error, context StreamContext) error
	HandleProcessingError(err error, context StreamContext) error
	SendErrorEvent(writer io.Writer, errorType string, message string) error
	ClassifyError(err error) ErrorClassification
	ShouldRetry(classification ErrorClassification, context StreamContext) bool
	GetRetryDelay(classification ErrorClassification, retryCount int) time.Duration
}

// DefaultStreamErrorHandler implements the StreamErrorHandler interface
type DefaultStreamErrorHandler struct {
	maxRetries      int
	baseRetryDelay  time.Duration
	maxRetryDelay   time.Duration
	retryMultiplier float64
	logLevel        string
}

// NewDefaultStreamErrorHandler creates a new DefaultStreamErrorHandler with default configuration
func NewDefaultStreamErrorHandler() *DefaultStreamErrorHandler {
	return &DefaultStreamErrorHandler{
		maxRetries:      3,
		baseRetryDelay:  100 * time.Millisecond,
		maxRetryDelay:   30 * time.Second,
		retryMultiplier: 2.0,
		logLevel:        "info",
	}
}

// NewStreamErrorHandlerWithConfig creates a new DefaultStreamErrorHandler with custom configuration
func NewStreamErrorHandlerWithConfig(maxRetries int, baseRetryDelay, maxRetryDelay time.Duration, retryMultiplier float64, logLevel string) *DefaultStreamErrorHandler {
	return &DefaultStreamErrorHandler{
		maxRetries:      maxRetries,
		baseRetryDelay:  baseRetryDelay,
		maxRetryDelay:   maxRetryDelay,
		retryMultiplier: retryMultiplier,
		logLevel:        logLevel,
	}
}

// ClassifyError analyzes an error and returns detailed classification information
func (h *DefaultStreamErrorHandler) ClassifyError(err error) ErrorClassification {
	if err == nil {
		return ErrorClassification{
			Type:       ProcessingError,
			Severity:   SeverityLow,
			Retryable:  false,
			UserFacing: false,
			Cause:      "no error",
			HTTPStatus: http.StatusOK,
		}
	}

	// Check for context errors first
	if errors.Is(err, context.Canceled) {
		return ErrorClassification{
			Type:       ContextError,
			Severity:   SeverityLow, // Changed from Medium to Low - client cancellation is normal
			Retryable:  false,
			UserFacing: false, // Changed to false - not really a user-facing error
			Cause:      "request cancelled by client",
			HTTPStatus: http.StatusRequestTimeout,
		}
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorClassification{
			Type:       TimeoutError,
			Severity:   SeverityMedium,
			Retryable:  true,
			UserFacing: true,
			Cause:      "request timeout exceeded",
			HTTPStatus: http.StatusRequestTimeout,
			RetryAfter: 5 * time.Second,
		}
	}

	// Check for network-related errors
	if netErr, ok := err.(net.Error); ok {
		if netErr.Timeout() {
			return ErrorClassification{
				Type:       TimeoutError,
				Severity:   SeverityMedium,
				Retryable:  true,
				UserFacing: true,
				Cause:      "network timeout",
				HTTPStatus: http.StatusRequestTimeout,
				RetryAfter: 2 * time.Second,
			}
		}

		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityHigh,
			Retryable:  true,
			UserFacing: true,
			Cause:      "network error: " + netErr.Error(),
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 1 * time.Second,
		}
	}

	// Check for syscall errors (connection refused, broken pipe, etc.)
	if errors.Is(err, syscall.ECONNREFUSED) {
		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityHigh,
			Retryable:  true,
			UserFacing: true,
			Cause:      "connection refused by upstream service",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 5 * time.Second,
		}
	}

	if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityHigh,
			Retryable:  true,
			UserFacing: true,
			Cause:      "connection broken during streaming",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 1 * time.Second,
		}
	}

	// Check for HTTP/2 specific errors and StreamError with context cancellation
	errStr := err.Error()

	// Check for StreamError with context cancellation
	if strings.Contains(errStr, "context_cancelled") || strings.Contains(errStr, "stream processing cancelled") {
		return ErrorClassification{
			Type:       ContextError,
			Severity:   SeverityLow,
			Retryable:  false,
			UserFacing: false,
			Cause:      "request cancelled by client",
			HTTPStatus: http.StatusRequestTimeout,
		}
	}
	if strings.Contains(errStr, "http2:") {
		if strings.Contains(errStr, "stream closed") || strings.Contains(errStr, "response body closed") {
			return ErrorClassification{
				Type:       ConnectionError,
				Severity:   SeverityHigh,
				Retryable:  true,
				UserFacing: true,
				Cause:      "HTTP/2 stream closed unexpectedly",
				HTTPStatus: http.StatusBadGateway,
				RetryAfter: 1 * time.Second,
			}
		}

		if strings.Contains(errStr, "protocol error") {
			return ErrorClassification{
				Type:       ConnectionError,
				Severity:   SeverityHigh,
				Retryable:  false,
				UserFacing: true,
				Cause:      "HTTP/2 protocol error",
				HTTPStatus: http.StatusBadGateway,
			}
		}

		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityMedium,
			Retryable:  true,
			UserFacing: true,
			Cause:      "HTTP/2 connection error",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 2 * time.Second,
		}
	}

	// Check for IO errors
	if errors.Is(err, io.EOF) {
		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityMedium,
			Retryable:  true,
			UserFacing: true,
			Cause:      "unexpected end of stream",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 1 * time.Second,
		}
	}

	if errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrorClassification{
			Type:       ConnectionError,
			Severity:   SeverityMedium,
			Retryable:  true,
			UserFacing: true,
			Cause:      "stream terminated unexpectedly",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 1 * time.Second,
		}
	}

	// Check for JSON parsing errors
	if strings.Contains(errStr, "json") || strings.Contains(errStr, "unmarshal") || strings.Contains(errStr, "invalid character") {
		return ErrorClassification{
			Type:       ProcessingError,
			Severity:   SeverityLow,
			Retryable:  false,
			UserFacing: false,
			Cause:      "JSON parsing error",
			HTTPStatus: http.StatusInternalServerError,
		}
	}

	// Check for buffer-related errors
	if strings.Contains(errStr, "buffer") || strings.Contains(errStr, "capacity") || strings.Contains(errStr, "memory") {
		return ErrorClassification{
			Type:       BufferError,
			Severity:   SeverityHigh,
			Retryable:  false,
			UserFacing: true,
			Cause:      "buffer overflow or memory limit exceeded",
			HTTPStatus: http.StatusInsufficientStorage,
		}
	}

	// Check for upstream service errors
	if strings.Contains(errStr, "upstream") || strings.Contains(errStr, "openrouter") || strings.Contains(errStr, "api") {
		return ErrorClassification{
			Type:       UpstreamError,
			Severity:   SeverityHigh,
			Retryable:  true,
			UserFacing: true,
			Cause:      "upstream service error",
			HTTPStatus: http.StatusBadGateway,
			RetryAfter: 3 * time.Second,
		}
	}

	// Default classification for unknown errors
	return ErrorClassification{
		Type:       ProcessingError,
		Severity:   SeverityMedium,
		Retryable:  false,
		UserFacing: true,
		Cause:      "unknown error: " + err.Error(),
		HTTPStatus: http.StatusInternalServerError,
	}
}

// ShouldRetry determines if an error should be retried based on classification and context
func (h *DefaultStreamErrorHandler) ShouldRetry(classification ErrorClassification, context StreamContext) bool {
	// Don't retry if error is not retryable
	if !classification.Retryable {
		return false
	}

	// Don't retry if max retries exceeded
	if context.RetryCount >= h.maxRetries {
		return false
	}

	// Don't retry context cancellation or critical errors
	if classification.Type == ContextError || classification.Severity == SeverityCritical {
		return false
	}

	// Don't retry processing errors (usually indicate bad data)
	if classification.Type == ProcessingError {
		return false
	}

	return true
}

// GetRetryDelay calculates the delay before the next retry attempt using exponential backoff
func (h *DefaultStreamErrorHandler) GetRetryDelay(classification ErrorClassification, retryCount int) time.Duration {
	// Use classification-specific retry delay if available
	if classification.RetryAfter > 0 {
		return classification.RetryAfter
	}

	// Calculate exponential backoff delay
	delay := float64(h.baseRetryDelay) * (h.retryMultiplier * float64(retryCount))

	// Add jitter (±25% randomization)
	jitter := 0.25
	jitterRange := delay * jitter
	jitteredDelay := delay + (jitterRange * (2*0.5 - 1)) // Simplified jitter calculation

	finalDelay := time.Duration(jitteredDelay)

	// Cap at maximum retry delay
	if finalDelay > h.maxRetryDelay {
		finalDelay = h.maxRetryDelay
	}

	return finalDelay
}

// shouldLog determines if a message should be logged based on the configured log level
func (h *DefaultStreamErrorHandler) shouldLog(severity ErrorSeverity) bool {
	switch h.logLevel {
	case "debug":
		return true // Log everything
	case "info":
		return severity != SeverityLow // Log medium, high, critical
	case "warn":
		return severity == SeverityHigh || severity == SeverityCritical // Log high, critical
	case "error":
		return severity == SeverityCritical // Log only critical
	default:
		return severity != SeverityLow // Default to info level
	}
}

// NewStreamErrorHandlerFromConfig creates a new DefaultStreamErrorHandler from streaming configuration
func NewStreamErrorHandlerFromConfig(streamingConfig interface{}) *DefaultStreamErrorHandler {
	// Try to extract streaming config if available
	if config, ok := streamingConfig.(interface {
		GetLogLevel() string
		GetMaxRetries() int
		GetRetryBackoffBase() time.Duration
		GetRetryBackoffMax() time.Duration
	}); ok {
		return &DefaultStreamErrorHandler{
			maxRetries:      config.GetMaxRetries(),
			baseRetryDelay:  config.GetRetryBackoffBase(),
			maxRetryDelay:   config.GetRetryBackoffMax(),
			retryMultiplier: 2.0,
			logLevel:        config.GetLogLevel(),
		}
	}

	// Fallback to default configuration
	return NewDefaultStreamErrorHandler()
}

// HandleConnectionError handles connection-specific errors with appropriate recovery strategies
func (h *DefaultStreamErrorHandler) HandleConnectionError(err error, streamContext StreamContext) error {
	classification := h.ClassifyError(err)

	// Don't log context cancellation errors as they're normal client disconnections
	if classification.Type == ContextError && (errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "context_cancelled") ||
		strings.Contains(err.Error(), "stream processing cancelled")) {
		// Silently handle client cancellation - this is expected behavior
		return nil
	}

	// Log the error with context (only if log level permits)
	if h.shouldLog(classification.Severity) {
		fmt.Printf("Connection error in stream %s: %s (type: %s, severity: %s, retryable: %t)\n",
			streamContext.StreamID, classification.Cause, classification.Type, classification.Severity, classification.Retryable)
	}

	// For critical connection errors, return immediately
	if classification.Severity == SeverityCritical {
		return fmt.Errorf("critical connection error: %w", err)
	}

	// For retryable errors, provide retry information
	if classification.Retryable && h.ShouldRetry(classification, streamContext) {
		retryDelay := h.GetRetryDelay(classification, streamContext.RetryCount)
		return &RetryableError{
			OriginalError:  err,
			Classification: classification,
			RetryAfter:     retryDelay,
			Context:        streamContext,
		}
	}

	// For non-retryable errors, wrap with classification
	return &ClassifiedError{
		OriginalError:  err,
		Classification: classification,
		Context:        streamContext,
	}
}

// HandleProcessingError handles processing-specific errors during stream conversion
func (h *DefaultStreamErrorHandler) HandleProcessingError(err error, streamContext StreamContext) error {
	classification := h.ClassifyError(err)

	// Don't log context cancellation errors as they're normal client disconnections
	if classification.Type == ContextError && (errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "context_cancelled") ||
		strings.Contains(err.Error(), "stream processing cancelled")) {
		// Silently handle client cancellation - this is expected behavior
		return nil
	}

	// Log the error with context (only if log level permits)
	if h.shouldLog(classification.Severity) {
		fmt.Printf("Processing error in stream %s: %s (type: %s, severity: %s)\n",
			streamContext.StreamID, classification.Cause, classification.Type, classification.Severity)
	}

	// For low-severity processing errors, we might want to continue processing
	if classification.Severity == SeverityLow {
		if classification.UserFacing {
			fmt.Printf("Continuing stream processing despite low-severity error: %s\n", classification.Cause)
		}
		return nil // Continue processing
	}

	// For higher severity processing errors, stop processing
	return &ClassifiedError{
		OriginalError:  err,
		Classification: classification,
		Context:        streamContext,
	}
}

// RetryableError represents an error that can be retried
type RetryableError struct {
	OriginalError  error
	Classification ErrorClassification
	RetryAfter     time.Duration
	Context        StreamContext
}

func (e *RetryableError) Error() string {
	return fmt.Sprintf("retryable error (retry after %v): %s", e.RetryAfter, e.OriginalError.Error())
}

func (e *RetryableError) Unwrap() error {
	return e.OriginalError
}

// ClassifiedError represents an error with classification information
type ClassifiedError struct {
	OriginalError  error
	Classification ErrorClassification
	Context        StreamContext
}

func (e *ClassifiedError) Error() string {
	return fmt.Sprintf("classified error [%s/%s]: %s", e.Classification.Type, e.Classification.Severity, e.OriginalError.Error())
}

func (e *ClassifiedError) Unwrap() error {
	return e.OriginalError
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	var retryableErr *RetryableError
	return errors.As(err, &retryableErr)
}

// GetRetryDelay extracts retry delay from a retryable error
func GetRetryDelay(err error) time.Duration {
	var retryableErr *RetryableError
	if errors.As(err, &retryableErr) {
		return retryableErr.RetryAfter
	}
	return 0
}

// GetErrorClassification extracts classification from a classified error
func GetErrorClassification(err error) *ErrorClassification {
	var classifiedErr *ClassifiedError
	if errors.As(err, &classifiedErr) {
		return &classifiedErr.Classification
	}

	var retryableErr *RetryableError
	if errors.As(err, &retryableErr) {
		return &retryableErr.Classification
	}

	return nil
}

// SendErrorEvent sends a formatted error event to the client in SSE format
func (h *DefaultStreamErrorHandler) SendErrorEvent(writer io.Writer, errorType string, message string) error {
	return h.SendErrorEventWithDetails(writer, errorType, message, nil)
}

// SendErrorEventWithDetails sends a detailed error event to the client in SSE format
func (h *DefaultStreamErrorHandler) SendErrorEventWithDetails(writer io.Writer, errorType string, message string, details map[string]any) error {
	// Create error event data
	errorData := map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errorType,
			"message": message,
		},
	}

	// Add additional details if provided
	for k, v := range details {
		errorData[k] = v
	}

	// Format as SSE event
	return h.writeSSEEvent(writer, "error", errorData)
}

// SendClassifiedErrorEvent sends an error event based on error classification
func (h *DefaultStreamErrorHandler) SendClassifiedErrorEvent(writer io.Writer, classification ErrorClassification, correlationID string) error {
	// Create user-friendly error message based on classification
	userMessage := h.getUserFriendlyMessage(classification)

	// Prepare error details
	details := map[string]any{
		"correlation_id": correlationID,
		"error_code":     string(classification.Type),
		"severity":       string(classification.Severity),
		"retryable":      classification.Retryable,
	}

	// Add retry information if applicable
	if classification.Retryable && classification.RetryAfter > 0 {
		details["retry_after_ms"] = int64(classification.RetryAfter / time.Millisecond)
	}

	return h.SendErrorEventWithDetails(writer, string(classification.Type), userMessage, details)
}

// getUserFriendlyMessage converts technical error classifications to user-friendly messages
func (h *DefaultStreamErrorHandler) getUserFriendlyMessage(classification ErrorClassification) string {
	switch classification.Type {
	case ConnectionError:
		switch classification.Severity {
		case SeverityLow:
			return "Temporary connection issue. The request will be retried automatically."
		case SeverityMedium:
			return "Connection interrupted. Please try your request again."
		case SeverityHigh:
			return "Unable to establish stable connection. Please check your network and try again."
		case SeverityCritical:
			return "Critical connection failure. Please contact support if this persists."
		}

	case TimeoutError:
		if classification.Retryable {
			return "Request timed out. The system will retry automatically."
		}
		return "Request timed out. Please try again with a shorter request or check your connection."

	case ProcessingError:
		switch classification.Severity {
		case SeverityLow:
			return "Minor processing issue encountered. Continuing with request."
		case SeverityMedium:
			return "Unable to process part of the request. Please check your input and try again."
		case SeverityHigh, SeverityCritical:
			return "Processing error occurred. Please modify your request and try again."
		}

	case UpstreamError:
		if classification.Retryable {
			return "Temporary service issue. The request will be retried automatically."
		}
		return "Service temporarily unavailable. Please try again in a few moments."

	case BufferError:
		return "Request too large to process. Please reduce the size of your request and try again."

	case ContextError:
		return "Request was cancelled or timed out."

	default:
		if classification.Retryable {
			return "Temporary issue encountered. The request will be retried automatically."
		}
		return "An error occurred while processing your request. Please try again."
	}

	return "An unexpected error occurred. Please try again."
}

// writeSSEEvent writes a Server-Sent Event to the writer
func (h *DefaultStreamErrorHandler) writeSSEEvent(writer io.Writer, eventType string, data any) error {
	// Marshal data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal SSE event data: %w", err)
	}

	// Format as SSE
	sseEvent := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(jsonData))

	// Write to stream
	_, err = writer.Write([]byte(sseEvent))
	if err != nil {
		return fmt.Errorf("failed to write SSE event: %w", err)
	}

	return nil
}

// StreamingErrorLogger provides structured logging for streaming errors with correlation IDs
type StreamingErrorLogger struct {
	correlationIDGenerator func() string
}

// NewStreamingErrorLogger creates a new StreamingErrorLogger
func NewStreamingErrorLogger() *StreamingErrorLogger {
	return &StreamingErrorLogger{
		correlationIDGenerator: generateCorrelationID,
	}
}

// LogError logs a structured error with correlation ID and context
func (l *StreamingErrorLogger) LogError(err error, context StreamContext, classification *ErrorClassification) string {
	correlationID := l.correlationIDGenerator()

	// Create structured log entry
	logEntry := map[string]any{
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"correlation_id": correlationID,
		"stream_id":      context.StreamID,
		"request_id":     context.RequestID,
		"client_ip":      context.ClientIP,
		"model":          context.Model,
		"connection_id":  context.ConnectionID,
		"retry_count":    context.RetryCount,
		"error_message":  err.Error(),
	}

	// Add classification details if available
	if classification != nil {
		logEntry["error_type"] = string(classification.Type)
		logEntry["error_severity"] = string(classification.Severity)
		logEntry["retryable"] = classification.Retryable
		logEntry["user_facing"] = classification.UserFacing
		logEntry["http_status"] = classification.HTTPStatus
		logEntry["cause"] = classification.Cause

		if classification.RetryAfter > 0 {
			logEntry["retry_after_ms"] = int64(classification.RetryAfter / time.Millisecond)
		}
	}

	// Add metadata if available
	if context.Metadata != nil {
		logEntry["metadata"] = context.Metadata
	}

	// Marshal to JSON for structured logging
	logJSON, err := json.Marshal(logEntry)
	if err != nil {
		// Fallback to simple logging if JSON marshaling fails
		fmt.Printf("ERROR [%s] Stream %s: %s (correlation_id: %s)\n",
			time.Now().Format(time.RFC3339), context.StreamID, err.Error(), correlationID)
		return correlationID
	}

	// Output structured log (in production, this would go to your logging system)
	fmt.Printf("STREAMING_ERROR: %s\n", string(logJSON))

	return correlationID
}

// LogErrorWithClassification logs an error with automatic classification
func (l *StreamingErrorLogger) LogErrorWithClassification(err error, context StreamContext, handler StreamErrorHandler) (string, ErrorClassification) {
	classification := handler.ClassifyError(err)
	correlationID := l.LogError(err, context, &classification)
	return correlationID, classification
}

// generateCorrelationID generates a unique correlation ID for error tracking
func generateCorrelationID() string {
	// Simple correlation ID generation (in production, use UUID or similar)
	return fmt.Sprintf("err_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond()%1000)
}

// ErrorEventSender provides high-level error event sending functionality
type ErrorEventSender struct {
	handler StreamErrorHandler
	logger  *StreamingErrorLogger
}

// NewErrorEventSender creates a new ErrorEventSender
func NewErrorEventSender(handler StreamErrorHandler) *ErrorEventSender {
	return &ErrorEventSender{
		handler: handler,
		logger:  NewStreamingErrorLogger(),
	}
}

// SendErrorWithLogging sends an error event and logs it with correlation ID
func (s *ErrorEventSender) SendErrorWithLogging(writer io.Writer, err error, context StreamContext) error {
	// Log error and get correlation ID and classification
	correlationID, classification := s.logger.LogErrorWithClassification(err, context, s.handler)

	// Send classified error event to client
	return s.handler.(*DefaultStreamErrorHandler).SendClassifiedErrorEvent(writer, classification, correlationID)
}

// SendRetryableErrorEvent sends an error event for retryable errors with retry information
func (s *ErrorEventSender) SendRetryableErrorEvent(writer io.Writer, retryableErr *RetryableError) error {
	correlationID := s.logger.LogError(retryableErr.OriginalError, retryableErr.Context, &retryableErr.Classification)

	// Add retry-specific details
	details := map[string]any{
		"correlation_id": correlationID,
		"error_code":     string(retryableErr.Classification.Type),
		"severity":       string(retryableErr.Classification.Severity),
		"retryable":      true,
		"retry_after_ms": int64(retryableErr.RetryAfter / time.Millisecond),
		"retry_count":    retryableErr.Context.RetryCount,
	}

	userMessage := s.handler.(*DefaultStreamErrorHandler).getUserFriendlyMessage(retryableErr.Classification)

	return s.handler.(*DefaultStreamErrorHandler).SendErrorEventWithDetails(
		writer,
		string(retryableErr.Classification.Type),
		userMessage,
		details,
	)
}
