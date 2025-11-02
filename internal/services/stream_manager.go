package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"

	"anthropic-openai-gateway/internal/models"
)

// StreamStatus represents the current status of a stream
type StreamStatus string

const (
	StreamStatusStarting  StreamStatus = "starting"
	StreamStatusActive    StreamStatus = "active"
	StreamStatusCompleted StreamStatus = "completed"
	StreamStatusFailed    StreamStatus = "failed"
	StreamStatusCancelled StreamStatus = "cancelled"
)

// StreamContext holds context information for a streaming request
type StreamContext struct {
	StreamID      string            `json:"stream_id"`
	RequestID     string            `json:"request_id"`
	ClientIP      string            `json:"client_ip"`
	StartTime     time.Time         `json:"start_time"`
	Model         string            `json:"model"`
	ConnectionID  string            `json:"connection_id"`
	RetryCount    int               `json:"retry_count"`
	UserAgent     string            `json:"user_agent"`
	APIKey        string            `json:"api_key"` // Sanitized for logging
	Status        StreamStatus      `json:"status"`
	LastActivity  time.Time         `json:"last_activity"`
	BytesStreamed int64             `json:"bytes_streamed"`
	ErrorCount    int               `json:"error_count"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// StreamRequest represents a request to start a stream
type StreamRequest struct {
	Request      *models.OpenAIRequest
	APIKey       string
	ClientIP     string
	UserAgent    string
	RequestID    string
	ConnectionID string
}

// StreamResponse represents the response from starting a stream
type StreamResponse struct {
	StreamID string
	Reader   io.Reader
	Context  *StreamContext
}

// StreamManager defines the interface for managing streaming connections
type StreamManager interface {
	StartStream(ctx context.Context, req *StreamRequest) (*StreamResponse, error)
	HandleStreamError(err error, streamID string) error
	CleanupStream(streamID string) error
	GetStreamStatus(streamID string) StreamStatus
	GetStreamContext(streamID string) (*StreamContext, error)
	ListActiveStreams() []string
	GetStreamMetrics() StreamMetrics
}

// StreamMetrics holds metrics about streaming operations
type StreamMetrics struct {
	ActiveStreams      int
	TotalStreams       int64
	CompletedStreams   int64
	FailedStreams      int64
	AverageLatencyMs   int64
	TotalBytesStreamed int64
}

// DefaultStreamManager implements the StreamManager interface
type DefaultStreamManager struct {
	streams          map[string]*StreamContext
	streamsMutex     sync.RWMutex
	client           OpenRouterClient
	converter        StreamingService
	metrics          StreamMetrics
	metricsMutex     sync.RWMutex
	cleanupTicker    *time.Ticker
	stopCleanup      chan struct{}
	lifecycleManager *StreamLifecycleManager
	resourceManager  *StreamResourceManager
	progressTrackers map[string]*StreamProgressTracker
	trackersMutex    sync.RWMutex
}

// NewStreamManager creates a new DefaultStreamManager instance
func NewStreamManager(client OpenRouterClient, converter StreamingService) *DefaultStreamManager {
	sm := &DefaultStreamManager{
		streams:          make(map[string]*StreamContext),
		client:           client,
		converter:        converter,
		stopCleanup:      make(chan struct{}),
		lifecycleManager: NewStreamLifecycleManager(1000),
		resourceManager:  NewStreamResourceManager(),
		progressTrackers: make(map[string]*StreamProgressTracker),
	}

	// Start cleanup routine for stale streams
	sm.startCleanupRoutine()

	return sm
}

// NewStreamManagerWithLifecycle creates a stream manager with custom lifecycle manager
func NewStreamManagerWithLifecycle(client OpenRouterClient, converter StreamingService, lifecycleManager *StreamLifecycleManager) *DefaultStreamManager {
	sm := &DefaultStreamManager{
		streams:          make(map[string]*StreamContext),
		client:           client,
		converter:        converter,
		stopCleanup:      make(chan struct{}),
		lifecycleManager: lifecycleManager,
		resourceManager:  NewStreamResourceManager(),
		progressTrackers: make(map[string]*StreamProgressTracker),
	}

	// Start cleanup routine for stale streams
	sm.startCleanupRoutine()

	return sm
}

// StartStream initiates a new streaming request and returns a StreamResponse
func (sm *DefaultStreamManager) StartStream(ctx context.Context, req *StreamRequest) (*StreamResponse, error) {
	// Generate unique stream ID
	streamID := sm.generateStreamID()

	// Create progress tracker
	progressTracker := NewStreamProgressTracker(streamID, sm.lifecycleManager)
	sm.trackersMutex.Lock()
	sm.progressTrackers[streamID] = progressTracker
	sm.trackersMutex.Unlock()

	// Create stream context
	streamCtx := &StreamContext{
		StreamID:      streamID,
		RequestID:     req.RequestID,
		ClientIP:      req.ClientIP,
		StartTime:     time.Now(),
		Model:         req.Request.Model,
		ConnectionID:  req.ConnectionID,
		RetryCount:    0,
		UserAgent:     req.UserAgent,
		APIKey:        sm.sanitizeAPIKey(req.APIKey),
		Status:        StreamStatusStarting,
		LastActivity:  time.Now(),
		BytesStreamed: 0,
		ErrorCount:    0,
		Metadata:      make(map[string]string),
	}

	// Register stream
	sm.registerStream(streamID, streamCtx)

	// Record lifecycle event
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RecordEvent(streamID, StreamEventStarted, "Stream initialization started", nil, map[string]interface{}{
			"model":         req.Request.Model,
			"client_ip":     req.ClientIP,
			"connection_id": req.ConnectionID,
			"request_id":    req.RequestID,
		})
	}

	// Update metrics
	sm.updateMetrics(func(m *StreamMetrics) {
		m.ActiveStreams++
		m.TotalStreams++
	})

	// Start the streaming request
	resp, err := sm.client.SendStreamingRequest(ctx, req.Request, req.APIKey)
	if err != nil {
		// Record error and update status
		progressTracker.RecordError(err)
		sm.updateStreamStatus(streamID, StreamStatusFailed)
		sm.updateMetrics(func(m *StreamMetrics) {
			m.ActiveStreams--
			m.FailedStreams++
		})

		// Record lifecycle event
		if sm.lifecycleManager != nil {
			sm.lifecycleManager.RecordEvent(streamID, StreamEventFailed, "Failed to start streaming request", err, map[string]interface{}{
				"error_type": "upstream_connection_error",
			})
		}

		return nil, fmt.Errorf("failed to start streaming request: %w", err)
	}

	// Add response body as a resource to be cleaned up
	sm.resourceManager.AddResource(streamID, &httpResponseResource{resp.Body})

	// Convert the stream
	convertedReader, err := sm.converter.ConvertStream(ctx, resp.Body, req.Request.Model)
	if err != nil {
		resp.Body.Close()
		progressTracker.RecordError(err)
		sm.updateStreamStatus(streamID, StreamStatusFailed)
		sm.updateMetrics(func(m *StreamMetrics) {
			m.ActiveStreams--
			m.FailedStreams++
		})

		// Record lifecycle event
		if sm.lifecycleManager != nil {
			sm.lifecycleManager.RecordEvent(streamID, StreamEventFailed, "Failed to convert stream", err, map[string]interface{}{
				"error_type": "stream_conversion_error",
			})
		}

		return nil, fmt.Errorf("failed to convert stream: %w", err)
	}

	// Update stream status to active
	sm.updateStreamStatus(streamID, StreamStatusActive)

	// Record lifecycle event
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RecordEvent(streamID, StreamEventActive, "Stream is now active", nil, map[string]interface{}{
			"conversion_successful": true,
		})
	}

	// Wrap the reader to track stream completion
	wrappedReader := sm.wrapStreamReaderWithLifecycle(convertedReader, streamID)

	return &StreamResponse{
		StreamID: streamID,
		Reader:   wrappedReader,
		Context:  streamCtx,
	}, nil
}

// HandleStreamError handles errors that occur during streaming
func (sm *DefaultStreamManager) HandleStreamError(err error, streamID string) error {
	return sm.HandleStreamErrorWithLifecycle(err, streamID)
}

// CleanupStream removes a stream and cleans up its resources
func (sm *DefaultStreamManager) CleanupStream(streamID string) error {
	return sm.CleanupStreamWithLifecycle(streamID)
}

// GetStreamStatus returns the current status of a stream
func (sm *DefaultStreamManager) GetStreamStatus(streamID string) StreamStatus {
	sm.streamsMutex.RLock()
	defer sm.streamsMutex.RUnlock()

	if streamCtx, exists := sm.streams[streamID]; exists {
		return streamCtx.Status
	}

	return StreamStatus("")
}

// GetStreamContext returns the context for a specific stream
func (sm *DefaultStreamManager) GetStreamContext(streamID string) (*StreamContext, error) {
	sm.streamsMutex.RLock()
	defer sm.streamsMutex.RUnlock()

	if streamCtx, exists := sm.streams[streamID]; exists {
		// Return a copy to avoid race conditions
		contextCopy := *streamCtx
		return &contextCopy, nil
	}

	return nil, fmt.Errorf("stream %s not found", streamID)
}

// ListActiveStreams returns a list of all active stream IDs
func (sm *DefaultStreamManager) ListActiveStreams() []string {
	sm.streamsMutex.RLock()
	defer sm.streamsMutex.RUnlock()

	var activeStreams []string
	for streamID, streamCtx := range sm.streams {
		if streamCtx.Status == StreamStatusActive || streamCtx.Status == StreamStatusStarting {
			activeStreams = append(activeStreams, streamID)
		}
	}

	return activeStreams
}

// GetStreamMetrics returns current streaming metrics
func (sm *DefaultStreamManager) GetStreamMetrics() StreamMetrics {
	sm.metricsMutex.RLock()
	defer sm.metricsMutex.RUnlock()

	// Return a copy of metrics
	return sm.metrics
}

// Helper methods

// generateStreamID generates a unique stream identifier
func (sm *DefaultStreamManager) generateStreamID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return fmt.Sprintf("stream_%s", hex.EncodeToString(bytes))
}

// sanitizeAPIKey sanitizes API key for logging purposes
func (sm *DefaultStreamManager) sanitizeAPIKey(apiKey string) string {
	if len(apiKey) <= 8 {
		return "[REDACTED]"
	}
	return fmt.Sprintf("%s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
}

// registerStream adds a stream to the internal registry
func (sm *DefaultStreamManager) registerStream(streamID string, streamCtx *StreamContext) {
	sm.streamsMutex.Lock()
	defer sm.streamsMutex.Unlock()
	sm.streams[streamID] = streamCtx
}

// updateStreamStatus updates the status of a stream
func (sm *DefaultStreamManager) updateStreamStatus(streamID string, status StreamStatus) {
	sm.streamsMutex.Lock()
	defer sm.streamsMutex.Unlock()

	if streamCtx, exists := sm.streams[streamID]; exists {
		streamCtx.Status = status
		streamCtx.LastActivity = time.Now()
	}
}

// updateMetrics safely updates streaming metrics
func (sm *DefaultStreamManager) updateMetrics(updateFunc func(*StreamMetrics)) {
	sm.metricsMutex.Lock()
	defer sm.metricsMutex.Unlock()
	updateFunc(&sm.metrics)
}

// startCleanupRoutine starts a background routine to clean up stale streams
func (sm *DefaultStreamManager) startCleanupRoutine() {
	sm.cleanupTicker = time.NewTicker(30 * time.Second) // Run cleanup every 30 seconds

	go func() {
		for {
			select {
			case <-sm.cleanupTicker.C:
				sm.cleanupStaleStreams()
			case <-sm.stopCleanup:
				sm.cleanupTicker.Stop()
				return
			}
		}
	}()
}

// cleanupStaleStreams removes streams that have been inactive for too long
func (sm *DefaultStreamManager) cleanupStaleStreams() {
	sm.streamsMutex.Lock()
	defer sm.streamsMutex.Unlock()

	staleThreshold := 5 * time.Minute // Consider streams stale after 5 minutes of inactivity
	now := time.Now()

	var staleStreams []string
	for streamID, streamCtx := range sm.streams {
		if now.Sub(streamCtx.LastActivity) > staleThreshold {
			staleStreams = append(staleStreams, streamID)
		}
	}

	// Clean up stale streams
	for _, streamID := range staleStreams {
		if streamCtx, exists := sm.streams[streamID]; exists {
			// Update status to cancelled for stale streams
			streamCtx.Status = StreamStatusCancelled

			// Update metrics
			if streamCtx.Status == StreamStatusActive || streamCtx.Status == StreamStatusStarting {
				sm.updateMetrics(func(m *StreamMetrics) {
					m.ActiveStreams--
				})
			}

			delete(sm.streams, streamID)
		}
	}
}

// Stop gracefully shuts down the stream manager
func (sm *DefaultStreamManager) Stop() {
	// Stop cleanup routine
	close(sm.stopCleanup)

	// Clean up all remaining streams
	sm.streamsMutex.Lock()
	defer sm.streamsMutex.Unlock()

	for streamID := range sm.streams {
		delete(sm.streams, streamID)
	}

	// Reset metrics
	sm.metricsMutex.Lock()
	sm.metrics = StreamMetrics{}
	sm.metricsMutex.Unlock()
}

// GetStreamsByStatus returns streams filtered by status
func (sm *DefaultStreamManager) GetStreamsByStatus(status StreamStatus) []*StreamContext {
	sm.streamsMutex.RLock()
	defer sm.streamsMutex.RUnlock()

	var streams []*StreamContext
	for _, streamCtx := range sm.streams {
		if streamCtx.Status == status {
			// Return a copy to avoid race conditions
			contextCopy := *streamCtx
			streams = append(streams, &contextCopy)
		}
	}

	return streams
}

// GetStreamDuration returns the duration of a stream
func (sm *DefaultStreamManager) GetStreamDuration(streamID string) (time.Duration, error) {
	sm.streamsMutex.RLock()
	defer sm.streamsMutex.RUnlock()

	if streamCtx, exists := sm.streams[streamID]; exists {
		return time.Since(streamCtx.StartTime), nil
	}

	return 0, fmt.Errorf("stream %s not found", streamID)
}

// IsStreamActive checks if a stream is currently active
func (sm *DefaultStreamManager) IsStreamActive(streamID string) bool {
	status := sm.GetStreamStatus(streamID)
	return status == StreamStatusActive || status == StreamStatusStarting
}

// httpResponseResource implements StreamResource for HTTP response bodies
type httpResponseResource struct {
	body io.ReadCloser
}

func (r *httpResponseResource) Cleanup() error {
	if r.body != nil {
		return r.body.Close()
	}
	return nil
}

func (r *httpResponseResource) ResourceType() string {
	return "http_response_body"
}

// Enhanced HandleStreamError with lifecycle tracking
func (sm *DefaultStreamManager) HandleStreamErrorWithLifecycle(err error, streamID string) error {
	sm.streamsMutex.Lock()
	defer sm.streamsMutex.Unlock()

	streamCtx, exists := sm.streams[streamID]
	if !exists {
		return fmt.Errorf("stream %s not found", streamID)
	}

	// Update error count and last activity
	streamCtx.ErrorCount++
	streamCtx.LastActivity = time.Now()

	// Record error with progress tracker
	sm.trackersMutex.RLock()
	if tracker, exists := sm.progressTrackers[streamID]; exists {
		tracker.RecordError(err)
	}
	sm.trackersMutex.RUnlock()

	// Record lifecycle event
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RecordEvent(streamID, StreamEventError, "Stream error occurred", err, map[string]interface{}{
			"error_count":    streamCtx.ErrorCount,
			"bytes_streamed": streamCtx.BytesStreamed,
		})
	}

	// Update stream status based on error severity
	if streamCtx.ErrorCount >= 3 {
		streamCtx.Status = StreamStatusFailed
		sm.updateMetrics(func(m *StreamMetrics) {
			m.ActiveStreams--
			m.FailedStreams++
		})

		// Record failure event
		if sm.lifecycleManager != nil {
			sm.lifecycleManager.RecordEvent(streamID, StreamEventFailed, "Stream failed due to excessive errors", err, map[string]interface{}{
				"final_error_count": streamCtx.ErrorCount,
				"failure_reason":    "excessive_errors",
			})
		}

		// Trigger cleanup
		go sm.CleanupStreamWithLifecycle(streamID)
	}

	return nil
}

// Enhanced CleanupStream with lifecycle tracking and resource management
func (sm *DefaultStreamManager) CleanupStreamWithLifecycle(streamID string) error {
	sm.streamsMutex.Lock()
	streamCtx, exists := sm.streams[streamID]
	if !exists {
		sm.streamsMutex.Unlock()
		return fmt.Errorf("stream %s not found", streamID)
	}

	// Record cleanup start
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RecordEvent(streamID, StreamEventCleanup, "Starting stream cleanup", nil, map[string]interface{}{
			"final_status":   string(streamCtx.Status),
			"bytes_streamed": streamCtx.BytesStreamed,
			"duration_ms":    time.Since(streamCtx.StartTime).Milliseconds(),
		})
	}

	// Update metrics based on final status
	if streamCtx.Status == StreamStatusActive || streamCtx.Status == StreamStatusStarting {
		sm.updateMetrics(func(m *StreamMetrics) {
			m.ActiveStreams--
			if streamCtx.Status == StreamStatusActive {
				m.CompletedStreams++
			}
		})
	}

	// Calculate final metrics
	duration := time.Since(streamCtx.StartTime)
	sm.updateMetrics(func(m *StreamMetrics) {
		if m.CompletedStreams > 0 {
			// Update average latency
			totalLatency := m.AverageLatencyMs * (m.CompletedStreams - 1)
			m.AverageLatencyMs = (totalLatency + duration.Milliseconds()) / m.CompletedStreams
		} else {
			m.AverageLatencyMs = duration.Milliseconds()
		}
		m.TotalBytesStreamed += streamCtx.BytesStreamed
	})

	// Remove from streams map
	delete(sm.streams, streamID)
	sm.streamsMutex.Unlock()

	// Complete progress tracking
	sm.trackersMutex.Lock()
	if tracker, exists := sm.progressTrackers[streamID]; exists {
		tracker.RecordCompletion(streamCtx.Status == StreamStatusCompleted)
		delete(sm.progressTrackers, streamID)
	}
	sm.trackersMutex.Unlock()

	// Cleanup resources
	cleanupErrors := sm.resourceManager.CleanupResources(streamID)
	if len(cleanupErrors) > 0 {
		// Log cleanup errors but don't fail the cleanup process
		if sm.lifecycleManager != nil {
			for _, cleanupErr := range cleanupErrors {
				sm.lifecycleManager.RecordEvent(streamID, StreamEventError, "Resource cleanup error", cleanupErr, map[string]interface{}{
					"cleanup_phase": true,
				})
			}
		}
	}

	// Record final cleanup completion
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RecordEvent(streamID, StreamEventCleanup, "Stream cleanup completed", nil, map[string]interface{}{
			"cleanup_errors":    len(cleanupErrors),
			"resources_cleaned": len(sm.resourceManager.ListResourceTypes(streamID)),
		})
	}

	return nil
}

// GetStreamLifecycleEvents returns lifecycle events for a stream
func (sm *DefaultStreamManager) GetStreamLifecycleEvents(streamID string, limit int) []StreamLifecycleEvent {
	if sm.lifecycleManager != nil {
		return sm.lifecycleManager.GetEvents(streamID, limit)
	}
	return nil
}

// GetAllLifecycleEvents returns all lifecycle events
func (sm *DefaultStreamManager) GetAllLifecycleEvents(limit int) []StreamLifecycleEvent {
	if sm.lifecycleManager != nil {
		return sm.lifecycleManager.GetEvents("", limit)
	}
	return nil
}

// AddStreamEventListener adds a listener for stream events
func (sm *DefaultStreamManager) AddStreamEventListener(streamID string, listener StreamEventListener) {
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.AddListener(streamID, listener)
	}
}

// RemoveStreamEventListener removes a stream event listener
func (sm *DefaultStreamManager) RemoveStreamEventListener(streamID string, listener StreamEventListener) {
	if sm.lifecycleManager != nil {
		sm.lifecycleManager.RemoveListener(streamID, listener)
	}
}

// GetStreamProgress returns progress information for a stream
func (sm *DefaultStreamManager) GetStreamProgress(streamID string) (bytesProcessed, chunksProcessed, errorCount int64, duration time.Duration, throughput float64, err error) {
	sm.trackersMutex.RLock()
	defer sm.trackersMutex.RUnlock()

	if tracker, exists := sm.progressTrackers[streamID]; exists {
		bytesProcessed, chunksProcessed, errorCount, duration, throughput = tracker.GetProgress()
		return bytesProcessed, chunksProcessed, errorCount, duration, throughput, nil
	}

	return 0, 0, 0, 0, 0, fmt.Errorf("progress tracker not found for stream %s", streamID)
}

// Enhanced updateStreamActivity with progress tracking
func (sm *DefaultStreamManager) updateStreamActivityWithProgress(streamID string, bytesRead int64) {
	sm.streamsMutex.Lock()
	if streamCtx, exists := sm.streams[streamID]; exists {
		streamCtx.LastActivity = time.Now()
		streamCtx.BytesStreamed += bytesRead
	}
	sm.streamsMutex.Unlock()

	// Update progress tracker
	sm.trackersMutex.RLock()
	if tracker, exists := sm.progressTrackers[streamID]; exists {
		tracker.RecordProgress(bytesRead, 1) // 1 chunk processed
	}
	sm.trackersMutex.RUnlock()
}

// Enhanced streamReaderWrapper with progress tracking
type enhancedStreamReaderWrapper struct {
	io.Reader
	streamID  string
	manager   *DefaultStreamManager
	completed bool
	mutex     sync.Mutex
}

// Read implements io.Reader with enhanced progress tracking
func (w *enhancedStreamReaderWrapper) Read(p []byte) (n int, err error) {
	n, err = w.Reader.Read(p)

	// Update bytes streamed and progress
	if n > 0 {
		w.manager.updateStreamActivityWithProgress(w.streamID, int64(n))
	}

	// Handle stream completion
	if err == io.EOF {
		w.mutex.Lock()
		if !w.completed {
			w.completed = true
			w.manager.updateStreamStatus(w.streamID, StreamStatusCompleted)

			// Record completion event
			if w.manager.lifecycleManager != nil {
				w.manager.lifecycleManager.RecordEvent(w.streamID, StreamEventCompleted, "Stream completed successfully", nil, nil)
			}
		}
		w.mutex.Unlock()
	} else if err != nil {
		w.mutex.Lock()
		if !w.completed {
			w.completed = true
			w.manager.HandleStreamErrorWithLifecycle(err, w.streamID)
		}
		w.mutex.Unlock()
	}

	return n, err
}

// Close implements io.Closer with enhanced cleanup
func (w *enhancedStreamReaderWrapper) Close() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if !w.completed {
		w.completed = true
		w.manager.CleanupStreamWithLifecycle(w.streamID)
	}

	if closer, ok := w.Reader.(io.Closer); ok {
		return closer.Close()
	}

	return nil
}

// Enhanced wrapStreamReader with lifecycle tracking
func (sm *DefaultStreamManager) wrapStreamReaderWithLifecycle(reader io.Reader, streamID string) io.Reader {
	return &enhancedStreamReaderWrapper{
		Reader:    reader,
		streamID:  streamID,
		manager:   sm,
		completed: false,
	}
}
