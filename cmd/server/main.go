package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"anthropic-openai-gateway/internal/config"
	"anthropic-openai-gateway/internal/handlers"
	"anthropic-openai-gateway/internal/middleware"
	"anthropic-openai-gateway/internal/services"

	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
)

const version = "1.0.0"

func main() {
	// Check for health check flag
	if len(os.Args) > 1 && os.Args[1] == "--health-check" {
		performHealthCheck()
		return
	}

	// Load .env file if it exists (optional - won't fail if file doesn't exist)
	if err := godotenv.Load(); err != nil {
		log.Printf("No .env file found or error loading .env file: %v", err)
	}

	// Load configuration
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Validate configuration at startup
	if err := cfg.ValidateAtStartup(); err != nil {
		log.Fatalf("Configuration validation failed: %v", err)
	}

	// Create Fiber app with custom configuration
	app := fiber.New(fiber.Config{
		AppName:      "Anthropic OpenAI Gateway",
		ServerHeader: "anthropic-openai-gateway",
		ErrorHandler: middleware.ErrorHandler,
		// Disable startup message for cleaner logs
		DisableStartupMessage: false,
	})

	// Initialize services
	converter := services.NewFormatConverter()

	// Initialize streaming component manager with configuration
	streamingManager := services.NewStreamingComponentManager(cfg)

	// Create streaming service with configuration and enhanced components
	bufferManager := streamingManager.GetBufferManager()
	streamingConfig := streamingManager.GetConfiguration()
	errorHandler := services.NewStreamErrorHandlerWithConfig(
		streamingConfig.MaxRetries,
		streamingConfig.RetryBackoffBase,
		streamingConfig.RetryBackoffMax,
		2.0,
		streamingConfig.LogLevel,
	)

	// Create streaming service with all components
	streamingService := services.NewStreamConverterWithComponents(
		converter,
		bufferManager,
		errorHandler,
		nil, // streamManager will be set by SetStreamConverter
	)
	streamingManager.SetStreamConverter(streamingService)

	// Initialize handlers with streaming manager
	apiHandler := handlers.NewAPIHandlerWithStreamingManager(converter, streamingManager)
	healthHandler := handlers.NewHealthHandler(version)

	staticHandler, err := handlers.NewStaticHandler()
	if err != nil {
		log.Fatalf("Failed to initialize static handler: %v", err)
	}

	// Setup middleware
	setupMiddleware(app, cfg)

	// Setup routes
	setupRoutes(app, apiHandler, healthHandler, staticHandler)

	// Start configuration auto-reload
	ctx := context.Background()
	streamingManager.StartAutoReload(ctx)

	// Setup graceful shutdown
	setupGracefulShutdown(app, cfg, streamingManager)

	// Start server
	address := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("Starting server on %s", address)

	if err := app.Listen(address); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// setupMiddleware configures all middleware for the application
func setupMiddleware(app *fiber.App, cfg *config.Config) {
	// Request ID middleware (should be first)
	app.Use(middleware.RequestIDMiddleware())

	// Debug request/response logging middleware (when log level is debug)
	app.Use(middleware.DebugRequestResponseLogger(cfg.LogLevel))

	// Logging middleware
	loggingConfig := middleware.DefaultLoggingConfig()
	loggingConfig.LogLevel = cfg.LogLevel
	app.Use(middleware.NewLogger(loggingConfig))

	// Recovery middleware
	app.Use(middleware.NewRecovery())

	// CORS middleware
	app.Use(middleware.NewCORS())
}

// setupRoutes configures all routes for the application
func setupRoutes(app *fiber.App, apiHandler *handlers.APIHandler, healthHandler *handlers.HealthHandler, staticHandler *handlers.StaticHandler) {
	// API routes
	api := app.Group("/v1")
	api.Post("/messages", apiHandler.HandleMessages)

	// Health check routes
	healthHandler.RegisterRoutes(app)

	// Static content routes
	app.Get("/", staticHandler.ServeIndex)
	app.Get("/terms", staticHandler.ServeTerms)
	app.Get("/privacy", staticHandler.ServePrivacy)
	app.Get("/install.sh", staticHandler.ServeInstallScript)

	// Favicon route (optional, returns 404 if not implemented)
	app.Get("/favicon.ico", func(c *fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).SendString("Not Found")
	})
}

// setupGracefulShutdown configures graceful shutdown handling
func setupGracefulShutdown(app *fiber.App, cfg *config.Config, streamingManager *services.StreamingComponentManager) {
	// Create a channel to receive OS signals
	sigChan := make(chan os.Signal, 1)

	// Register the channel to receive specific signals
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// Start a goroutine to handle shutdown
	go func() {
		// Wait for signal
		sig := <-sigChan
		log.Printf("Received signal: %v. Initiating graceful shutdown...", sig)

		// Create a context with timeout for shutdown
		ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()

		// Stop streaming manager first
		streamingManager.StopAutoReload()
		streamingManager.Shutdown()

		// Attempt graceful shutdown
		if err := app.ShutdownWithContext(ctx); err != nil {
			log.Printf("Error during graceful shutdown: %v", err)
		} else {
			log.Println("Server shutdown completed successfully")
		}

		// Exit the application
		os.Exit(0)
	}()
}

// performHealthCheck performs a health check for Docker health check
func performHealthCheck() {
	// Load .env file for health check too
	godotenv.Load()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	url := fmt.Sprintf("http://localhost:%s/health", port)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Health check failed: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Health check failed with status: %d", resp.StatusCode)
		os.Exit(1)
	}

	log.Println("Health check passed")
	os.Exit(0)
}
