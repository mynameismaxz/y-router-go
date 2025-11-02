package middleware

import (
	"io"
	"os"
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
}

// DefaultLoggingConfig returns default logging configuration
func DefaultLoggingConfig() LoggingConfig {
	return LoggingConfig{
		Format:     "[${time}] ${status} - ${method} ${path} - ${ip} - ${latency} - ${bytesSent}B\n",
		TimeFormat: "2006-01-02 15:04:05",
		TimeZone:   "UTC",
		Output:     os.Stdout, // Uses stdout as default output
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
