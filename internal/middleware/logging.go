package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/logger"
)

// LoggingConfig holds configuration for logging middleware
type LoggingConfig struct {
	Format     string
	TimeFormat string
	TimeZone   string
	Output     io.Writer
	LogLevel   string // Add log level to config
}

// DefaultLoggingConfig returns default logging configuration
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Format:     "[${time}] ${status} - ${method} ${path} - ${ip} - ${latency} - ${bytesSent}B\n",
		TimeFormat: "2006-01-02 15:04:05",
		TimeZone:   "UTC",
		Output:     os.Stdout, // Uses stdout as default output
		LogLevel:   "info",    // Default log level
	}
}

// NewLogger creates a new logging middleware with the provided configuration
func NewLogger(config ...LoggingConfig) fiber.Handler {
	cfg := DefaultLoggingConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return logger.New(logger.Config{
		Format:     cfg.Format,
		TimeFormat: cfg.TimeFormat,
		TimeZone:   cfg.TimeZone,
		Output:     cfg.Output,
		// Custom function to handle request logging
		CustomTags: map[string]logger.LogFunc{
			"request_id": func(output logger.Buffer, c *fiber.Ctx, data *logger.Data, extraParam string) (int, error) {
				return output.WriteString(c.Get("X-Request-ID", "unknown"))
			},
		},
	})
}

// RequestIDMiddleware adds a unique request ID to each request
func RequestIDMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Check if request ID already exists in headers
		requestID := c.Get("X-Request-ID")
		if requestID == "" {
			// Generate a simple request ID using timestamp and random component
			requestID = generateRequestID()
		}

		// Set the request ID in context and response header
		c.Set("X-Request-ID", requestID)
		c.Locals("request_id", requestID)

		return c.Next()
	}
}

// generateRequestID creates a simple request ID
func generateRequestID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(6)
}

// randomString generates a random string of specified length
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// DebugRequestResponseLogger creates middleware that logs full request and response details when log level is debug
func DebugRequestResponseLogger(logLevel string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Only log detailed request/response when log level is debug
		if strings.ToLower(logLevel) != "debug" {
			return c.Next()
		}

		start := time.Now()
		requestID := c.Get("X-Request-ID", generateRequestID())

		// Log request details
		logRequestDetails(c, requestID)

		// Process the request
		err := c.Next()

		// Log response details (without capturing body for now due to Fiber limitations)
		logResponseDetails(c, requestID, nil, time.Since(start))

		return err
	}
}

// logRequestDetails logs detailed request information for debug level
func logRequestDetails(c *fiber.Ctx, requestID string) {
	logEntry := map[string]interface{}{
		"timestamp":    time.Now().Format(time.RFC3339),
		"level":        "debug",
		"component":    "request_logger",
		"event":        "request_received",
		"request_id":   requestID,
		"method":       c.Method(),
		"path":         c.Path(),
		"query":        c.Context().QueryArgs().String(),
		"ip":           c.IP(),
		"user_agent":   c.Get("User-Agent"),
		"content_type": c.Get("Content-Type"),
		"headers":      extractHeaders(c),
	}

	// Log request body for POST/PUT requests (be careful with large payloads)
	if c.Method() == "POST" || c.Method() == "PUT" || c.Method() == "PATCH" {
		body := c.Body()
		if len(body) > 0 && len(body) < 10240 { // Only log bodies smaller than 10KB
			// Try to parse as JSON for better formatting
			var jsonBody interface{}
			if err := json.Unmarshal(body, &jsonBody); err == nil {
				logEntry["request_body"] = jsonBody
			} else {
				logEntry["request_body"] = string(body)
			}
		} else if len(body) >= 10240 {
			logEntry["request_body"] = fmt.Sprintf("[LARGE_BODY: %d bytes]", len(body))
		}
	}

	logJSON(logEntry)
}

// logResponseDetails logs detailed response information for debug level
func logResponseDetails(c *fiber.Ctx, requestID string, responseBody *bytes.Buffer, duration time.Duration) {
	logEntry := map[string]interface{}{
		"timestamp":    time.Now().Format(time.RFC3339),
		"level":        "debug",
		"component":    "response_logger",
		"event":        "response_sent",
		"request_id":   requestID,
		"status_code":  c.Response().StatusCode(),
		"duration_ms":  duration.Milliseconds(),
		"content_type": c.Get("Content-Type"),
	}

	// For Fiber, we can't easily capture response body without significant overhead
	// So we'll just log the response metadata
	contentType := c.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		logEntry["response_type"] = "streaming"
	} else {
		logEntry["response_type"] = "standard"
	}

	logJSON(logEntry)
}

// extractHeaders extracts relevant headers for logging (excluding sensitive ones)
func extractHeaders(c *fiber.Ctx) map[string]string {
	headers := make(map[string]string)

	// List of headers to log (excluding sensitive ones like Authorization, x-api-key)
	headersToLog := []string{
		"Accept",
		"Accept-Encoding",
		"Accept-Language",
		"Cache-Control",
		"Connection",
		"Content-Length",
		"Content-Type",
		"Origin",
		"Referer",
		"X-Forwarded-For",
		"X-Real-IP",
		"X-Request-ID",
		"X-Correlation-ID",
	}

	for _, headerName := range headersToLog {
		if value := c.Get(headerName); value != "" {
			headers[headerName] = value
		}
	}

	// Add custom headers that might be relevant (but mask sensitive ones)
	reqHeaders := c.GetReqHeaders()
	for headerName, headerValues := range reqHeaders {
		// Skip sensitive headers
		if isSensitiveHeader(headerName) {
			headers[headerName] = "[REDACTED]"
		} else if !contains(headersToLog, headerName) && strings.HasPrefix(headerName, "X-") {
			headers[headerName] = strings.Join(headerValues, ", ")
		}
	}

	return headers
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

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, item) {
			return true
		}
	}
	return false
}

// logJSON outputs a log entry as JSON
func logJSON(entry map[string]interface{}) {
	jsonData, err := json.Marshal(entry)
	if err != nil {
		fmt.Printf("Error marshaling log entry: %v\n", err)
		return
	}
	fmt.Println(string(jsonData))
}
