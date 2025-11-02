package services

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"anthropic-openai-gateway/internal/config"
	"anthropic-openai-gateway/internal/models"
)

// StreamingService defines the interface for streaming response conversion
type StreamingService interface {
	ConvertStream(ctx context.Context, openaiStream io.Reader, model string) (io.Reader, error)
}

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	Event string      `json:"event,omitempty"`
	Data  interface{} `json:"data,omitempty"`
}

// ToolCallBuffer buffers partial tool call data for future enhancement
type ToolCallBuffer struct {
	ID        string
	Name      string
	Arguments strings.Builder
	Started   bool
}

// StreamConverter implements the StreamingService interface
type StreamConverter struct {
	converter     ConversionService
	bufferManager BufferManager
	errorHandler  StreamErrorHandler
	streamManager StreamManager
	logger        StructuredLogger
	logLevel      string
}

// StreamBuffer handles buffering of incomplete JSON data
type StreamBuffer struct {
	buffer    strings.Builder
	lineCount int
}

// StreamError represents streaming-specific errors
type StreamError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    int    `json:"code,omitempty"`
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("stream error [%s]: %s", e.Type, e.Message)
}

// NewStreamConverter creates a new StreamConverter instance
func NewStreamConverter(converter ConversionService) *StreamConverter {
	return &StreamConverter{
		converter:     converter,
		bufferManager: NewStreamBufferManager(DefaultBufferConfig()),
		errorHandler:  NewDefaultStreamErrorHandler(),
		streamManager: nil, // Will be set by the API handler
		logger:        NewStructuredLogger(nil),
		logLevel:      "info",
	}
}

// NewStreamConverterWithConfig creates a new StreamConverter with streaming configuration
func NewStreamConverterWithConfig(converter ConversionService, streamingConfig *config.StreamingConfig) *StreamConverter {
	return &StreamConverter{
		converter:     converter,
		bufferManager: NewStreamBufferManagerFromConfig(streamingConfig),
		errorHandler:  NewDefaultStreamErrorHandler(),
		streamManager: nil, // Will be set by the API handler
		logger:        NewStructuredLogger(nil),
		logLevel:      streamingConfig.LogLevel,
	}
}

// NewStreamConverterWithComponents creates a new StreamConverter with custom components
func NewStreamConverterWithComponents(
	converter ConversionService,
	bufferManager BufferManager,
	errorHandler StreamErrorHandler,
	streamManager StreamManager,
) *StreamConverter {
	return &StreamConverter{
		converter:     converter,
		bufferManager: bufferManager,
		errorHandler:  errorHandler,
		streamManager: streamManager,
		logger:        NewStructuredLogger(nil),
		logLevel:      "info",
	}
}

// SetStreamManager sets the stream manager for lifecycle tracking
func (sc *StreamConverter) SetStreamManager(streamManager StreamManager) {
	sc.streamManager = streamManager
}

// SetLogLevel sets the log level for debug logging
func (sc *StreamConverter) SetLogLevel(logLevel string) {
	sc.logLevel = logLevel
}

// logDebugStreamData logs streaming data when log level is debug
func (sc *StreamConverter) logDebugStreamData(eventType, data string, streamID string) {
	if strings.ToLower(sc.logLevel) != "debug" {
		return
	}

	logEntry := map[string]interface{}{
		"timestamp":  time.Now().Format(time.RFC3339),
		"level":      "debug",
		"component":  "stream_converter",
		"event":      "stream_data",
		"stream_id":  streamID,
		"event_type": eventType,
	}

	// Only log data for smaller chunks to avoid log spam
	if len(data) < 1024 {
		logEntry["data"] = data
	} else {
		logEntry["data"] = fmt.Sprintf("[LARGE_DATA: %d bytes]", len(data))
	}

	if sc.logger != nil {
		sc.logger.Log(LogLevelDebug, "stream_converter", fmt.Sprintf("streaming %s event", eventType), logEntry)
	}
}

// ConvertStream converts OpenAI streaming response to Anthropic streaming format
func (sc *StreamConverter) ConvertStream(ctx context.Context, openaiStream io.Reader, model string) (io.Reader, error) {
	pipeReader, pipeWriter := io.Pipe()

	go func() {
		defer func() {
			// Ensure proper cleanup
			pipeWriter.Close()

			// Close the source stream if it implements io.Closer
			if closer, ok := openaiStream.(io.Closer); ok {
				closer.Close()
			}

			// Reset buffer manager for next use
			if sc.bufferManager != nil {
				sc.bufferManager.Reset()
			}
		}()

		if err := sc.processStreamWithEnhancedHandling(ctx, openaiStream, pipeWriter, model); err != nil {
			// Use enhanced error handling
			if sc.errorHandler != nil {
				// Create a basic stream context for error handling
				streamCtx := StreamContext{
					StreamID:     "converter_stream",
					Model:        model,
					StartTime:    time.Now(),
					LastActivity: time.Now(),
				}

				// Handle the error with classification
				handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
				if handledErr != nil {
					// Send error event to client
					sc.errorHandler.SendErrorEvent(pipeWriter, "processing_error", handledErr.Error())
					pipeWriter.CloseWithError(handledErr)
				} else {
					// Error was handled gracefully, close normally
					pipeWriter.Close()
				}
			} else {
				// Fallback to original error handling
				var streamErr *StreamError
				if errors.As(err, &streamErr) {
					pipeWriter.CloseWithError(streamErr)
				} else {
					pipeWriter.CloseWithError(&StreamError{
						Type:    "unknown_error",
						Message: fmt.Sprintf("stream processing error: %v", err),
					})
				}
			}
		}
	}()

	return pipeReader, nil
}

// processStreamWithEnhancedHandling processes the OpenAI stream with enhanced error handling and buffering
func (sc *StreamConverter) processStreamWithEnhancedHandling(ctx context.Context, openaiStream io.Reader, writer io.Writer, model string) error {
	// Ensure StreamConverter is properly initialized
	if sc == nil {
		return fmt.Errorf("stream converter is nil")
	}

	// Use enhanced processing if buffer manager is available
	if sc.bufferManager != nil {
		return sc.processStreamWithBuffering(ctx, openaiStream, writer, model)
	}

	// Fallback to original processing
	return sc.processStream(ctx, openaiStream, writer, model)
}

// processStreamWithBuffering processes the stream using the BufferManager for robust chunk handling
func (sc *StreamConverter) processStreamWithBuffering(ctx context.Context, openaiStream io.Reader, writer io.Writer, model string) error {
	scanner := bufio.NewScanner(openaiStream)

	// Set a larger buffer size for handling large JSON chunks
	const maxCapacity = 1024 * 1024 // 1MB buffer
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	// Initialize streaming state
	messageID := ""
	contentBlockIndex := 0
	hasStartedMessage := false

	// Set up timeout for stream processing
	streamTimeout := 30 * time.Second
	timer := time.NewTimer(streamTimeout)
	defer timer.Stop()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			// Check if it's a cancellation (client disconnect) vs timeout
			if errors.Is(ctx.Err(), context.Canceled) {
				// Client disconnected - this is normal, don't treat as error
				return nil
			}
			return &StreamError{
				Type:    "context_cancelled",
				Message: "stream processing cancelled",
			}
		case <-timer.C:
			return &StreamError{
				Type:    "timeout",
				Message: "stream processing timeout",
			}
		default:
		}

		// Reset timer on each successful read
		timer.Reset(streamTimeout)

		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Handle SSE data lines
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Log incoming stream data for debug
			sc.logDebugStreamData("incoming_openai", data, "stream_converter")

			// Check for stream end
			if data == "[DONE]" {
				// Log stream completion for debug
				sc.logDebugStreamData("stream_complete", "[DONE]", "stream_converter")

				// Send message_stop event
				if err := sc.writeAnthropicEvent(writer, "message_stop", nil); err != nil {
					return &StreamError{
						Type:    "write_error",
						Message: fmt.Sprintf("failed to write message_stop event: %v", err),
					}
				}
				break
			}

			// Process the data with enhanced buffering
			if err := sc.processStreamDataWithBuffering(data, writer, model, &messageID, &contentBlockIndex, &hasStartedMessage); err != nil {
				// Use error handler if available
				if sc.errorHandler != nil {
					streamCtx := StreamContext{
						StreamID:     "converter_stream",
						Model:        model,
						StartTime:    time.Now(),
						LastActivity: time.Now(),
					}

					handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
					if handledErr != nil {
						return fmt.Errorf("failed to process stream data: %w", handledErr)
					}
					// Continue processing if error was handled gracefully
				} else {
					return fmt.Errorf("failed to process stream data: %w", err)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		// Use error handler for scanner errors
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				Model:        model,
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			classification := sc.errorHandler.ClassifyError(err)
			if classification.Type == ConnectionError {
				return sc.errorHandler.HandleConnectionError(err, streamCtx)
			} else {
				return sc.errorHandler.HandleProcessingError(err, streamCtx)
			}
		}

		return &StreamError{
			Type:    "read_error",
			Message: fmt.Sprintf("error reading stream: %v", err),
		}
	}

	return nil
}

// processStreamDataWithBuffering processes individual data chunks using BufferManager
func (sc *StreamConverter) processStreamDataWithBuffering(data string, writer io.Writer, model string, messageID *string, contentBlockIndex *int, hasStartedMessage *bool) error {
	// Ensure buffer manager is available
	if sc.bufferManager == nil {
		return fmt.Errorf("buffer manager is nil")
	}

	// Process data through buffer manager
	processedChunks, err := sc.bufferManager.ProcessChunk([]byte(data))
	if err != nil {
		// Handle buffer errors gracefully
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				Model:        model,
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			classification := sc.errorHandler.ClassifyError(err)
			if classification.Type == BufferError {
				// Handle buffer error with context
				handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
				if handledErr == nil {
					// For buffer errors, try to flush and continue
					flushedChunks, flushErr := sc.bufferManager.FlushBuffer()
					if flushErr == nil && len(flushedChunks) > 0 {
						// Process flushed chunks
						for _, chunk := range flushedChunks {
							if processErr := sc.processChunkData(string(chunk.Data), writer, model, messageID, contentBlockIndex, hasStartedMessage); processErr != nil {
								return processErr
							}
						}
					}
					// Continue processing even if buffer had issues
					return nil
				}
			}
		}
		return fmt.Errorf("buffer processing error: %w", err)
	}

	// Process all successfully buffered chunks
	for _, chunk := range processedChunks {
		if err := sc.processChunkData(string(chunk.Data), writer, model, messageID, contentBlockIndex, hasStartedMessage); err != nil {
			return fmt.Errorf("failed to process chunk %s: %w", chunk.ChunkID, err)
		}
	}

	return nil
}

// processChunkData processes a single chunk of data
func (sc *StreamConverter) processChunkData(data string, writer io.Writer, model string, messageID *string, contentBlockIndex *int, hasStartedMessage *bool) error {
	var chunk models.OpenAIStreamResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		// Handle JSON parsing errors gracefully
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				Model:        model,
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			classification := sc.errorHandler.ClassifyError(err)
			if classification.Severity == SeverityLow {
				// Handle low severity error with context
				handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
				if handledErr == nil {
					// Skip malformed chunks for low severity errors
					return nil
				}
			}
		}
		return fmt.Errorf("failed to parse JSON chunk: %w", err)
	}

	// Process the chunk with error handling
	return sc.processOpenAIChunkSafely(&chunk, writer, model, messageID, contentBlockIndex, hasStartedMessage)
}

// processStream processes the OpenAI stream and writes Anthropic events to the writer (original method)
func (sc *StreamConverter) processStream(ctx context.Context, openaiStream io.Reader, writer io.Writer, model string) error {
	scanner := bufio.NewScanner(openaiStream)

	// Set a larger buffer size for handling large JSON chunks
	const maxCapacity = 1024 * 1024 // 1MB buffer
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	// Initialize streaming state
	messageID := ""
	contentBlockIndex := 0
	hasStartedMessage := false
	streamBuffer := &StreamBuffer{}

	// Set up timeout for stream processing
	streamTimeout := 30 * time.Second
	timer := time.NewTimer(streamTimeout)
	defer timer.Stop()

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			// Check if it's a cancellation (client disconnect) vs timeout
			if errors.Is(ctx.Err(), context.Canceled) {
				// Client disconnected - this is normal, don't treat as error
				return nil
			}
			return &StreamError{
				Type:    "context_cancelled",
				Message: "stream processing cancelled",
			}
		case <-timer.C:
			return &StreamError{
				Type:    "timeout",
				Message: "stream processing timeout",
			}
		default:
		}

		// Reset timer on each successful read
		timer.Reset(streamTimeout)

		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		// Handle SSE data lines
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				// Send message_stop event
				if err := sc.writeAnthropicEvent(writer, "message_stop", nil); err != nil {
					return &StreamError{
						Type:    "write_error",
						Message: fmt.Sprintf("failed to write message_stop event: %v", err),
					}
				}
				break
			}

			// Process the data with buffering
			if err := sc.processStreamData(data, streamBuffer, writer, model, &messageID, &contentBlockIndex, &hasStartedMessage); err != nil {
				return fmt.Errorf("failed to process stream data: %w", err)
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return &StreamError{
			Type:    "read_error",
			Message: fmt.Sprintf("error reading stream: %v", err),
		}
	}

	return nil
}

// processStreamData processes individual data chunks with buffering support (legacy method)
func (sc *StreamConverter) processStreamData(data string, buffer *StreamBuffer, writer io.Writer, model string, messageID *string, contentBlockIndex *int, hasStartedMessage *bool) error {
	// If we have the new buffer manager, use it instead
	if sc.bufferManager != nil {
		return sc.processStreamDataWithBuffering(data, writer, model, messageID, contentBlockIndex, hasStartedMessage)
	}

	// Legacy buffering logic
	// Add data to buffer
	buffer.buffer.WriteString(data)
	buffer.lineCount++

	// Try to parse the buffered data as JSON
	bufferedData := buffer.buffer.String()

	var chunk models.OpenAIStreamResponse
	if err := json.Unmarshal([]byte(bufferedData), &chunk); err != nil {
		// If JSON is incomplete and buffer is reasonable size, continue buffering
		if buffer.lineCount < 10 && len(bufferedData) < 10240 { // Max 10 lines or 10KB
			return nil // Continue buffering
		}

		// If buffer is too large or too many lines, reset and try with just current data
		buffer.buffer.Reset()
		buffer.lineCount = 0

		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Use error handler if available
			if sc.errorHandler != nil {
				streamCtx := StreamContext{
					StreamID:     "converter_stream",
					Model:        model,
					StartTime:    time.Now(),
					LastActivity: time.Now(),
				}

				handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
				if handledErr == nil {
					// Error handled gracefully, skip this chunk
					return nil
				}
			}
			// Log the error but continue processing (skip malformed chunks)
			return nil
		}
	} else {
		// Successfully parsed, reset buffer
		buffer.buffer.Reset()
		buffer.lineCount = 0
	}

	// Process the chunk with error handling
	if err := sc.processOpenAIChunkSafely(&chunk, writer, model, messageID, contentBlockIndex, hasStartedMessage); err != nil {
		// Use enhanced error handling if available
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				Model:        model,
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
			if handledErr != nil {
				return &StreamError{
					Type:    "processing_error",
					Message: fmt.Sprintf("failed to process chunk: %v", handledErr),
				}
			}
			return nil // Error handled gracefully
		}

		return &StreamError{
			Type:    "processing_error",
			Message: fmt.Sprintf("failed to process chunk: %v", err),
		}
	}

	return nil
}

// processOpenAIChunkSafely processes a chunk with additional error handling and recovery
func (sc *StreamConverter) processOpenAIChunkSafely(chunk *models.OpenAIStreamResponse, writer io.Writer, model string, messageID *string, contentBlockIndex *int, hasStartedMessage *bool) error {
	// Recover from panics during chunk processing
	defer func() {
		if r := recover(); r != nil {
			// Log the panic but don't crash the stream
			fmt.Printf("Recovered from panic during chunk processing: %v\n", r)
		}
	}()

	return sc.processOpenAIChunk(chunk, writer, model, messageID, contentBlockIndex, hasStartedMessage)
}

// processOpenAIChunk processes a single OpenAI streaming chunk and generates appropriate Anthropic events
func (sc *StreamConverter) processOpenAIChunk(chunk *models.OpenAIStreamResponse, writer io.Writer, model string, messageID *string, contentBlockIndex *int, hasStartedMessage *bool) error {
	if len(chunk.Choices) == 0 {
		return nil
	}

	choice := chunk.Choices[0]

	// Set message ID from first chunk
	if *messageID == "" {
		*messageID = chunk.ID
	}

	// Send message_start event if not already sent
	if !*hasStartedMessage {
		messageStartEvent := &models.AnthropicResponse{
			ID:      *messageID,
			Type:    "message",
			Role:    "assistant",
			Model:   model,
			Content: []models.AnthropicContentBlock{},
			Usage: &models.AnthropicUsage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		}

		if err := sc.writeAnthropicEvent(writer, "message_start", map[string]interface{}{
			"message": messageStartEvent,
		}); err != nil {
			return fmt.Errorf("failed to write message_start event: %w", err)
		}

		*hasStartedMessage = true
	}

	// Process delta content
	if choice.Delta != nil {
		if err := sc.processDelta(choice.Delta, writer, contentBlockIndex, choice.FinishReason); err != nil {
			return fmt.Errorf("failed to process delta: %w", err)
		}
	}

	return nil
}

// processDelta processes delta content from OpenAI and generates Anthropic streaming events
func (sc *StreamConverter) processDelta(delta *models.OpenAIMessage, writer io.Writer, contentBlockIndex *int, finishReason *string) error {
	// Handle text content
	if delta.Content != nil {
		textContent, err := delta.ParseContent()
		if err != nil {
			return fmt.Errorf("failed to parse delta content: %w", err)
		}

		if textContent != "" {
			// Send content_block_start if this is the first content
			if *contentBlockIndex == 0 {
				contentBlock := models.AnthropicContentBlock{
					Type: "text",
					Text: "",
				}

				if err := sc.writeAnthropicEvent(writer, "content_block_start", map[string]interface{}{
					"index":         *contentBlockIndex,
					"content_block": contentBlock,
				}); err != nil {
					return fmt.Errorf("failed to write content_block_start event: %w", err)
				}
			}

			// Send content_block_delta
			if err := sc.writeAnthropicEvent(writer, "content_block_delta", map[string]interface{}{
				"index": *contentBlockIndex,
				"delta": map[string]interface{}{
					"type": "text_delta",
					"text": textContent,
				},
			}); err != nil {
				return fmt.Errorf("failed to write content_block_delta event: %w", err)
			}
		}
	}

	// Handle tool calls
	if len(delta.ToolCalls) > 0 {
		if err := sc.processToolCallDeltas(writer, delta.ToolCalls, contentBlockIndex); err != nil {
			return fmt.Errorf("failed to process tool call deltas: %w", err)
		}
	}

	// Handle finish reason
	if finishReason != nil {
		if err := sc.processFinishReason(writer, *finishReason, contentBlockIndex); err != nil {
			return fmt.Errorf("failed to process finish reason: %w", err)
		}
		*contentBlockIndex++
	}

	return nil
}

// processToolCallDeltas handles tool call deltas from OpenAI streaming
func (sc *StreamConverter) processToolCallDeltas(writer io.Writer, toolCalls []models.OpenAIToolCall, contentBlockIndex *int) error {
	for _, toolCall := range toolCalls {
		if toolCall.Type != "function" {
			continue
		}

		// Handle tool call start (when we get the function name)
		if toolCall.Function.Name != "" {
			// Increment content block index for new tool call
			if *contentBlockIndex > 0 {
				*contentBlockIndex++
			}

			contentBlock := models.AnthropicContentBlock{
				Type: "tool_use",
				ID:   toolCall.ID,
				Name: toolCall.Function.Name,
			}

			if err := sc.writeAnthropicEvent(writer, "content_block_start", map[string]interface{}{
				"index":         *contentBlockIndex,
				"content_block": contentBlock,
			}); err != nil {
				return fmt.Errorf("failed to write tool_use content_block_start event: %w", err)
			}
		}

		// Handle function arguments delta
		if toolCall.Function.Arguments != "" {
			if err := sc.writeAnthropicEvent(writer, "content_block_delta", map[string]interface{}{
				"index": *contentBlockIndex,
				"delta": map[string]interface{}{
					"type":         "input_json_delta",
					"partial_json": toolCall.Function.Arguments,
				},
			}); err != nil {
				return fmt.Errorf("failed to write tool_use content_block_delta event: %w", err)
			}
		}
	}

	return nil
}

// processTextDelta handles text content deltas with proper event sequencing
func (sc *StreamConverter) processTextDelta(writer io.Writer, textContent string, contentBlockIndex *int) error {
	// Send content_block_start if this is the first text content
	if *contentBlockIndex == 0 {
		contentBlock := models.AnthropicContentBlock{
			Type: "text",
			Text: "",
		}

		if err := sc.writeAnthropicEvent(writer, "content_block_start", map[string]interface{}{
			"index":         *contentBlockIndex,
			"content_block": contentBlock,
		}); err != nil {
			return fmt.Errorf("failed to write content_block_start event: %w", err)
		}
	}

	// Send content_block_delta with text
	if err := sc.writeAnthropicEvent(writer, "content_block_delta", map[string]interface{}{
		"index": *contentBlockIndex,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": textContent,
		},
	}); err != nil {
		return fmt.Errorf("failed to write content_block_delta event: %w", err)
	}

	return nil
}

// processFinishReason handles the completion of streaming with proper event sequencing
func (sc *StreamConverter) processFinishReason(writer io.Writer, finishReason string, contentBlockIndex *int) error {
	// Send content_block_stop for the current content block
	if err := sc.writeAnthropicEvent(writer, "content_block_stop", map[string]interface{}{
		"index": *contentBlockIndex,
	}); err != nil {
		return fmt.Errorf("failed to write content_block_stop event: %w", err)
	}

	// Convert OpenAI finish reason to Anthropic stop reason
	stopReason := sc.convertFinishReason(finishReason)

	// Send message_delta with stop reason
	if err := sc.writeAnthropicEvent(writer, "message_delta", map[string]interface{}{
		"delta": map[string]interface{}{
			"stop_reason": stopReason,
		},
	}); err != nil {
		return fmt.Errorf("failed to write message_delta event: %w", err)
	}

	return nil
}

// convertFinishReason converts OpenAI finish reason to Anthropic stop reason
func (sc *StreamConverter) convertFinishReason(finishReason string) string {
	switch finishReason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// writeAnthropicEvent writes an Anthropic streaming event in SSE format
func (sc *StreamConverter) writeAnthropicEvent(writer io.Writer, eventType string, data interface{}) error {
	// Create the event structure
	event := map[string]interface{}{
		"type": eventType,
	}

	// Add data if provided
	if data != nil {
		for k, v := range data.(map[string]interface{}) {
			event[k] = v
		}
	}

	// Marshal to JSON
	eventJSON, err := json.Marshal(event)
	if err != nil {
		// Use error handler if available
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			handledErr := sc.errorHandler.HandleProcessingError(err, streamCtx)
			if handledErr != nil {
				return fmt.Errorf("failed to marshal event: %w", handledErr)
			}
			// If error was handled gracefully, skip this event
			return nil
		}
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Log outgoing Anthropic event for debug
	sc.logDebugStreamData("outgoing_anthropic", string(eventJSON), "stream_converter")

	// Write in SSE format
	sseData := fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, string(eventJSON))

	_, err = writer.Write([]byte(sseData))
	if err != nil {
		// Use error handler for write errors
		if sc.errorHandler != nil {
			streamCtx := StreamContext{
				StreamID:     "converter_stream",
				StartTime:    time.Now(),
				LastActivity: time.Now(),
			}

			classification := sc.errorHandler.ClassifyError(err)
			if classification.Type == ConnectionError {
				return sc.errorHandler.HandleConnectionError(err, streamCtx)
			} else {
				return sc.errorHandler.HandleProcessingError(err, streamCtx)
			}
		}
		return fmt.Errorf("failed to write SSE data: %w", err)
	}

	return nil
}
