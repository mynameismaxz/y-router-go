package middleware

import (
	"log"
	"runtime/debug"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
)

// RecoveryConfig holds configuration for recovery middleware
type RecoveryConfig struct {
	EnableStackTrace bool
	LogStackTrace    bool
}

// DefaultRecoveryConfig returns default recovery configuration
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		EnableStackTrace: true,
		LogStackTrace:    true,
	}
}

// NewRecovery creates a new recovery middleware with the provided configuration
func NewRecovery(config ...RecoveryConfig) fiber.Handler {
	cfg := DefaultRecoveryConfig()
	if len(config) > 0 {
		cfg = config[0]
	}

	return recover.New(recover.Config{
		EnableStackTrace: cfg.EnableStackTrace,
		StackTraceHandler: func(c *fiber.Ctx, e interface{}) {
			requestID := c.Locals("request_id")
			if requestID == nil {
				requestID = "unknown"
			}

			// Log the panic with request context
			log.Printf("PANIC RECOVERED [Request ID: %v] [Path: %s] [Method: %s] [IP: %s]: %v",
				requestID, c.Path(), c.Method(), c.IP(), e)

			if cfg.LogStackTrace {
				log.Printf("Stack trace [Request ID: %v]:\n%s", requestID, debug.Stack())
			}

			// Set error response
			c.Status(fiber.StatusInternalServerError)
			c.JSON(fiber.Map{
				"error": fiber.Map{
					"code":    fiber.StatusInternalServerError,
					"message": "Internal server error",
					"type":    "internal_error",
				},
				"request_id": requestID,
			})
		},
	})
}

// ErrorHandler is a custom error handler for Fiber
func ErrorHandler(c *fiber.Ctx, err error) error {
	// Default to 500 server error
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"

	// Check if it's a Fiber error
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}

	requestID := c.Locals("request_id")
	if requestID == nil {
		requestID = "unknown"
	}

	// Log the error
	log.Printf("ERROR [Request ID: %v] [Path: %s] [Method: %s] [Status: %d]: %v",
		requestID, c.Path(), c.Method(), code, err)

	// Send error response
	return c.Status(code).JSON(fiber.Map{
		"error": fiber.Map{
			"code":    code,
			"message": message,
			"type":    getErrorType(code),
		},
		"request_id": requestID,
	})
}

// getErrorType returns error type based on status code
func getErrorType(code int) string {
	switch {
	case code >= 400 && code < 500:
		return "client_error"
	case code >= 500:
		return "server_error"
	default:
		return "unknown_error"
	}
}
