package services

import (
	"context"
	"fmt"
	"time"
)

// MonitoringService provides comprehensive monitoring capabilities for streaming operations
type MonitoringService interface {
	// Stream monitoring
	StartStreamMonitoring(ctx context.Context, requestID, clientIP, model, userAgent string) (*StreamingContext, error)
	RecordStreamProgress(streamCtx *StreamingContext, event string, data map[string]interface{}) error
	CompleteStreamMonitoring(streamCtx *StreamingContext) error
	FailStreamMonitoring(streamCtx *StreamingContext, err error, errorType string) error

	// Connection monitoring
	StartConnectionMonitoring(connectionID string) error
	RecordConnectionLatency(connectionID string, latency time.Duration) error
	RecordConnectionHealth(connectionID string, isHealthy bool, healthData map[string]interface{}) error
	EndConnectionMonitoring(connectionID string, success bool) error

	// Buffer monitoring
	RecordBufferOperation(operation string, chunkSize int, processingTime time.Duration) error
	RecordBufferOverflow(bufferSize int, reason string) error
	RecordBufferUtilization(utilizationPercent float64) error

	// Error monitoring
	RecordError(streamCtx *StreamingContext, errorType string, err error, context map[string]interface{}) error
	RecordRecovery(streamCtx *StreamingContext, recoveryType string, context map[string]interface{}) error

	// Health and status
	GetHealthStatus() (*HealthStatus, error)
	GetMetricsSummary() (*MetricsSummary, error)
	ExportMonitoringData() (map[string]interface{}, error)
}

// HealthStatus represents the overall health status of the system
type HealthStatus struct {
	Overall          string                 `json:"overall"`
	StreamHealth     string                 `json:"stream_health"`
	ConnectionHealth string                 `json:"connection_health"`
	BufferHealth     string                 `json:"buffer_health"`
	LastCheck        time.Time              `json:"last_check"`
	Issues           []string               `json:"issues,omitempty"`
	Recommendations  []string               `json:"recommendations,omitempty"`
	Details          map[string]interface{} `json:"details,omitempty"`
}

// MetricsSummary provides a summary of key metrics
type MetricsSummary struct {
	ActiveStreams     int64     `json:"active_streams"`
	TotalStreams      int64     `json:"total_streams"`
	SuccessRate       float64   `json:"success_rate"`
	ErrorRate         float64   `json:"error_rate"`
	AverageLatency    float64   `json:"average_latency_ms"`
	ActiveConnections int64     `json:"active_connections"`
	BufferUtilization float64   `json:"buffer_utilization"`
	LastUpdated       time.Time `json:"last_updated"`
}

// DefaultMonitoringService implements the MonitoringService interface
type DefaultMonitoringService struct {
	logger    StructuredLogger
	metrics   MetricsCollector
	startTime time.Time
}

// NewMonitoringService creates a new monitoring service instance
func NewMonitoringService(logger StructuredLogger, metrics MetricsCollector) MonitoringService {
	return &DefaultMonitoringService{
		logger:    logger,
		metrics:   metrics,
		startTime: time.Now(),
	}
}

// Stream monitoring implementation

// StartStreamMonitoring begins monitoring a streaming operation
func (ms *DefaultMonitoringService) StartStreamMonitoring(ctx context.Context, requestID, clientIP, model, userAgent string) (*StreamingContext, error) {
	// Create stream context
	streamCtx := ms.logger.CreateStreamContext(requestID, clientIP, model)
	streamCtx.UserAgent = userAgent

	// Log stream start
	if err := ms.logger.LogStreamStart(streamCtx); err != nil {
		return nil, fmt.Errorf("failed to log stream start: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordStreamStart(streamCtx.StreamID, model); err != nil {
		return nil, fmt.Errorf("failed to record stream start metrics: %w", err)
	}

	return streamCtx, nil
}

// RecordStreamProgress records progress events during streaming
func (ms *DefaultMonitoringService) RecordStreamProgress(streamCtx *StreamingContext, event string, data map[string]interface{}) error {
	// Log progress
	message := fmt.Sprintf("stream progress: %s", event)
	if err := ms.logger.LogStreamProgress(streamCtx, message, data); err != nil {
		return fmt.Errorf("failed to log stream progress: %w", err)
	}

	return nil
}

// CompleteStreamMonitoring completes monitoring for a successful stream
func (ms *DefaultMonitoringService) CompleteStreamMonitoring(streamCtx *StreamingContext) error {
	duration := time.Since(streamCtx.StartTime)

	// Log completion
	if err := ms.logger.LogStreamComplete(streamCtx, duration); err != nil {
		return fmt.Errorf("failed to log stream completion: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordStreamComplete(streamCtx.StreamID, duration); err != nil {
		return fmt.Errorf("failed to record stream completion metrics: %w", err)
	}

	return nil
}

// FailStreamMonitoring completes monitoring for a failed stream
func (ms *DefaultMonitoringService) FailStreamMonitoring(streamCtx *StreamingContext, err error, errorType string) error {
	duration := time.Since(streamCtx.StartTime)

	// Log error
	errorFields := map[string]interface{}{
		"error_type": errorType,
		"duration":   duration.String(),
	}
	if logErr := ms.logger.LogStreamError(streamCtx, err, errorFields); logErr != nil {
		return fmt.Errorf("failed to log stream error: %w", logErr)
	}

	// Record metrics
	if metricsErr := ms.metrics.RecordStreamError(streamCtx.StreamID, errorType, duration); metricsErr != nil {
		return fmt.Errorf("failed to record stream error metrics: %w", metricsErr)
	}

	return nil
}

// Connection monitoring implementation

// StartConnectionMonitoring begins monitoring a connection
func (ms *DefaultMonitoringService) StartConnectionMonitoring(connectionID string) error {
	// Log connection start
	if err := ms.logger.LogConnectionEvent(nil, "connection_start", map[string]interface{}{
		"connection_id": connectionID,
	}); err != nil {
		return fmt.Errorf("failed to log connection start: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordConnectionStart(connectionID); err != nil {
		return fmt.Errorf("failed to record connection start metrics: %w", err)
	}

	return nil
}

// RecordConnectionLatency records connection latency
func (ms *DefaultMonitoringService) RecordConnectionLatency(connectionID string, latency time.Duration) error {
	// Log latency
	if err := ms.logger.LogConnectionEvent(nil, "connection_latency", map[string]interface{}{
		"connection_id": connectionID,
		"latency_ms":    latency.Milliseconds(),
	}); err != nil {
		return fmt.Errorf("failed to log connection latency: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordConnectionLatency(connectionID, latency); err != nil {
		return fmt.Errorf("failed to record connection latency metrics: %w", err)
	}

	return nil
}

// RecordConnectionHealth records connection health status
func (ms *DefaultMonitoringService) RecordConnectionHealth(connectionID string, isHealthy bool, healthData map[string]interface{}) error {
	// Log health check
	if err := ms.logger.LogConnectionHealth(connectionID, isHealthy, healthData); err != nil {
		return fmt.Errorf("failed to log connection health: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordConnectionHealth(connectionID, isHealthy); err != nil {
		return fmt.Errorf("failed to record connection health metrics: %w", err)
	}

	return nil
}

// EndConnectionMonitoring ends monitoring for a connection
func (ms *DefaultMonitoringService) EndConnectionMonitoring(connectionID string, success bool) error {
	// Calculate duration (this would need to be tracked separately in a real implementation)
	duration := time.Minute // Placeholder - would need actual tracking

	// Log connection end
	status := "success"
	if !success {
		status = "failed"
	}

	if err := ms.logger.LogConnectionEvent(nil, "connection_end", map[string]interface{}{
		"connection_id": connectionID,
		"status":        status,
		"duration":      duration.String(),
	}); err != nil {
		return fmt.Errorf("failed to log connection end: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordConnectionEnd(connectionID, duration, success); err != nil {
		return fmt.Errorf("failed to record connection end metrics: %w", err)
	}

	return nil
}

// Buffer monitoring implementation

// RecordBufferOperation records buffer operations
func (ms *DefaultMonitoringService) RecordBufferOperation(operation string, chunkSize int, processingTime time.Duration) error {
	// Log buffer operation
	if err := ms.logger.Log(LogLevelInfo, "buffer_manager", fmt.Sprintf("buffer operation: %s", operation), map[string]interface{}{
		"operation":       operation,
		"chunk_size":      chunkSize,
		"processing_time": processingTime.String(),
		"event":           "buffer_operation",
	}); err != nil {
		return fmt.Errorf("failed to log buffer operation: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordChunkProcessed(chunkSize, processingTime); err != nil {
		return fmt.Errorf("failed to record buffer operation metrics: %w", err)
	}

	return nil
}

// RecordBufferOverflow records buffer overflow events
func (ms *DefaultMonitoringService) RecordBufferOverflow(bufferSize int, reason string) error {
	// Log overflow
	if err := ms.logger.Log(LogLevelWarn, "buffer_manager", "buffer overflow detected", map[string]interface{}{
		"buffer_size": bufferSize,
		"reason":      reason,
		"event":       "buffer_overflow",
	}); err != nil {
		return fmt.Errorf("failed to log buffer overflow: %w", err)
	}

	// Record metrics
	if err := ms.metrics.RecordBufferOverflow(bufferSize); err != nil {
		return fmt.Errorf("failed to record buffer overflow metrics: %w", err)
	}

	return nil
}

// RecordBufferUtilization records buffer utilization
func (ms *DefaultMonitoringService) RecordBufferUtilization(utilizationPercent float64) error {
	// Log utilization if high
	if utilizationPercent > 80 {
		if err := ms.logger.Log(LogLevelWarn, "buffer_manager", "high buffer utilization", map[string]interface{}{
			"utilization_percent": utilizationPercent,
			"event":               "high_buffer_utilization",
		}); err != nil {
			return fmt.Errorf("failed to log buffer utilization: %w", err)
		}
	}

	// Record metrics
	if err := ms.metrics.RecordBufferUtilization(utilizationPercent); err != nil {
		return fmt.Errorf("failed to record buffer utilization metrics: %w", err)
	}

	return nil
}

// Error monitoring implementation

// RecordError records error events with context
func (ms *DefaultMonitoringService) RecordError(streamCtx *StreamingContext, errorType string, err error, context map[string]interface{}) error {
	// Log error with context
	if logErr := ms.logger.LogErrorContext(streamCtx, errorType, err, context); logErr != nil {
		return fmt.Errorf("failed to log error context: %w", logErr)
	}

	// Record error metrics
	if metricsErr := ms.metrics.IncrementCounter("errors_total", map[string]string{
		"error_type": errorType,
	}); metricsErr != nil {
		return fmt.Errorf("failed to record error metrics: %w", metricsErr)
	}

	return nil
}

// RecordRecovery records error recovery events
func (ms *DefaultMonitoringService) RecordRecovery(streamCtx *StreamingContext, recoveryType string, context map[string]interface{}) error {
	// Log recovery
	if context == nil {
		context = make(map[string]interface{})
	}
	context["recovery_type"] = recoveryType
	context["event"] = "error_recovery"

	if err := ms.logger.LogWithContext(LogLevelInfo, "error_handler", fmt.Sprintf("error recovery: %s", recoveryType), streamCtx, context); err != nil {
		return fmt.Errorf("failed to log error recovery: %w", err)
	}

	// Record recovery metrics
	if err := ms.metrics.IncrementCounter("recoveries_total", map[string]string{
		"recovery_type": recoveryType,
	}); err != nil {
		return fmt.Errorf("failed to record recovery metrics: %w", err)
	}

	return nil
}

// Health and status implementation

// GetHealthStatus returns the overall health status of the system
func (ms *DefaultMonitoringService) GetHealthStatus() (*HealthStatus, error) {
	streamMetrics := ms.metrics.GetStreamMetrics()
	connectionMetrics := ms.metrics.GetConnectionMetrics()
	bufferMetrics := ms.metrics.GetBufferMetrics()

	status := &HealthStatus{
		Overall:          "healthy",
		StreamHealth:     "healthy",
		ConnectionHealth: "healthy",
		BufferHealth:     "healthy",
		LastCheck:        time.Now(),
		Issues:           []string{},
		Recommendations:  []string{},
		Details: map[string]interface{}{
			"uptime_seconds": time.Since(ms.startTime).Seconds(),
		},
	}

	// Check stream health
	if streamMetrics.ErrorRate > 10 {
		status.StreamHealth = "degraded"
		status.Issues = append(status.Issues, fmt.Sprintf("High stream error rate: %.2f%%", streamMetrics.ErrorRate))
		status.Recommendations = append(status.Recommendations, "Investigate stream processing errors")
	}

	// Check connection health
	if connectionMetrics.ConnectionSuccessRate < 90 {
		status.ConnectionHealth = "degraded"
		status.Issues = append(status.Issues, fmt.Sprintf("Low connection success rate: %.2f%%", connectionMetrics.ConnectionSuccessRate))
		status.Recommendations = append(status.Recommendations, "Check network connectivity and upstream service health")
	}

	// Check buffer health
	if bufferMetrics.BufferUtilization > 90 {
		status.BufferHealth = "degraded"
		status.Issues = append(status.Issues, fmt.Sprintf("High buffer utilization: %.2f%%", bufferMetrics.BufferUtilization))
		status.Recommendations = append(status.Recommendations, "Consider increasing buffer size or optimizing processing")
	}

	// Set overall status
	if len(status.Issues) > 0 {
		status.Overall = "degraded"
	}

	return status, nil
}

// GetMetricsSummary returns a summary of key metrics
func (ms *DefaultMonitoringService) GetMetricsSummary() (*MetricsSummary, error) {
	streamMetrics := ms.metrics.GetStreamMetrics()
	connectionMetrics := ms.metrics.GetConnectionMetrics()
	bufferMetrics := ms.metrics.GetBufferMetrics()

	summary := &MetricsSummary{
		ActiveStreams:     streamMetrics.StreamsStarted - streamMetrics.StreamsCompleted - streamMetrics.StreamsFailed,
		TotalStreams:      streamMetrics.StreamsStarted,
		SuccessRate:       streamMetrics.SuccessRate,
		ErrorRate:         streamMetrics.ErrorRate,
		AverageLatency:    connectionMetrics.AverageLatency,
		ActiveConnections: connectionMetrics.ActiveConnections,
		BufferUtilization: bufferMetrics.BufferUtilization,
		LastUpdated:       time.Now(),
	}

	return summary, nil
}

// ExportMonitoringData exports all monitoring data
func (ms *DefaultMonitoringService) ExportMonitoringData() (map[string]interface{}, error) {
	metricsData, err := ms.metrics.ExportMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to export metrics: %w", err)
	}

	healthStatus, err := ms.GetHealthStatus()
	if err != nil {
		return nil, fmt.Errorf("failed to get health status: %w", err)
	}

	metricsSummary, err := ms.GetMetricsSummary()
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics summary: %w", err)
	}

	export := map[string]interface{}{
		"timestamp":        time.Now(),
		"health_status":    healthStatus,
		"metrics_summary":  metricsSummary,
		"detailed_metrics": metricsData,
		"uptime_seconds":   time.Since(ms.startTime).Seconds(),
	}

	return export, nil
}
