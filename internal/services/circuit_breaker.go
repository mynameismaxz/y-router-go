package services

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"anthropic-openai-gateway/internal/config"
	"anthropic-openai-gateway/internal/models"
)

// CircuitBreakerState represents the current state of the circuit breaker
type CircuitBreakerState string

const (
	// StateClosed - circuit is closed, requests are allowed through
	StateClosed CircuitBreakerState = "closed"
	// StateOpen - circuit is open, requests are rejected immediately
	StateOpen CircuitBreakerState = "open"
	// StateHalfOpen - circuit is testing if the service has recovered
	StateHalfOpen CircuitBreakerState = "half_open"
)

// CircuitBreakerConfig holds configuration for the circuit breaker
type CircuitBreakerConfig struct {
	// MaxFailures is the number of failures required to open the circuit
	MaxFailures int `json:"max_failures"`

	// ResetTimeout is the time to wait before attempting to close the circuit
	ResetTimeout time.Duration `json:"reset_timeout"`

	// SuccessThreshold is the number of consecutive successes required to close the circuit from half-open
	SuccessThreshold int `json:"success_threshold"`

	// FailureThreshold is the number of failures in half-open state to reopen the circuit
	FailureThreshold int `json:"failure_threshold"`

	// MonitoringWindow is the time window for tracking failures
	MonitoringWindow time.Duration `json:"monitoring_window"`

	// MinRequestsThreshold is the minimum number of requests before considering failure rate
	MinRequestsThreshold int `json:"min_requests_threshold"`
}

// DefaultCircuitBreakerConfig returns default circuit breaker configuration
func DefaultCircuitBreakerConfig() *CircuitBreakerConfig {
	return &CircuitBreakerConfig{
		MaxFailures:          5,
		ResetTimeout:         30 * time.Second,
		SuccessThreshold:     3,
		FailureThreshold:     2,
		MonitoringWindow:     60 * time.Second,
		MinRequestsThreshold: 10,
	}
}

// CircuitBreakerMetrics holds metrics for the circuit breaker
type CircuitBreakerMetrics struct {
	TotalRequests        int64     `json:"total_requests"`
	SuccessfulRequests   int64     `json:"successful_requests"`
	FailedRequests       int64     `json:"failed_requests"`
	ConsecutiveFailures  int       `json:"consecutive_failures"`
	ConsecutiveSuccesses int       `json:"consecutive_successes"`
	LastFailureTime      time.Time `json:"last_failure_time"`
	LastSuccessTime      time.Time `json:"last_success_time"`
	StateChanges         int64     `json:"state_changes"`
	LastStateChange      time.Time `json:"last_state_change"`
}

// CircuitBreakerEvent represents an event in the circuit breaker lifecycle
type CircuitBreakerEvent struct {
	Type      string                 `json:"type"`
	State     CircuitBreakerState    `json:"state"`
	Timestamp time.Time              `json:"timestamp"`
	Reason    string                 `json:"reason"`
	Metrics   *CircuitBreakerMetrics `json:"metrics"`
}

// CircuitBreaker implements the circuit breaker pattern for protecting upstream services
type CircuitBreaker struct {
	config  *CircuitBreakerConfig
	state   CircuitBreakerState
	metrics *CircuitBreakerMetrics
	mutex   sync.RWMutex

	// Event handlers
	onStateChange func(event *CircuitBreakerEvent)
	onRequest     func(success bool, duration time.Duration)

	// Internal tracking
	failures    []time.Time // Track failure timestamps for sliding window
	lastAttempt time.Time
	name        string
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration
func NewCircuitBreaker(name string, config *CircuitBreakerConfig) *CircuitBreaker {
	if config == nil {
		config = DefaultCircuitBreakerConfig()
	}

	return &CircuitBreaker{
		config:   config,
		state:    StateClosed,
		metrics:  &CircuitBreakerMetrics{},
		failures: make([]time.Time, 0),
		name:     name,
	}
}

// NewCircuitBreakerFromStreamingConfig creates a circuit breaker from streaming configuration
func NewCircuitBreakerFromStreamingConfig(name string, streamingConfig *config.StreamingConfig) *CircuitBreaker {
	circuitConfig := &CircuitBreakerConfig{
		MaxFailures:          streamingConfig.CircuitBreakerThreshold,
		ResetTimeout:         streamingConfig.CircuitBreakerTimeout,
		SuccessThreshold:     streamingConfig.RecoveryThreshold,
		FailureThreshold:     streamingConfig.FailureThreshold,
		MonitoringWindow:     60 * time.Second, // Default monitoring window
		MinRequestsThreshold: 10,               // Default minimum requests
	}

	return NewCircuitBreaker(name, circuitConfig)
}

// Execute executes a function with circuit breaker protection
func (cb *CircuitBreaker) Execute(ctx context.Context, operation func() error) error {
	// Check if request is allowed
	if !cb.allowRequest() {
		return &CircuitBreakerError{
			State:   cb.GetState(),
			Message: "circuit breaker is open",
			Name:    cb.name,
		}
	}

	// Record request start
	startTime := time.Now()

	// Execute the operation
	err := operation()

	// Record the result
	duration := time.Since(startTime)
	cb.recordResult(err == nil, duration)

	return err
}

// ExecuteHTTP executes an HTTP operation with circuit breaker protection
func (cb *CircuitBreaker) ExecuteHTTP(ctx context.Context, operation func() (*http.Response, error)) (*http.Response, error) {
	// Check if request is allowed
	if !cb.allowRequest() {
		return nil, &CircuitBreakerError{
			State:   cb.GetState(),
			Message: "circuit breaker is open",
			Name:    cb.name,
		}
	}

	// Record request start
	startTime := time.Now()

	// Execute the operation
	resp, err := operation()

	// Determine if the result is a success or failure
	success := cb.isHTTPSuccess(resp, err)

	// Record the result
	duration := time.Since(startTime)
	cb.recordResult(success, duration)

	return resp, err
}

// allowRequest determines if a request should be allowed through the circuit breaker
func (cb *CircuitBreaker) allowRequest() bool {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	cb.lastAttempt = now

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		// Check if enough time has passed to attempt recovery
		if now.Sub(cb.metrics.LastFailureTime) >= cb.config.ResetTimeout {
			cb.setState(StateHalfOpen, "reset timeout reached")
			return true
		}
		return false

	case StateHalfOpen:
		return true

	default:
		return false
	}
}

// recordResult records the result of an operation and updates circuit breaker state
func (cb *CircuitBreaker) recordResult(success bool, duration time.Duration) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	now := time.Now()
	cb.metrics.TotalRequests++

	if success {
		cb.recordSuccess(now, duration)
	} else {
		cb.recordFailure(now, duration)
	}

	// Update state based on current metrics
	cb.updateState(now)
}

// recordSuccess records a successful operation
func (cb *CircuitBreaker) recordSuccess(timestamp time.Time, duration time.Duration) {
	cb.metrics.SuccessfulRequests++
	cb.metrics.ConsecutiveSuccesses++
	cb.metrics.ConsecutiveFailures = 0
	cb.metrics.LastSuccessTime = timestamp

	// Notify success handler if set
	if cb.onRequest != nil {
		cb.onRequest(true, duration)
	}
}

// recordFailure records a failed operation
func (cb *CircuitBreaker) recordFailure(timestamp time.Time, duration time.Duration) {
	cb.metrics.FailedRequests++
	cb.metrics.ConsecutiveFailures++
	cb.metrics.ConsecutiveSuccesses = 0
	cb.metrics.LastFailureTime = timestamp

	// Add failure to sliding window
	cb.failures = append(cb.failures, timestamp)

	// Clean up old failures outside the monitoring window
	cb.cleanupOldFailures(timestamp)

	// Notify failure handler if set
	if cb.onRequest != nil {
		cb.onRequest(false, duration)
	}
}

// cleanupOldFailures removes failures outside the monitoring window
func (cb *CircuitBreaker) cleanupOldFailures(now time.Time) {
	cutoff := now.Add(-cb.config.MonitoringWindow)

	// Find the first failure within the window
	start := 0
	for i, failureTime := range cb.failures {
		if failureTime.After(cutoff) {
			start = i
			break
		}
	}

	// Keep only recent failures
	if start > 0 {
		cb.failures = cb.failures[start:]
	}
}

// updateState updates the circuit breaker state based on current metrics
func (cb *CircuitBreaker) updateState(now time.Time) {
	switch cb.state {
	case StateClosed:
		// Check if we should open the circuit
		if cb.shouldOpen(now) {
			cb.setState(StateOpen, fmt.Sprintf("failure threshold exceeded: %d failures", cb.metrics.ConsecutiveFailures))
		}

	case StateHalfOpen:
		// Check if we should close the circuit (enough successes)
		if cb.metrics.ConsecutiveSuccesses >= cb.config.SuccessThreshold {
			cb.setState(StateClosed, fmt.Sprintf("success threshold reached: %d successes", cb.metrics.ConsecutiveSuccesses))
		} else if cb.metrics.ConsecutiveFailures >= cb.config.FailureThreshold {
			// Reopen the circuit if we get failures in half-open state
			cb.setState(StateOpen, fmt.Sprintf("failure in half-open state: %d failures", cb.metrics.ConsecutiveFailures))
		}

	case StateOpen:
		// State transitions from open are handled in allowRequest()
		break
	}
}

// shouldOpen determines if the circuit should be opened based on failure metrics
func (cb *CircuitBreaker) shouldOpen(now time.Time) bool {
	// Check consecutive failures
	if cb.metrics.ConsecutiveFailures >= cb.config.MaxFailures {
		return true
	}

	// Check failure rate within monitoring window
	if cb.metrics.TotalRequests >= int64(cb.config.MinRequestsThreshold) {
		recentFailures := len(cb.failures)
		if recentFailures >= cb.config.MaxFailures {
			return true
		}
	}

	return false
}

// setState changes the circuit breaker state and triggers events
func (cb *CircuitBreaker) setState(newState CircuitBreakerState, reason string) {
	if cb.state == newState {
		return
	}

	oldState := cb.state
	cb.state = newState
	cb.metrics.StateChanges++
	cb.metrics.LastStateChange = time.Now()

	// Reset consecutive counters on state change
	if newState == StateClosed {
		cb.metrics.ConsecutiveFailures = 0
		cb.failures = cb.failures[:0] // Clear failure history
	} else if newState == StateHalfOpen {
		cb.metrics.ConsecutiveSuccesses = 0
		cb.metrics.ConsecutiveFailures = 0
	}

	// Trigger state change event
	if cb.onStateChange != nil {
		event := &CircuitBreakerEvent{
			Type:      "state_change",
			State:     newState,
			Timestamp: time.Now(),
			Reason:    fmt.Sprintf("%s -> %s: %s", oldState, newState, reason),
			Metrics:   cb.copyMetrics(),
		}
		cb.onStateChange(event)
	}
}

// isHTTPSuccess determines if an HTTP response represents a success
func (cb *CircuitBreaker) isHTTPSuccess(resp *http.Response, err error) bool {
	if err != nil {
		return false
	}

	if resp == nil {
		return false
	}

	// Consider 2xx and 3xx as success, 4xx as client errors (success from circuit breaker perspective),
	// and 5xx as server errors (failures)
	return resp.StatusCode < 500
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitBreakerState {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state
}

// GetMetrics returns a copy of the current metrics
func (cb *CircuitBreaker) GetMetrics() *CircuitBreakerMetrics {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.copyMetrics()
}

// copyMetrics creates a copy of the current metrics
func (cb *CircuitBreaker) copyMetrics() *CircuitBreakerMetrics {
	return &CircuitBreakerMetrics{
		TotalRequests:        cb.metrics.TotalRequests,
		SuccessfulRequests:   cb.metrics.SuccessfulRequests,
		FailedRequests:       cb.metrics.FailedRequests,
		ConsecutiveFailures:  cb.metrics.ConsecutiveFailures,
		ConsecutiveSuccesses: cb.metrics.ConsecutiveSuccesses,
		LastFailureTime:      cb.metrics.LastFailureTime,
		LastSuccessTime:      cb.metrics.LastSuccessTime,
		StateChanges:         cb.metrics.StateChanges,
		LastStateChange:      cb.metrics.LastStateChange,
	}
}

// GetName returns the name of the circuit breaker
func (cb *CircuitBreaker) GetName() string {
	return cb.name
}

// GetConfig returns the circuit breaker configuration
func (cb *CircuitBreaker) GetConfig() *CircuitBreakerConfig {
	return cb.config
}

// UpdateConfig updates the circuit breaker configuration
func (cb *CircuitBreaker) UpdateConfig(config *CircuitBreakerConfig) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.config = config
}

// Reset resets the circuit breaker to its initial state
func (cb *CircuitBreaker) Reset() {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	cb.state = StateClosed
	cb.metrics = &CircuitBreakerMetrics{}
	cb.failures = cb.failures[:0]

	if cb.onStateChange != nil {
		event := &CircuitBreakerEvent{
			Type:      "reset",
			State:     StateClosed,
			Timestamp: time.Now(),
			Reason:    "manual reset",
			Metrics:   cb.copyMetrics(),
		}
		cb.onStateChange(event)
	}
}

// ForceOpen forces the circuit breaker to open state
func (cb *CircuitBreaker) ForceOpen(reason string) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.setState(StateOpen, fmt.Sprintf("forced open: %s", reason))
}

// ForceClose forces the circuit breaker to closed state
func (cb *CircuitBreaker) ForceClose(reason string) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.setState(StateClosed, fmt.Sprintf("forced close: %s", reason))
}

// SetOnStateChange sets a callback for state change events
func (cb *CircuitBreaker) SetOnStateChange(handler func(event *CircuitBreakerEvent)) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.onStateChange = handler
}

// SetOnRequest sets a callback for request events
func (cb *CircuitBreaker) SetOnRequest(handler func(success bool, duration time.Duration)) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.onRequest = handler
}

// IsOpen returns true if the circuit breaker is in open state
func (cb *CircuitBreaker) IsOpen() bool {
	return cb.GetState() == StateOpen
}

// IsClosed returns true if the circuit breaker is in closed state
func (cb *CircuitBreaker) IsClosed() bool {
	return cb.GetState() == StateClosed
}

// IsHalfOpen returns true if the circuit breaker is in half-open state
func (cb *CircuitBreaker) IsHalfOpen() bool {
	return cb.GetState() == StateHalfOpen
}

// GetFailureRate returns the current failure rate within the monitoring window
func (cb *CircuitBreaker) GetFailureRate() float64 {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	if cb.metrics.TotalRequests == 0 {
		return 0.0
	}

	return float64(cb.metrics.FailedRequests) / float64(cb.metrics.TotalRequests)
}

// GetRecentFailureRate returns the failure rate within the monitoring window
func (cb *CircuitBreaker) GetRecentFailureRate() float64 {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()

	now := time.Now()
	cb.cleanupOldFailures(now)

	recentFailures := len(cb.failures)
	if recentFailures == 0 {
		return 0.0
	}

	// Estimate recent requests based on failure rate and total requests
	// This is an approximation since we don't track all recent requests
	if cb.metrics.TotalRequests < int64(cb.config.MinRequestsThreshold) {
		return 0.0
	}

	// Use a simple heuristic: assume recent requests are proportional to the monitoring window
	windowRatio := float64(cb.config.MonitoringWindow) / float64(time.Since(cb.metrics.LastSuccessTime.Add(-cb.config.MonitoringWindow)))
	if windowRatio > 1.0 {
		windowRatio = 1.0
	}

	estimatedRecentRequests := float64(cb.metrics.TotalRequests) * windowRatio
	if estimatedRecentRequests < float64(recentFailures) {
		estimatedRecentRequests = float64(recentFailures)
	}

	return float64(recentFailures) / estimatedRecentRequests
}

// CircuitBreakerError represents an error when the circuit breaker is open
type CircuitBreakerError struct {
	State   CircuitBreakerState `json:"state"`
	Message string              `json:"message"`
	Name    string              `json:"name"`
}

// Error implements the error interface
func (e *CircuitBreakerError) Error() string {
	return fmt.Sprintf("circuit breaker '%s' is %s: %s", e.Name, e.State, e.Message)
}

// CircuitBreakerManager manages multiple circuit breakers for different services
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker
	mutex    sync.RWMutex
	config   *CircuitBreakerConfig
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager(defaultConfig *CircuitBreakerConfig) *CircuitBreakerManager {
	if defaultConfig == nil {
		defaultConfig = DefaultCircuitBreakerConfig()
	}

	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
		config:   defaultConfig,
	}
}

// GetCircuitBreaker gets or creates a circuit breaker for the given name
func (cbm *CircuitBreakerManager) GetCircuitBreaker(name string) *CircuitBreaker {
	cbm.mutex.RLock()
	if breaker, exists := cbm.breakers[name]; exists {
		cbm.mutex.RUnlock()
		return breaker
	}
	cbm.mutex.RUnlock()

	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()

	// Double-check after acquiring write lock
	if breaker, exists := cbm.breakers[name]; exists {
		return breaker
	}

	// Create new circuit breaker
	breaker := NewCircuitBreaker(name, cbm.config)
	cbm.breakers[name] = breaker
	return breaker
}

// GetAllCircuitBreakers returns all circuit breakers
func (cbm *CircuitBreakerManager) GetAllCircuitBreakers() map[string]*CircuitBreaker {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	result := make(map[string]*CircuitBreaker)
	for name, breaker := range cbm.breakers {
		result[name] = breaker
	}
	return result
}

// RemoveCircuitBreaker removes a circuit breaker
func (cbm *CircuitBreakerManager) RemoveCircuitBreaker(name string) {
	cbm.mutex.Lock()
	defer cbm.mutex.Unlock()
	delete(cbm.breakers, name)
}

// ResetAll resets all circuit breakers
func (cbm *CircuitBreakerManager) ResetAll() {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	for _, breaker := range cbm.breakers {
		breaker.Reset()
	}
}

// GetStatus returns the status of all circuit breakers
func (cbm *CircuitBreakerManager) GetStatus() map[string]CircuitBreakerState {
	cbm.mutex.RLock()
	defer cbm.mutex.RUnlock()

	status := make(map[string]CircuitBreakerState)
	for name, breaker := range cbm.breakers {
		status[name] = breaker.GetState()
	}
	return status
}

// HTTPClientWithCircuitBreaker wraps an HTTP client with circuit breaker protection
type HTTPClientWithCircuitBreaker struct {
	client         *HTTPClient
	circuitBreaker *CircuitBreaker
}

// NewHTTPClientWithCircuitBreaker creates a new HTTP client with circuit breaker protection
func NewHTTPClientWithCircuitBreaker(client *HTTPClient, circuitBreaker *CircuitBreaker) *HTTPClientWithCircuitBreaker {
	return &HTTPClientWithCircuitBreaker{
		client:         client,
		circuitBreaker: circuitBreaker,
	}
}

// SendRequest sends a request with circuit breaker protection
func (hc *HTTPClientWithCircuitBreaker) SendRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	return hc.circuitBreaker.ExecuteHTTP(ctx, func() (*http.Response, error) {
		return hc.client.SendRequest(ctx, req, apiKey)
	})
}

// SendStreamingRequest sends a streaming request with circuit breaker protection
func (hc *HTTPClientWithCircuitBreaker) SendStreamingRequest(ctx context.Context, req *models.OpenAIRequest, apiKey string) (*http.Response, error) {
	return hc.circuitBreaker.ExecuteHTTP(ctx, func() (*http.Response, error) {
		return hc.client.SendStreamingRequest(ctx, req, apiKey)
	})
}

// GetCircuitBreaker returns the circuit breaker instance
func (hc *HTTPClientWithCircuitBreaker) GetCircuitBreaker() *CircuitBreaker {
	return hc.circuitBreaker
}

// GetClient returns the underlying HTTP client
func (hc *HTTPClientWithCircuitBreaker) GetClient() *HTTPClient {
	return hc.client
}
