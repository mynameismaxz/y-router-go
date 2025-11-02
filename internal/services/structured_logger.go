package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
)

// LogLevel represents the severity level of a log entry
type LogLevel string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

// StreamingContext represents the context for a streaming operation
type StreamingContext struct {
	StreamID      string    `json:"stream_id"`
	RequestID     string    `json:"request_id"`
	CorrelationID string    `json:"correlation_id"`
	ClientIP      string    `json:"client_ip"`
	Model         string    `json:"model"`
	StartTime     time.Time `json:"start_time"`
	UserAgent     string    `json:"user_agent,omitempty"`
}

// LogEntry represents a structured log entry
type LogEntry struct {
	Timestamp     time.Time              `json:"timestamp"`
	Level         LogLevel               `json:"level"`
	Message       string                 `json:"message"`
	Component     string                 `json:"component"`
	StreamContext *StreamingContext      `json:"stream_context,omitempty"`
	Fields        map[string]interface{} `json:"fields,omitempty"`
	Error         string                 `json:"error,omitempty"`
	Duration      *time.Duration         `json:"duration,omitempty"`
}

// StructuredLogger provides structured logging capabilities for streaming operations
type StructuredLogger interface {
	// Stream lifecycle logging
	LogStreamStart(ctx *StreamingContext) error
	LogStreamProgress(ctx *StreamingContext, message string, fields map[string]interface{}) error
	LogStreamComplete(ctx *StreamingContext, duration time.Duration) error
	LogStreamError(ctx *StreamingContext, err error, fields map[string]interface{}) error

	// Connection logging
	LogConnectionEvent(ctx *StreamingContext, event string, fields map[string]interface{}) error
	LogConnectionHealth(connectionID string, isHealthy bool, metrics map[string]interface{}) error

	// Error context logging
	LogErrorContext(ctx *StreamingContext, errorType string, err error, context map[string]interface{}) error

	// General structured logging
	Log(level LogLevel, component string, message string, fields map[string]interface{}) error
	LogWithContext(level LogLevel, component string, message string, ctx *StreamingContext, fields map[string]interface{}) error

	// Correlation ID management
	GenerateCorrelationID() string
	CreateStreamContext(requestID, clientIP, model string) *StreamingContext
}

// DefaultStructuredLogger implements the StructuredLogger interface
type DefaultStructuredLogger struct {
	output io.Writer
}

// NewStructuredLogger creates a new structured logger instance
func NewStructuredLogger(output io.Writer) StructuredLogger {
	if output == nil {
		output = os.Stdout
	}
	return &DefaultStructuredLogger{
		output: output,
	}
}

// GenerateCorrelationID generates a unique correlation ID for tracking requests
func (l *DefaultStructuredLogger) GenerateCorrelationID() string {
	return uuid.New().String()
}

// CreateStreamContext creates a new stream context with generated IDs
func (l *DefaultStructuredLogger) CreateStreamContext(requestID, clientIP, model string) *StreamingContext {
	return &StreamingContext{
		StreamID:      l.GenerateCorrelationID(),
		RequestID:     requestID,
		CorrelationID: l.GenerateCorrelationID(),
		ClientIP:      clientIP,
		Model:         model,
		StartTime:     time.Now(),
	}
}

// LogStreamStart logs the beginning of a streaming operation
func (l *DefaultStructuredLogger) LogStreamStart(ctx *StreamingContext) error {
	return l.LogWithContext(LogLevelInfo, "stream_manager", "stream started", ctx, map[string]interface{}{
		"event": "stream_start",
	})
}

// LogStreamProgress logs progress events during streaming
func (l *DefaultStructuredLogger) LogStreamProgress(ctx *StreamingContext, message string, fields map[string]interface{}) error {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["event"] = "stream_progress"

	return l.LogWithContext(LogLevelInfo, "stream_manager", message, ctx, fields)
}

// LogStreamComplete logs the successful completion of a streaming operation
func (l *DefaultStructuredLogger) LogStreamComplete(ctx *StreamingContext, duration time.Duration) error {
	return l.LogWithContext(LogLevelInfo, "stream_manager", "stream completed successfully", ctx, map[string]interface{}{
		"event":    "stream_complete",
		"duration": duration.String(),
	})
}

// LogStreamError logs errors that occur during streaming operations
func (l *DefaultStructuredLogger) LogStreamError(ctx *StreamingContext, err error, fields map[string]interface{}) error {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["event"] = "stream_error"

	entry := &LogEntry{
		Timestamp:     time.Now(),
		Level:         LogLevelError,
		Message:       "stream error occurred",
		Component:     "stream_manager",
		StreamContext: ctx,
		Fields:        fields,
		Error:         err.Error(),
	}

	return l.writeLogEntry(entry)
}

// LogConnectionEvent logs connection-related events
func (l *DefaultStructuredLogger) LogConnectionEvent(ctx *StreamingContext, event string, fields map[string]interface{}) error {
	if fields == nil {
		fields = make(map[string]interface{})
	}
	fields["connection_event"] = event

	return l.LogWithContext(LogLevelInfo, "connection_manager", fmt.Sprintf("connection event: %s", event), ctx, fields)
}

// LogConnectionHealth logs connection health status
func (l *DefaultStructuredLogger) LogConnectionHealth(connectionID string, isHealthy bool, metrics map[string]interface{}) error {
	fields := map[string]interface{}{
		"connection_id": connectionID,
		"is_healthy":    isHealthy,
		"event":         "connection_health_check",
	}

	// Add metrics to fields
	for k, v := range metrics {
		fields[k] = v
	}

	level := LogLevelInfo
	message := "connection health check: healthy"
	if !isHealthy {
		level = LogLevelWarn
		message = "connection health check: unhealthy"
	}

	return l.Log(level, "connection_health", message, fields)
}

// LogErrorContext logs detailed error context information
func (l *DefaultStructuredLogger) LogErrorContext(ctx *StreamingContext, errorType string, err error, context map[string]interface{}) error {
	fields := map[string]interface{}{
		"error_type": errorType,
		"event":      "error_context",
	}

	// Add context fields
	for k, v := range context {
		fields[k] = v
	}

	entry := &LogEntry{
		Timestamp:     time.Now(),
		Level:         LogLevelError,
		Message:       fmt.Sprintf("error context: %s", errorType),
		Component:     "error_handler",
		StreamContext: ctx,
		Fields:        fields,
		Error:         err.Error(),
	}

	return l.writeLogEntry(entry)
}

// Log provides general structured logging
func (l *DefaultStructuredLogger) Log(level LogLevel, component string, message string, fields map[string]interface{}) error {
	entry := &LogEntry{
		Timestamp: time.Now(),
		Level:     level,
		Message:   message,
		Component: component,
		Fields:    fields,
	}

	return l.writeLogEntry(entry)
}

// LogWithContext logs with stream context information
func (l *DefaultStructuredLogger) LogWithContext(level LogLevel, component string, message string, ctx *StreamingContext, fields map[string]interface{}) error {
	entry := &LogEntry{
		Timestamp:     time.Now(),
		Level:         level,
		Message:       message,
		Component:     component,
		StreamContext: ctx,
		Fields:        fields,
	}

	return l.writeLogEntry(entry)
}

// writeLogEntry writes a log entry to the output in JSON format
func (l *DefaultStructuredLogger) writeLogEntry(entry *LogEntry) error {
	jsonData, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	_, err = l.output.Write(append(jsonData, '\n'))
	if err != nil {
		return fmt.Errorf("failed to write log entry: %w", err)
	}

	return nil
}

// ContextKey is used for context values
type ContextKey string

const (
	// StreamContextKey is the key for stream context in Go context
	StreamContextKey ContextKey = "stream_context"
)

// GetStreamContextFromContext extracts stream context from Go context
func GetStreamContextFromContext(ctx context.Context) *StreamingContext {
	if streamCtx, ok := ctx.Value(StreamContextKey).(*StreamingContext); ok {
		return streamCtx
	}
	return nil
}

// SetStreamContextInContext sets stream context in Go context
func SetStreamContextInContext(ctx context.Context, streamCtx *StreamingContext) context.Context {
	return context.WithValue(ctx, StreamContextKey, streamCtx)
}
