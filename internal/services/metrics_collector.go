package services

import (
	"fmt"
	"sync"
	"time"
)

// MetricType represents the type of metric being collected
type MetricType string

const (
	MetricTypeCounter   MetricType = "counter"
	MetricTypeGauge     MetricType = "gauge"
	MetricTypeHistogram MetricType = "histogram"
	MetricTypeTiming    MetricType = "timing"
)

// Metric represents a single metric data point
type Metric struct {
	Name      string                 `json:"name"`
	Type      MetricType             `json:"type"`
	Value     float64                `json:"value"`
	Labels    map[string]string      `json:"labels,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
}

// StreamingOperationMetrics holds metrics specific to streaming operations
type StreamingOperationMetrics struct {
	StreamsStarted   int64     `json:"streams_started"`
	StreamsCompleted int64     `json:"streams_completed"`
	StreamsFailed    int64     `json:"streams_failed"`
	TotalDuration    float64   `json:"total_duration_ms"`
	AverageDuration  float64   `json:"average_duration_ms"`
	LastUpdated      time.Time `json:"last_updated"`
	SuccessRate      float64   `json:"success_rate"`
	ErrorRate        float64   `json:"error_rate"`
}

// ConnectionMetrics holds metrics for connection health and performance
type ConnectionMetrics struct {
	ActiveConnections     int64     `json:"active_connections"`
	TotalConnections      int64     `json:"total_connections"`
	FailedConnections     int64     `json:"failed_connections"`
	HealthyConnections    int64     `json:"healthy_connections"`
	AverageLatency        float64   `json:"average_latency_ms"`
	ConnectionDuration    float64   `json:"average_connection_duration_ms"`
	LastHealthCheck       time.Time `json:"last_health_check"`
	ConnectionSuccessRate float64   `json:"connection_success_rate"`
}

// BufferMetrics holds metrics for buffer operations and performance
type BufferMetrics struct {
	TotalChunksProcessed  int64     `json:"total_chunks_processed"`
	ChunksBuffered        int64     `json:"chunks_buffered"`
	BufferOverflows       int64     `json:"buffer_overflows"`
	AverageChunkSize      float64   `json:"average_chunk_size_bytes"`
	AverageProcessingTime float64   `json:"average_processing_time_ms"`
	BufferUtilization     float64   `json:"buffer_utilization_percent"`
	LastProcessed         time.Time `json:"last_processed"`
}

// MetricsCollector defines the interface for collecting and managing metrics
type MetricsCollector interface {
	// Stream metrics
	RecordStreamStart(streamID, model string) error
	RecordStreamComplete(streamID string, duration time.Duration) error
	RecordStreamError(streamID, errorType string, duration time.Duration) error
	GetStreamMetrics() *StreamingOperationMetrics

	// Connection metrics
	RecordConnectionStart(connectionID string) error
	RecordConnectionEnd(connectionID string, duration time.Duration, success bool) error
	RecordConnectionLatency(connectionID string, latency time.Duration) error
	RecordConnectionHealth(connectionID string, isHealthy bool) error
	GetConnectionMetrics() *ConnectionMetrics

	// Buffer metrics
	RecordChunkProcessed(chunkSize int, processingTime time.Duration) error
	RecordBufferOverflow(bufferSize int) error
	RecordBufferUtilization(utilizationPercent float64) error
	GetBufferMetrics() *BufferMetrics

	// General metrics
	IncrementCounter(name string, labels map[string]string) error
	SetGauge(name string, value float64, labels map[string]string) error
	RecordTiming(name string, duration time.Duration, labels map[string]string) error
	GetMetric(name string) (*Metric, error)
	GetAllMetrics() ([]*Metric, error)

	// Metrics export
	ExportMetrics() (map[string]interface{}, error)
	ResetMetrics() error
}

// DefaultMetricsCollector implements the MetricsCollector interface
type DefaultMetricsCollector struct {
	mu                sync.RWMutex
	metrics           map[string]*Metric
	streamMetrics     *StreamingOperationMetrics
	connectionMetrics *ConnectionMetrics
	bufferMetrics     *BufferMetrics
	activeStreams     map[string]time.Time
	activeConnections map[string]time.Time
}

// NewMetricsCollector creates a new metrics collector instance
func NewMetricsCollector() MetricsCollector {
	return &DefaultMetricsCollector{
		metrics:           make(map[string]*Metric),
		streamMetrics:     &StreamingOperationMetrics{LastUpdated: time.Now()},
		connectionMetrics: &ConnectionMetrics{LastHealthCheck: time.Now()},
		bufferMetrics:     &BufferMetrics{LastProcessed: time.Now()},
		activeStreams:     make(map[string]time.Time),
		activeConnections: make(map[string]time.Time),
	}
}

// Stream metrics implementation

// RecordStreamStart records the start of a streaming operation
func (mc *DefaultMetricsCollector) RecordStreamStart(streamID, model string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.activeStreams[streamID] = time.Now()
	mc.streamMetrics.StreamsStarted++
	mc.streamMetrics.LastUpdated = time.Now()

	return mc.IncrementCounter("streams_started_total", map[string]string{
		"model": model,
	})
}

// RecordStreamComplete records the successful completion of a streaming operation
func (mc *DefaultMetricsCollector) RecordStreamComplete(streamID string, duration time.Duration) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	delete(mc.activeStreams, streamID)
	mc.streamMetrics.StreamsCompleted++
	mc.streamMetrics.TotalDuration += float64(duration.Milliseconds())
	mc.updateStreamAverages()

	return mc.RecordTiming("stream_duration", duration, map[string]string{
		"status": "completed",
	})
}

// RecordStreamError records a streaming operation failure
func (mc *DefaultMetricsCollector) RecordStreamError(streamID, errorType string, duration time.Duration) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	delete(mc.activeStreams, streamID)
	mc.streamMetrics.StreamsFailed++
	mc.streamMetrics.TotalDuration += float64(duration.Milliseconds())
	mc.updateStreamAverages()

	return mc.IncrementCounter("streams_failed_total", map[string]string{
		"error_type": errorType,
	})
}

// GetStreamMetrics returns current stream metrics
func (mc *DefaultMetricsCollector) GetStreamMetrics() *StreamingOperationMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Create a copy to avoid race conditions
	metrics := *mc.streamMetrics
	return &metrics
}

// Connection metrics implementation

// RecordConnectionStart records the start of a connection
func (mc *DefaultMetricsCollector) RecordConnectionStart(connectionID string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.activeConnections[connectionID] = time.Now()
	mc.connectionMetrics.ActiveConnections++
	mc.connectionMetrics.TotalConnections++

	return mc.SetGauge("active_connections", float64(mc.connectionMetrics.ActiveConnections), nil)
}

// RecordConnectionEnd records the end of a connection
func (mc *DefaultMetricsCollector) RecordConnectionEnd(connectionID string, duration time.Duration, success bool) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	delete(mc.activeConnections, connectionID)
	mc.connectionMetrics.ActiveConnections--

	if !success {
		mc.connectionMetrics.FailedConnections++
	}

	mc.updateConnectionAverages(duration)

	status := "success"
	if !success {
		status = "failed"
	}

	return mc.RecordTiming("connection_duration", duration, map[string]string{
		"status": status,
	})
}

// RecordConnectionLatency records connection latency
func (mc *DefaultMetricsCollector) RecordConnectionLatency(connectionID string, latency time.Duration) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	// Update running average (simple moving average)
	if mc.connectionMetrics.AverageLatency == 0 {
		mc.connectionMetrics.AverageLatency = float64(latency.Milliseconds())
	} else {
		mc.connectionMetrics.AverageLatency = (mc.connectionMetrics.AverageLatency + float64(latency.Milliseconds())) / 2
	}

	return mc.RecordTiming("connection_latency", latency, map[string]string{
		"connection_id": connectionID,
	})
}

// RecordConnectionHealth records connection health status
func (mc *DefaultMetricsCollector) RecordConnectionHealth(connectionID string, isHealthy bool) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.connectionMetrics.LastHealthCheck = time.Now()

	if isHealthy {
		mc.connectionMetrics.HealthyConnections++
	}

	mc.updateConnectionSuccessRate()

	healthValue := 0.0
	if isHealthy {
		healthValue = 1.0
	}

	return mc.SetGauge("connection_health", healthValue, map[string]string{
		"connection_id": connectionID,
	})
}

// GetConnectionMetrics returns current connection metrics
func (mc *DefaultMetricsCollector) GetConnectionMetrics() *ConnectionMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Create a copy to avoid race conditions
	metrics := *mc.connectionMetrics
	return &metrics
}

// Buffer metrics implementation

// RecordChunkProcessed records processing of a data chunk
func (mc *DefaultMetricsCollector) RecordChunkProcessed(chunkSize int, processingTime time.Duration) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.bufferMetrics.TotalChunksProcessed++
	mc.bufferMetrics.LastProcessed = time.Now()

	// Update average chunk size
	if mc.bufferMetrics.AverageChunkSize == 0 {
		mc.bufferMetrics.AverageChunkSize = float64(chunkSize)
	} else {
		mc.bufferMetrics.AverageChunkSize = (mc.bufferMetrics.AverageChunkSize + float64(chunkSize)) / 2
	}

	// Update average processing time
	processingTimeMs := float64(processingTime.Milliseconds())
	if mc.bufferMetrics.AverageProcessingTime == 0 {
		mc.bufferMetrics.AverageProcessingTime = processingTimeMs
	} else {
		mc.bufferMetrics.AverageProcessingTime = (mc.bufferMetrics.AverageProcessingTime + processingTimeMs) / 2
	}

	return mc.RecordTiming("chunk_processing_time", processingTime, map[string]string{
		"chunk_size": string(rune(chunkSize)),
	})
}

// RecordBufferOverflow records a buffer overflow event
func (mc *DefaultMetricsCollector) RecordBufferOverflow(bufferSize int) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.bufferMetrics.BufferOverflows++

	return mc.IncrementCounter("buffer_overflows_total", map[string]string{
		"buffer_size": string(rune(bufferSize)),
	})
}

// RecordBufferUtilization records current buffer utilization
func (mc *DefaultMetricsCollector) RecordBufferUtilization(utilizationPercent float64) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.bufferMetrics.BufferUtilization = utilizationPercent

	return mc.SetGauge("buffer_utilization_percent", utilizationPercent, nil)
}

// GetBufferMetrics returns current buffer metrics
func (mc *DefaultMetricsCollector) GetBufferMetrics() *BufferMetrics {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	// Create a copy to avoid race conditions
	metrics := *mc.bufferMetrics
	return &metrics
}

// General metrics implementation

// IncrementCounter increments a counter metric
func (mc *DefaultMetricsCollector) IncrementCounter(name string, labels map[string]string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := mc.buildMetricKey(name, labels)

	if metric, exists := mc.metrics[key]; exists {
		metric.Value++
		metric.Timestamp = time.Now()
	} else {
		mc.metrics[key] = &Metric{
			Name:      name,
			Type:      MetricTypeCounter,
			Value:     1,
			Labels:    labels,
			Timestamp: time.Now(),
		}
	}

	return nil
}

// SetGauge sets a gauge metric value
func (mc *DefaultMetricsCollector) SetGauge(name string, value float64, labels map[string]string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := mc.buildMetricKey(name, labels)

	mc.metrics[key] = &Metric{
		Name:      name,
		Type:      MetricTypeGauge,
		Value:     value,
		Labels:    labels,
		Timestamp: time.Now(),
	}

	return nil
}

// RecordTiming records a timing metric
func (mc *DefaultMetricsCollector) RecordTiming(name string, duration time.Duration, labels map[string]string) error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	key := mc.buildMetricKey(name, labels)

	mc.metrics[key] = &Metric{
		Name:      name,
		Type:      MetricTypeTiming,
		Value:     float64(duration.Milliseconds()),
		Labels:    labels,
		Timestamp: time.Now(),
		Fields: map[string]interface{}{
			"duration_ns": duration.Nanoseconds(),
			"duration_ms": duration.Milliseconds(),
		},
	}

	return nil
}

// GetMetric retrieves a specific metric
func (mc *DefaultMetricsCollector) GetMetric(name string) (*Metric, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	for _, metric := range mc.metrics {
		if metric.Name == name {
			// Return a copy
			metricCopy := *metric
			return &metricCopy, nil
		}
	}

	return nil, fmt.Errorf("metric not found: %s", name)
}

// GetAllMetrics returns all collected metrics
func (mc *DefaultMetricsCollector) GetAllMetrics() ([]*Metric, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	metrics := make([]*Metric, 0, len(mc.metrics))
	for _, metric := range mc.metrics {
		// Create a copy
		metricCopy := *metric
		metrics = append(metrics, &metricCopy)
	}

	return metrics, nil
}

// ExportMetrics exports all metrics in a structured format
func (mc *DefaultMetricsCollector) ExportMetrics() (map[string]interface{}, error) {
	mc.mu.RLock()
	defer mc.mu.RUnlock()

	export := map[string]interface{}{
		"timestamp":          time.Now(),
		"stream_metrics":     mc.streamMetrics,
		"connection_metrics": mc.connectionMetrics,
		"buffer_metrics":     mc.bufferMetrics,
		"raw_metrics":        mc.metrics,
	}

	return export, nil
}

// ResetMetrics clears all collected metrics
func (mc *DefaultMetricsCollector) ResetMetrics() error {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	mc.metrics = make(map[string]*Metric)
	mc.streamMetrics = &StreamingOperationMetrics{LastUpdated: time.Now()}
	mc.connectionMetrics = &ConnectionMetrics{LastHealthCheck: time.Now()}
	mc.bufferMetrics = &BufferMetrics{LastProcessed: time.Now()}
	mc.activeStreams = make(map[string]time.Time)
	mc.activeConnections = make(map[string]time.Time)

	return nil
}

// Helper methods

// buildMetricKey creates a unique key for a metric with labels
func (mc *DefaultMetricsCollector) buildMetricKey(name string, labels map[string]string) string {
	key := name
	for k, v := range labels {
		key += fmt.Sprintf("_%s_%s", k, v)
	}
	return key
}

// updateStreamAverages updates calculated stream metrics
func (mc *DefaultMetricsCollector) updateStreamAverages() {
	totalStreams := mc.streamMetrics.StreamsStarted
	if totalStreams > 0 {
		mc.streamMetrics.AverageDuration = mc.streamMetrics.TotalDuration / float64(totalStreams)
		mc.streamMetrics.SuccessRate = float64(mc.streamMetrics.StreamsCompleted) / float64(totalStreams) * 100
		mc.streamMetrics.ErrorRate = float64(mc.streamMetrics.StreamsFailed) / float64(totalStreams) * 100
	}
	mc.streamMetrics.LastUpdated = time.Now()
}

// updateConnectionAverages updates calculated connection metrics
func (mc *DefaultMetricsCollector) updateConnectionAverages(duration time.Duration) {
	if mc.connectionMetrics.ConnectionDuration == 0 {
		mc.connectionMetrics.ConnectionDuration = float64(duration.Milliseconds())
	} else {
		mc.connectionMetrics.ConnectionDuration = (mc.connectionMetrics.ConnectionDuration + float64(duration.Milliseconds())) / 2
	}
}

// updateConnectionSuccessRate updates connection success rate
func (mc *DefaultMetricsCollector) updateConnectionSuccessRate() {
	if mc.connectionMetrics.TotalConnections > 0 {
		successfulConnections := mc.connectionMetrics.TotalConnections - mc.connectionMetrics.FailedConnections
		mc.connectionMetrics.ConnectionSuccessRate = float64(successfulConnections) / float64(mc.connectionMetrics.TotalConnections) * 100
	}
}
