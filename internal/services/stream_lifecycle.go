package services

import (
	"fmt"
	"sync"
	"time"
)

// StreamLifecycleEvent represents events in a stream's lifecycle
type StreamLifecycleEvent struct {
	StreamID  string
	EventType StreamEventType
	Timestamp time.Time
	Message   string
	Error     error
	Metadata  map[string]interface{}
}

// StreamEventType represents different types of stream events
type StreamEventType string

const (
	StreamEventStarted   StreamEventType = "started"
	StreamEventActive    StreamEventType = "active"
	StreamEventProgress  StreamEventType = "progress"
	StreamEventError     StreamEventType = "error"
	StreamEventCompleted StreamEventType = "completed"
	StreamEventFailed    StreamEventType = "failed"
	StreamEventCancelled StreamEventType = "cancelled"
	StreamEventTimeout   StreamEventType = "timeout"
	StreamEventCleanup   StreamEventType = "cleanup"
)

// StreamLifecycleManager manages the complete lifecycle of streams
type StreamLifecycleManager struct {
	events         []StreamLifecycleEvent
	eventsMutex    sync.RWMutex
	listeners      map[string][]StreamEventListener
	listenersMutex sync.RWMutex
	maxEvents      int
}

// StreamEventListener defines the interface for stream event listeners
type StreamEventListener interface {
	OnStreamEvent(event StreamLifecycleEvent)
}

// StreamEventListenerFunc is a function type that implements StreamEventListener
type StreamEventListenerFunc func(event StreamLifecycleEvent)

// OnStreamEvent implements StreamEventListener
func (f StreamEventListenerFunc) OnStreamEvent(event StreamLifecycleEvent) {
	f(event)
}

// NewStreamLifecycleManager creates a new stream lifecycle manager
func NewStreamLifecycleManager(maxEvents int) *StreamLifecycleManager {
	if maxEvents <= 0 {
		maxEvents = 1000 // Default to keeping last 1000 events
	}

	return &StreamLifecycleManager{
		events:    make([]StreamLifecycleEvent, 0, maxEvents),
		listeners: make(map[string][]StreamEventListener),
		maxEvents: maxEvents,
	}
}

// RecordEvent records a lifecycle event for a stream
func (slm *StreamLifecycleManager) RecordEvent(streamID string, eventType StreamEventType, message string, err error, metadata map[string]interface{}) {
	event := StreamLifecycleEvent{
		StreamID:  streamID,
		EventType: eventType,
		Timestamp: time.Now(),
		Message:   message,
		Error:     err,
		Metadata:  metadata,
	}

	// Store the event
	slm.eventsMutex.Lock()
	slm.events = append(slm.events, event)

	// Trim events if we exceed max capacity
	if len(slm.events) > slm.maxEvents {
		// Remove oldest events, keep the most recent ones
		copy(slm.events, slm.events[len(slm.events)-slm.maxEvents:])
		slm.events = slm.events[:slm.maxEvents]
	}
	slm.eventsMutex.Unlock()

	// Notify listeners
	slm.notifyListeners(event)
}

// AddListener adds an event listener for a specific stream or all streams
func (slm *StreamLifecycleManager) AddListener(streamID string, listener StreamEventListener) {
	slm.listenersMutex.Lock()
	defer slm.listenersMutex.Unlock()

	if slm.listeners[streamID] == nil {
		slm.listeners[streamID] = make([]StreamEventListener, 0)
	}
	slm.listeners[streamID] = append(slm.listeners[streamID], listener)
}

// RemoveListener removes an event listener
func (slm *StreamLifecycleManager) RemoveListener(streamID string, listener StreamEventListener) {
	slm.listenersMutex.Lock()
	defer slm.listenersMutex.Unlock()

	listeners := slm.listeners[streamID]
	for i, l := range listeners {
		// Compare function pointers (this is a simplified comparison)
		if fmt.Sprintf("%p", l) == fmt.Sprintf("%p", listener) {
			slm.listeners[streamID] = append(listeners[:i], listeners[i+1:]...)
			break
		}
	}
}

// GetEvents returns events for a specific stream or all events if streamID is empty
func (slm *StreamLifecycleManager) GetEvents(streamID string, limit int) []StreamLifecycleEvent {
	slm.eventsMutex.RLock()
	defer slm.eventsMutex.RUnlock()

	var filteredEvents []StreamLifecycleEvent

	if streamID == "" {
		// Return all events
		filteredEvents = make([]StreamLifecycleEvent, len(slm.events))
		copy(filteredEvents, slm.events)
	} else {
		// Filter events for specific stream
		for _, event := range slm.events {
			if event.StreamID == streamID {
				filteredEvents = append(filteredEvents, event)
			}
		}
	}

	// Apply limit if specified
	if limit > 0 && len(filteredEvents) > limit {
		// Return the most recent events
		start := len(filteredEvents) - limit
		filteredEvents = filteredEvents[start:]
	}

	return filteredEvents
}

// GetEventsByType returns events of a specific type
func (slm *StreamLifecycleManager) GetEventsByType(eventType StreamEventType, limit int) []StreamLifecycleEvent {
	slm.eventsMutex.RLock()
	defer slm.eventsMutex.RUnlock()

	var filteredEvents []StreamLifecycleEvent

	for _, event := range slm.events {
		if event.EventType == eventType {
			filteredEvents = append(filteredEvents, event)
		}
	}

	// Apply limit if specified
	if limit > 0 && len(filteredEvents) > limit {
		// Return the most recent events
		start := len(filteredEvents) - limit
		filteredEvents = filteredEvents[start:]
	}

	return filteredEvents
}

// notifyListeners notifies all relevant listeners about an event
func (slm *StreamLifecycleManager) notifyListeners(event StreamLifecycleEvent) {
	slm.listenersMutex.RLock()
	defer slm.listenersMutex.RUnlock()

	// Notify listeners for this specific stream
	if listeners, exists := slm.listeners[event.StreamID]; exists {
		for _, listener := range listeners {
			go listener.OnStreamEvent(event)
		}
	}

	// Notify global listeners (registered with empty streamID)
	if globalListeners, exists := slm.listeners[""]; exists {
		for _, listener := range globalListeners {
			go listener.OnStreamEvent(event)
		}
	}
}

// ClearEvents clears all stored events
func (slm *StreamLifecycleManager) ClearEvents() {
	slm.eventsMutex.Lock()
	defer slm.eventsMutex.Unlock()
	slm.events = slm.events[:0]
}

// ClearEventsForStream clears events for a specific stream
func (slm *StreamLifecycleManager) ClearEventsForStream(streamID string) {
	slm.eventsMutex.Lock()
	defer slm.eventsMutex.Unlock()

	var filteredEvents []StreamLifecycleEvent
	for _, event := range slm.events {
		if event.StreamID != streamID {
			filteredEvents = append(filteredEvents, event)
		}
	}
	slm.events = filteredEvents
}

// StreamProgressTracker tracks progress and performance metrics for streams
type StreamProgressTracker struct {
	streamID         string
	startTime        time.Time
	lastProgressTime time.Time
	bytesProcessed   int64
	chunksProcessed  int64
	errorCount       int64
	lifecycleManager *StreamLifecycleManager
	mutex            sync.RWMutex
}

// NewStreamProgressTracker creates a new progress tracker for a stream
func NewStreamProgressTracker(streamID string, lifecycleManager *StreamLifecycleManager) *StreamProgressTracker {
	tracker := &StreamProgressTracker{
		streamID:         streamID,
		startTime:        time.Now(),
		lastProgressTime: time.Now(),
		lifecycleManager: lifecycleManager,
	}

	// Record stream start event
	if lifecycleManager != nil {
		lifecycleManager.RecordEvent(streamID, StreamEventStarted, "Stream started", nil, map[string]interface{}{
			"start_time": tracker.startTime,
		})
	}

	return tracker
}

// RecordProgress records progress for the stream
func (spt *StreamProgressTracker) RecordProgress(bytesProcessed, chunksProcessed int64) {
	spt.mutex.Lock()
	defer spt.mutex.Unlock()

	spt.bytesProcessed += bytesProcessed
	spt.chunksProcessed += chunksProcessed
	spt.lastProgressTime = time.Now()

	// Record progress event periodically (every 100 chunks or 10KB)
	if spt.chunksProcessed%100 == 0 || spt.bytesProcessed%10240 == 0 {
		if spt.lifecycleManager != nil {
			spt.lifecycleManager.RecordEvent(spt.streamID, StreamEventProgress, "Stream progress update", nil, map[string]interface{}{
				"bytes_processed":  spt.bytesProcessed,
				"chunks_processed": spt.chunksProcessed,
				"duration_ms":      time.Since(spt.startTime).Milliseconds(),
				"throughput_bps":   spt.calculateThroughput(),
			})
		}
	}
}

// RecordError records an error for the stream
func (spt *StreamProgressTracker) RecordError(err error) {
	spt.mutex.Lock()
	defer spt.mutex.Unlock()

	spt.errorCount++

	if spt.lifecycleManager != nil {
		spt.lifecycleManager.RecordEvent(spt.streamID, StreamEventError, "Stream error occurred", err, map[string]interface{}{
			"error_count":      spt.errorCount,
			"bytes_processed":  spt.bytesProcessed,
			"chunks_processed": spt.chunksProcessed,
		})
	}
}

// RecordCompletion records the completion of the stream
func (spt *StreamProgressTracker) RecordCompletion(success bool) {
	spt.mutex.Lock()
	defer spt.mutex.Unlock()

	eventType := StreamEventCompleted
	message := "Stream completed successfully"

	if !success {
		eventType = StreamEventFailed
		message = "Stream failed"
	}

	if spt.lifecycleManager != nil {
		spt.lifecycleManager.RecordEvent(spt.streamID, eventType, message, nil, map[string]interface{}{
			"success":        success,
			"total_bytes":    spt.bytesProcessed,
			"total_chunks":   spt.chunksProcessed,
			"total_errors":   spt.errorCount,
			"duration_ms":    time.Since(spt.startTime).Milliseconds(),
			"avg_throughput": spt.calculateThroughput(),
		})
	}
}

// GetProgress returns current progress information
func (spt *StreamProgressTracker) GetProgress() (bytesProcessed, chunksProcessed, errorCount int64, duration time.Duration, throughput float64) {
	spt.mutex.RLock()
	defer spt.mutex.RUnlock()

	return spt.bytesProcessed, spt.chunksProcessed, spt.errorCount, time.Since(spt.startTime), spt.calculateThroughput()
}

// calculateThroughput calculates the current throughput in bytes per second
func (spt *StreamProgressTracker) calculateThroughput() float64 {
	duration := time.Since(spt.startTime)
	if duration.Seconds() == 0 {
		return 0
	}
	return float64(spt.bytesProcessed) / duration.Seconds()
}

// StreamResourceManager manages resources associated with streams
type StreamResourceManager struct {
	resources map[string][]StreamResource
	mutex     sync.RWMutex
}

// StreamResource represents a resource that needs cleanup
type StreamResource interface {
	Cleanup() error
	ResourceType() string
}

// NewStreamResourceManager creates a new resource manager
func NewStreamResourceManager() *StreamResourceManager {
	return &StreamResourceManager{
		resources: make(map[string][]StreamResource),
	}
}

// AddResource adds a resource to be managed for a stream
func (srm *StreamResourceManager) AddResource(streamID string, resource StreamResource) {
	srm.mutex.Lock()
	defer srm.mutex.Unlock()

	if srm.resources[streamID] == nil {
		srm.resources[streamID] = make([]StreamResource, 0)
	}
	srm.resources[streamID] = append(srm.resources[streamID], resource)
}

// CleanupResources cleans up all resources for a stream
func (srm *StreamResourceManager) CleanupResources(streamID string) []error {
	srm.mutex.Lock()
	defer srm.mutex.Unlock()

	resources := srm.resources[streamID]
	var errors []error

	for _, resource := range resources {
		if err := resource.Cleanup(); err != nil {
			errors = append(errors, fmt.Errorf("failed to cleanup %s: %w", resource.ResourceType(), err))
		}
	}

	// Remove resources after cleanup
	delete(srm.resources, streamID)

	return errors
}

// GetResourceCount returns the number of resources for a stream
func (srm *StreamResourceManager) GetResourceCount(streamID string) int {
	srm.mutex.RLock()
	defer srm.mutex.RUnlock()

	return len(srm.resources[streamID])
}

// ListResourceTypes returns the types of resources for a stream
func (srm *StreamResourceManager) ListResourceTypes(streamID string) []string {
	srm.mutex.RLock()
	defer srm.mutex.RUnlock()

	resources := srm.resources[streamID]
	var types []string

	for _, resource := range resources {
		types = append(types, resource.ResourceType())
	}

	return types
}

// Enhanced DefaultStreamManager with lifecycle management
func (sm *DefaultStreamManager) WithLifecycleManager(lifecycleManager *StreamLifecycleManager) *DefaultStreamManager {
	sm.lifecycleManager = lifecycleManager
	return sm
}

// Add lifecycle manager field to DefaultStreamManager (this would be added to the struct definition)
// lifecycleManager *StreamLifecycleManager
// resourceManager  *StreamResourceManager

// Enhanced stream management methods that integrate with lifecycle tracking
// This is an example method showing how lifecycle tracking would be integrated
// The actual implementation is in the DefaultStreamManager.StartStream method
