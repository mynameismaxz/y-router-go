package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"anthropic-openai-gateway/internal/models"
	"anthropic-openai-gateway/internal/services"

	"github.com/gofiber/fiber/v2"
)

const (
	// Adjust request timeout to be 120 seconds for API requests (with longer context support)
	requestTimeout = 120 * time.Second
)

// APIHandler handles API endpoint requests
type APIHandler struct {
	converter        services.ConversionService
	client           services.OpenRouterClient
	streamingService services.StreamingService
	streamManager    services.StreamManager
	errorHandler     services.StreamErrorHandler
	requestValidator *models.RequestValidator
}

// NewAPIHandler creates a new API handler instance
func NewAPIHandler(
	converter services.ConversionService,
	client services.OpenRouterClient,
	streamingService services.StreamingService,
) *APIHandler {
	// Create stream manager and error handler
	streamManager := services.NewStreamManager(client, streamingService)
	errorHandler := services.NewDefaultStreamErrorHandler()

	// Set up the streaming service with the stream manager if it supports it
	if streamConverter, ok := streamingService.(*services.StreamConverter); ok {
		streamConverter.SetStreamManager(streamManager)
	}

	return &APIHandler{
		converter:        converter,
		client:           client,
		streamingService: streamingService,
		streamManager:    streamManager,
		errorHandler:     errorHandler,
		requestValidator: models.NewRequestValidator(),
	}
}

// NewAPIHandlerWithStreamingManager creates a new API handler with streaming component manager
func NewAPIHandlerWithStreamingManager(
	converter services.ConversionService,
	streamingManager *services.StreamingComponentManager,
) *APIHandler {
	// Get components from the streaming manager
	client := streamingManager.GetHTTPClient()
	streamConverter := streamingManager.GetStreamConverter()
	streamManager := streamingManager.GetStreamManager()
	errorHandler := services.NewDefaultStreamErrorHandler()

	// Set up the streaming service with the stream manager
	if streamConverter != nil {
		streamConverter.SetStreamManager(streamManager)
	}

	return &APIHandler{
		converter:        converter,
		client:           client,
		streamingService: streamConverter, // StreamConverter implements StreamingService
		streamManager:    streamManager,
		errorHandler:     errorHandler,
		requestValidator: models.NewRequestValidator(),
	}
}

// NewAPIHandlerWithComponents creates a new API handler with custom components
func NewAPIHandlerWithComponents(
	converter services.ConversionService,
	client services.OpenRouterClient,
	streamingService services.StreamingService,
	streamManager services.StreamManager,
	errorHandler services.StreamErrorHandler,
) *APIHandler {
	return &APIHandler{
		converter:        converter,
		client:           client,
		streamingService: streamingService,
		streamManager:    streamManager,
		errorHandler:     errorHandler,
		requestValidator: models.NewRequestValidator(),
	}
}

// HandleMessages handles POST requests to "/v1/messages"
func (h *APIHandler) HandleMessages(c *fiber.Ctx) error {
	// Set request timeout
	ctx, cancel := context.WithTimeout(c.Context(), requestTimeout)
	defer cancel()

	// Validate content type
	contentType := c.Get("Content-Type")
	if err := h.requestValidator.ValidateContentType(contentType); err != nil {
		return h.handleValidationError(c, err)
	}

	// Validate x-api-key header
	apiKey := c.Get("x-api-key")
	if err := h.requestValidator.ValidateAuthHeader(apiKey); err != nil {
		return h.handleAPIError(c, err)
	}

	// Parse request body
	var anthropicReq models.AnthropicRequest
	if err := c.BodyParser(&anthropicReq); err != nil {
		return h.handleValidationError(c, &models.ValidationError{
			Field:   "body",
			Message: fmt.Sprintf("invalid JSON: %s", err.Error()),
		})
	}

	// Validate Anthropic request
	if err := anthropicReq.Validate(); err != nil {
		return h.handleValidationError(c, &models.ValidationError{
			Field:   "request",
			Message: err.Error(),
		})
	}

	// Convert Anthropic request to OpenAI format
	openAIReq, err := h.converter.AnthropicToOpenAI(&anthropicReq)
	if err != nil {
		return h.handleConversionError(c, err)
	}

	// Validate converted OpenAI request
	openAIReq.Messages = h.converter.ValidateToolCalls(openAIReq.Messages)

	// Handle streaming vs non-streaming requests
	if anthropicReq.Stream {
		return h.handleStreamingRequest(ctx, c, openAIReq, apiKey, anthropicReq.Model)
	}

	return h.handleNonStreamingRequest(ctx, c, openAIReq, apiKey, anthropicReq.Model)
}

// handleNonStreamingRequest handles regular (non-streaming) API requests
func (h *APIHandler) handleNonStreamingRequest(ctx context.Context, c *fiber.Ctx, openAIReq *models.OpenAIRequest, apiKey, originalModel string) error {
	// Send request to OpenRouter
	resp, err := h.client.SendRequest(ctx, openAIReq, apiKey)
	if err != nil {
		return h.handleUpstreamError(c, err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return h.handleInternalError(c, fmt.Errorf("failed to read response body: %w", err))
	}

	// Parse OpenAI response
	var openAIResp models.OpenAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return h.handleInternalError(c, fmt.Errorf("failed to parse OpenAI response: %w", err))
	}

	// Convert OpenAI response to Anthropic format
	anthropicResp, err := h.converter.OpenAIToAnthropic(&openAIResp, originalModel)
	if err != nil {
		return h.handleConversionError(c, err)
	}

	// Set response headers
	c.Set("Content-Type", "application/json")

	// Return Anthropic response
	return c.JSON(anthropicResp)
}

// handleStreamingRequest handles streaming API requests with enhanced stream management
func (h *APIHandler) handleStreamingRequest(ctx context.Context, c *fiber.Ctx, openAIReq *models.OpenAIRequest, apiKey, originalModel string) error {
	// Create stream request
	streamReq := &services.StreamRequest{
		Request:      openAIReq,
		APIKey:       apiKey,
		ClientIP:     c.IP(),
		UserAgent:    c.Get("User-Agent"),
		RequestID:    h.generateRequestID(),
		ConnectionID: h.generateConnectionID(c),
	}

	// Start stream using StreamManager
	streamResp, err := h.streamManager.StartStream(ctx, streamReq)
	if err != nil {
		// Use enhanced error handling
		if h.errorHandler != nil {
			streamCtx := services.StreamContext{
				StreamID:     "api_handler_stream",
				RequestID:    streamReq.RequestID,
				ClientIP:     streamReq.ClientIP,
				StartTime:    time.Now(),
				Model:        originalModel,
				ConnectionID: streamReq.ConnectionID,
				RetryCount:   0,
			}

			classification := h.errorHandler.ClassifyError(err)
			if classification.Type == services.UpstreamError {
				return h.handleUpstreamError(c, err)
			} else if classification.Type == services.ConnectionError {
				return h.handleStreamingConnectionError(c, err, streamCtx)
			}
		}
		return h.handleUpstreamError(c, err)
	}

	// Set streaming response headers
	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache")
	c.Set("Connection", "keep-alive")
	c.Set("Access-Control-Allow-Origin", "*")
	c.Set("X-Stream-ID", streamResp.StreamID) // Add stream ID for debugging

	// Stream the response with enhanced error handling and monitoring
	c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		defer func() {
			// Ensure proper cleanup through StreamManager
			if cleanupErr := h.streamManager.CleanupStream(streamResp.StreamID); cleanupErr != nil {
				fmt.Printf("Error cleaning up stream %s: %v\n", streamResp.StreamID, cleanupErr)
			}

			// Close the stream reader if it implements io.Closer
			if closer, ok := streamResp.Reader.(io.Closer); ok {
				closer.Close()
			}
		}()

		// Copy the converted stream to the response writer with error monitoring
		bytesWritten, err := h.copyStreamWithMonitoring(streamResp.Reader, w, streamResp.StreamID, streamResp.Context)
		if err != nil {
			// Check if this is a client disconnection (context cancellation)
			if errors.Is(err, context.Canceled) {
				// Client disconnected - this is normal, don't log as error
				return
			}

			// Handle streaming errors
			if h.errorHandler != nil {
				// Update stream context with current state
				streamResp.Context.BytesStreamed = bytesWritten
				streamResp.Context.LastActivity = time.Now()

				// Handle the error
				handledErr := h.errorHandler.HandleConnectionError(err, *streamResp.Context)
				if handledErr != nil {
					// Send error event to client if possible
					h.errorHandler.SendErrorEvent(w, "streaming_error", handledErr.Error())
				}

				// Record error with stream manager
				h.streamManager.HandleStreamError(handledErr, streamResp.StreamID)
			} else {
				// Log error but don't return it as the response has already started
				fmt.Printf("Error streaming response for stream %s: %v\n", streamResp.StreamID, err)
			}
		}

		// Flush any remaining data
		w.Flush()
	})

	return nil
}

// copyStreamWithMonitoring copies stream data while monitoring for errors and progress
func (h *APIHandler) copyStreamWithMonitoring(src io.Reader, dst io.Writer, streamID string, streamCtx *services.StreamContext) (int64, error) {
	buf := make([]byte, 32*1024) // 32KB buffer
	var totalBytes int64

	for {
		// Read from source
		nr, readErr := src.Read(buf)
		if nr > 0 {
			// Write to destination
			nw, writeErr := dst.Write(buf[0:nr])
			if nw > 0 {
				totalBytes += int64(nw)
			}

			// Handle write errors
			if writeErr != nil {
				return totalBytes, fmt.Errorf("write error after %d bytes: %w", totalBytes, writeErr)
			}

			// Check for short writes
			if nr != nw {
				return totalBytes, fmt.Errorf("short write: wrote %d bytes, expected %d", nw, nr)
			}

			// Flush if possible (for better streaming experience)
			if flusher, ok := dst.(interface{ Flush() error }); ok {
				flusher.Flush()
			}
		}

		// Handle read errors
		if readErr != nil {
			if readErr == io.EOF {
				return totalBytes, nil // Normal completion
			}
			return totalBytes, fmt.Errorf("read error after %d bytes: %w", totalBytes, readErr)
		}
	}
}

// generateRequestID generates a unique request identifier
func (h *APIHandler) generateRequestID() string {
	return fmt.Sprintf("req_%d", time.Now().UnixNano())
}

// generateConnectionID generates a connection identifier based on client info
func (h *APIHandler) generateConnectionID(c *fiber.Ctx) string {
	return fmt.Sprintf("conn_%s_%d", c.IP(), time.Now().UnixNano())
}

// Error handling methods

// handleValidationError handles validation errors with HTTP 400
func (h *APIHandler) handleValidationError(c *fiber.Ctx, err error) error {
	var apiErr *models.APIError

	switch e := err.(type) {
	case *models.ValidationError:
		apiErr = &models.APIError{
			Code:    http.StatusBadRequest,
			Message: e.Message,
			Type:    "invalid_request_error",
			Param:   e.Field,
		}
	case *models.APIError:
		apiErr = e
	default:
		apiErr = &models.APIError{
			Code:    http.StatusBadRequest,
			Message: err.Error(),
			Type:    "invalid_request_error",
		}
	}

	return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
		Error: apiErr,
	})
}

// handleAPIError handles API errors (authentication, etc.)
func (h *APIHandler) handleAPIError(c *fiber.Ctx, err error) error {
	if apiErr, ok := err.(*models.APIError); ok {
		return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
			Error: apiErr,
		})
	}

	// Fallback to internal error
	return h.handleInternalError(c, err)
}

// handleConversionError handles format conversion errors
func (h *APIHandler) handleConversionError(c *fiber.Ctx, err error) error {
	apiErr := &models.APIError{
		Code:    http.StatusBadRequest,
		Message: fmt.Sprintf("format conversion error: %s", err.Error()),
		Type:    "conversion_error",
	}

	return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
		Error: apiErr,
	})
}

// handleUpstreamError handles errors from upstream OpenRouter API
func (h *APIHandler) handleUpstreamError(c *fiber.Ctx, err error) error {
	if apiErr, ok := err.(*models.APIError); ok {
		// Forward upstream errors with original status codes
		return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
			Error: apiErr,
		})
	}

	// Fallback to bad gateway error
	apiErr := &models.APIError{
		Code:    http.StatusBadGateway,
		Message: fmt.Sprintf("upstream service error: %s", err.Error()),
		Type:    "upstream_error",
	}

	return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
		Error: apiErr,
	})
}

// handleStreamingError handles errors specific to streaming responses
func (h *APIHandler) handleStreamingError(c *fiber.Ctx, err error) error {
	// Use enhanced error handling if available
	if h.errorHandler != nil {
		streamCtx := services.StreamContext{
			StreamID:     "api_handler_error",
			ClientIP:     c.IP(),
			StartTime:    time.Now(),
			LastActivity: time.Now(),
		}

		classification := h.errorHandler.ClassifyError(err)

		// Handle the error with context
		handledErr := h.errorHandler.HandleProcessingError(err, streamCtx)
		if handledErr != nil {
			err = handledErr // Use the handled error for response
		}

		// Set appropriate HTTP status based on error classification
		statusCode := classification.HTTPStatus
		if statusCode == 0 {
			statusCode = http.StatusInternalServerError
		}

		// For streaming errors, we need to send an error event in SSE format
		c.Set("Content-Type", "text/event-stream")
		c.Status(statusCode)

		// Create error event with classification details
		errorEvent := &models.StreamEvent{
			Event: "error",
			Data: map[string]interface{}{
				"type":       string(classification.Type),
				"message":    err.Error(),
				"severity":   string(classification.Severity),
				"retryable":  classification.Retryable,
				"error_code": string(classification.Type),
			},
		}

		// Add retry information if applicable
		if classification.Retryable && classification.RetryAfter > 0 {
			errorEvent.Data.(map[string]interface{})["retry_after_ms"] = int64(classification.RetryAfter / time.Millisecond)
		}

		return c.SendString(errorEvent.FormatSSE())
	}

	// Fallback to original error handling
	errorEvent := &models.StreamEvent{
		Event: "error",
		Data: map[string]interface{}{
			"type":    "error",
			"message": err.Error(),
		},
	}

	c.Set("Content-Type", "text/event-stream")
	return c.SendString(errorEvent.FormatSSE())
}

// handleStreamingConnectionError handles connection-specific streaming errors
func (h *APIHandler) handleStreamingConnectionError(c *fiber.Ctx, err error, streamCtx services.StreamContext) error {
	if h.errorHandler != nil {
		// Handle the connection error
		handledErr := h.errorHandler.HandleConnectionError(err, streamCtx)

		// Check if this is a retryable error
		if retryableErr, ok := handledErr.(*services.RetryableError); ok {
			// Set retry headers
			c.Set("Retry-After", fmt.Sprintf("%.0f", retryableErr.RetryAfter.Seconds()))

			// Send retryable error event
			c.Set("Content-Type", "text/event-stream")
			c.Status(retryableErr.Classification.HTTPStatus)

			errorEvent := &models.StreamEvent{
				Event: "error",
				Data: map[string]interface{}{
					"type":           string(retryableErr.Classification.Type),
					"message":        retryableErr.OriginalError.Error(),
					"retryable":      true,
					"retry_after_ms": int64(retryableErr.RetryAfter / time.Millisecond),
					"retry_count":    retryableErr.Context.RetryCount,
				},
			}

			return c.SendString(errorEvent.FormatSSE())
		}

		// Handle non-retryable connection errors
		if classifiedErr, ok := handledErr.(*services.ClassifiedError); ok {
			c.Set("Content-Type", "text/event-stream")
			c.Status(classifiedErr.Classification.HTTPStatus)

			errorEvent := &models.StreamEvent{
				Event: "error",
				Data: map[string]interface{}{
					"type":      string(classifiedErr.Classification.Type),
					"message":   classifiedErr.OriginalError.Error(),
					"retryable": false,
					"severity":  string(classifiedErr.Classification.Severity),
				},
			}

			return c.SendString(errorEvent.FormatSSE())
		}
	}

	// Fallback to generic streaming error handling
	return h.handleStreamingError(c, err)
}

// handleInternalError handles internal server errors
func (h *APIHandler) handleInternalError(c *fiber.Ctx, err error) error {
	apiErr := &models.APIError{
		Code:    http.StatusInternalServerError,
		Message: "internal server error",
		Type:    "internal_error",
	}

	// Log the actual error for debugging (in production, use proper logging)
	fmt.Printf("Internal error: %v\n", err)

	return c.Status(apiErr.Code).JSON(&models.ErrorResponse{
		Error: apiErr,
	})
}

// Health check handler
func (h *APIHandler) HandleHealth(c *fiber.Ctx) error {
	health := &models.HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().Unix(),
		Version:   "1.0.0",
	}

	return c.JSON(health)
}
