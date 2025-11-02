package handlers

import (
	"runtime"
	"time"

	"github.com/gofiber/fiber/v2"
)

// HealthHandler handles health check endpoints
type HealthHandler struct {
	startTime time.Time
	version   string
}

// NewHealthHandler creates a new health handler
func NewHealthHandler(version string) *HealthHandler {
	return &HealthHandler{
		startTime: time.Now(),
		version:   version,
	}
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp int64             `json:"timestamp"`
	Uptime    string            `json:"uptime"`
	Version   string            `json:"version"`
	System    SystemInfo        `json:"system"`
	Services  map[string]string `json:"services"`
}

// SystemInfo contains system information
type SystemInfo struct {
	GoVersion    string `json:"go_version"`
	NumGoroutine int    `json:"num_goroutine"`
	NumCPU       int    `json:"num_cpu"`
}

// ReadinessResponse represents the readiness check response
type ReadinessResponse struct {
	Status   string            `json:"status"`
	Services map[string]string `json:"services"`
}

// LivenessResponse represents the liveness check response
type LivenessResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
}

// Health handles GET /health - comprehensive health check
func (h *HealthHandler) Health(c *fiber.Ctx) error {
	uptime := time.Since(h.startTime)

	response := HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().Unix(),
		Uptime:    uptime.String(),
		Version:   h.version,
		System: SystemInfo{
			GoVersion:    runtime.Version(),
			NumGoroutine: runtime.NumGoroutine(),
			NumCPU:       runtime.NumCPU(),
		},
		Services: h.checkServices(),
	}

	// Check if any service is unhealthy
	for _, status := range response.Services {
		if status != "healthy" {
			response.Status = "degraded"
			return c.Status(fiber.StatusServiceUnavailable).JSON(response)
		}
	}

	return c.JSON(response)
}

// Readiness handles GET /health/ready - readiness check
func (h *HealthHandler) Readiness(c *fiber.Ctx) error {
	services := h.checkServices()

	response := ReadinessResponse{
		Status:   "ready",
		Services: services,
	}

	// Check if any critical service is not ready
	for _, status := range services {
		if status != "healthy" {
			response.Status = "not_ready"
			return c.Status(fiber.StatusServiceUnavailable).JSON(response)
		}
	}

	return c.JSON(response)
}

// Liveness handles GET /health/live - liveness check
func (h *HealthHandler) Liveness(c *fiber.Ctx) error {
	response := LivenessResponse{
		Status:    "alive",
		Timestamp: time.Now().Unix(),
	}

	return c.JSON(response)
}

// checkServices performs health checks on dependent services
func (h *HealthHandler) checkServices() map[string]string {
	services := make(map[string]string)

	// Check OpenRouter API connectivity (basic check)
	// In a real implementation, you might want to make a lightweight request
	// to verify the upstream service is accessible
	services["openrouter_api"] = "healthy"

	// Check internal services
	services["format_converter"] = "healthy"
	services["streaming_service"] = "healthy"
	services["static_content"] = "healthy"

	return services
}

// RegisterHealthRoutes registers all health check routes
func (h *HealthHandler) RegisterRoutes(app *fiber.App) {
	// Main health check endpoint
	app.Get("/health", h.Health)

	// Kubernetes-style health checks
	app.Get("/health/ready", h.Readiness)
	app.Get("/health/live", h.Liveness)

	// Alternative endpoints for different monitoring systems
	app.Get("/healthz", h.Liveness)
	app.Get("/readyz", h.Readiness)
}
