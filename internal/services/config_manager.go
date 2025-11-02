package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"anthropic-openai-gateway/internal/config"
)

// ConfigManager manages streaming configuration and handles hot-reloading
type ConfigManager struct {
	config           *config.StreamingConfig
	mutex            sync.RWMutex
	updateCallbacks  []ConfigUpdateCallback
	reloadInterval   time.Duration
	stopReload       chan struct{}
	isReloading      bool
	lastReloadTime   time.Time
	reloadErrors     []error
	reloadErrorMutex sync.RWMutex
}

// ConfigUpdateCallback is called when configuration is updated
type ConfigUpdateCallback func(oldConfig, newConfig *config.StreamingConfig) error

// NewConfigManager creates a new configuration manager
func NewConfigManager(initialConfig *config.StreamingConfig) *ConfigManager {
	if initialConfig == nil {
		initialConfig = config.DefaultStreamingConfig()
	}

	return &ConfigManager{
		config:          initialConfig,
		updateCallbacks: make([]ConfigUpdateCallback, 0),
		reloadInterval:  5 * time.Minute, // Default reload interval
		stopReload:      make(chan struct{}),
		isReloading:     false,
		reloadErrors:    make([]error, 0),
	}
}

// GetConfig returns the current streaming configuration
func (cm *ConfigManager) GetConfig() *config.StreamingConfig {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	// Return a copy to prevent external modifications
	configCopy := *cm.config
	return &configCopy
}

// UpdateConfig updates the streaming configuration
func (cm *ConfigManager) UpdateConfig(newConfig *config.StreamingConfig) error {
	if newConfig == nil {
		return fmt.Errorf("new configuration cannot be nil")
	}

	// Validate the new configuration
	if err := newConfig.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	cm.mutex.Lock()
	oldConfig := cm.config
	cm.config = newConfig
	cm.mutex.Unlock()

	// Notify all callbacks about the configuration change
	var callbackErrors []error
	for _, callback := range cm.updateCallbacks {
		if err := callback(oldConfig, newConfig); err != nil {
			callbackErrors = append(callbackErrors, err)
		}
	}

	// If there were callback errors, revert the configuration
	if len(callbackErrors) > 0 {
		cm.mutex.Lock()
		cm.config = oldConfig
		cm.mutex.Unlock()

		return fmt.Errorf("configuration update failed due to callback errors: %v", callbackErrors)
	}

	return nil
}

// UpdateConfigField updates a specific configuration field
func (cm *ConfigManager) UpdateConfigField(field string, value interface{}) error {
	cm.mutex.Lock()
	currentConfig := *cm.config // Create a copy
	cm.mutex.Unlock()

	// Create updates map
	updates := map[string]interface{}{
		field: value,
	}

	// Apply the update to the copy
	if err := currentConfig.Update(updates); err != nil {
		return fmt.Errorf("failed to update field %s: %w", field, err)
	}

	// Update the configuration
	return cm.UpdateConfig(&currentConfig)
}

// AddUpdateCallback adds a callback that will be called when configuration is updated
func (cm *ConfigManager) AddUpdateCallback(callback ConfigUpdateCallback) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.updateCallbacks = append(cm.updateCallbacks, callback)
}

// StartAutoReload starts automatic configuration reloading from environment variables
func (cm *ConfigManager) StartAutoReload(ctx context.Context) {
	cm.mutex.Lock()
	if cm.isReloading {
		cm.mutex.Unlock()
		return
	}
	cm.isReloading = true
	cm.mutex.Unlock()

	go cm.reloadLoop(ctx)
}

// StopAutoReload stops automatic configuration reloading
func (cm *ConfigManager) StopAutoReload() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()

	if !cm.isReloading {
		return
	}

	cm.isReloading = false
	close(cm.stopReload)
	cm.stopReload = make(chan struct{}) // Reset for potential future use
}

// ReloadFromEnv manually reloads configuration from environment variables
func (cm *ConfigManager) ReloadFromEnv() error {
	newConfig, err := config.LoadStreamingConfigFromEnv()
	if err != nil {
		cm.recordReloadError(err)
		return fmt.Errorf("failed to load configuration from environment: %w", err)
	}

	if err := cm.UpdateConfig(newConfig); err != nil {
		cm.recordReloadError(err)
		return fmt.Errorf("failed to update configuration: %w", err)
	}

	cm.mutex.Lock()
	cm.lastReloadTime = time.Now()
	cm.mutex.Unlock()

	return nil
}

// reloadLoop runs the automatic reload loop
func (cm *ConfigManager) reloadLoop(ctx context.Context) {
	ticker := time.NewTicker(cm.reloadInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-cm.stopReload:
			return
		case <-ticker.C:
			if err := cm.ReloadFromEnv(); err != nil {
				// Log error but continue reloading
				cm.recordReloadError(err)
			}
		}
	}
}

// recordReloadError records a reload error
func (cm *ConfigManager) recordReloadError(err error) {
	cm.reloadErrorMutex.Lock()
	defer cm.reloadErrorMutex.Unlock()

	cm.reloadErrors = append(cm.reloadErrors, err)

	// Keep only the last 10 errors
	if len(cm.reloadErrors) > 10 {
		cm.reloadErrors = cm.reloadErrors[len(cm.reloadErrors)-10:]
	}
}

// GetReloadErrors returns recent reload errors
func (cm *ConfigManager) GetReloadErrors() []error {
	cm.reloadErrorMutex.RLock()
	defer cm.reloadErrorMutex.RUnlock()

	// Return a copy
	errors := make([]error, len(cm.reloadErrors))
	copy(errors, cm.reloadErrors)
	return errors
}

// GetLastReloadTime returns the time of the last successful reload
func (cm *ConfigManager) GetLastReloadTime() time.Time {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.lastReloadTime
}

// IsAutoReloading returns whether auto-reload is currently active
func (cm *ConfigManager) IsAutoReloading() bool {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.isReloading
}

// SetReloadInterval sets the interval for automatic configuration reloading
func (cm *ConfigManager) SetReloadInterval(interval time.Duration) {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	cm.reloadInterval = interval
}

// GetReloadInterval returns the current reload interval
func (cm *ConfigManager) GetReloadInterval() time.Duration {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return cm.reloadInterval
}

// StreamingComponentManager manages all streaming components with configuration
type StreamingComponentManager struct {
	configManager   *ConfigManager
	httpClient      *HTTPClient
	retryManager    *RetryManager
	circuitBreaker  *CircuitBreaker
	bufferManager   BufferManager
	streamConverter *StreamConverter
	streamManager   StreamManager
	mutex           sync.RWMutex
}

// NewStreamingComponentManager creates a new streaming component manager
func NewStreamingComponentManager(cfg *config.Config) *StreamingComponentManager {
	configManager := NewConfigManager(cfg.GetStreamingConfig())

	// Create HTTP client with streaming configuration
	httpClient := NewHTTPClientWithStreamingConfig(cfg, cfg.GetStreamingConfig())

	// Create other components
	retryManager := NewRetryManagerFromStreamingConfig(cfg.GetStreamingConfig())
	circuitBreaker := NewCircuitBreakerFromStreamingConfig("openrouter", cfg.GetStreamingConfig())
	bufferManager := NewStreamBufferManagerFromConfig(cfg.GetStreamingConfig())

	// Create stream converter (will need conversion service)
	var streamConverter *StreamConverter
	var streamManager StreamManager

	scm := &StreamingComponentManager{
		configManager:   configManager,
		httpClient:      httpClient,
		retryManager:    retryManager,
		circuitBreaker:  circuitBreaker,
		bufferManager:   bufferManager,
		streamConverter: streamConverter,
		streamManager:   streamManager,
	}

	// Add configuration update callback
	configManager.AddUpdateCallback(scm.handleConfigUpdate)

	return scm
}

// handleConfigUpdate handles configuration updates by updating all components
func (scm *StreamingComponentManager) handleConfigUpdate(oldConfig, newConfig *config.StreamingConfig) error {
	scm.mutex.Lock()
	defer scm.mutex.Unlock()

	var errors []error

	// Update HTTP client configuration
	if scm.httpClient != nil {
		scm.httpClient.UpdateStreamingConfig(newConfig)
	}

	// Update retry manager configuration
	if scm.retryManager != nil {
		retryConfig := &RetryConfig{
			MaxRetries:      newConfig.MaxRetries,
			BaseDelay:       newConfig.RetryBackoffBase,
			MaxDelay:        newConfig.RetryBackoffMax,
			BackoffFactor:   2.0,
			JitterEnabled:   true,
			JitterMaxFactor: 0.1,
		}
		scm.retryManager.UpdateConfig(retryConfig)
	}

	// Update circuit breaker configuration
	if scm.circuitBreaker != nil {
		circuitConfig := &CircuitBreakerConfig{
			MaxFailures:          newConfig.CircuitBreakerThreshold,
			ResetTimeout:         newConfig.CircuitBreakerTimeout,
			SuccessThreshold:     newConfig.RecoveryThreshold,
			FailureThreshold:     newConfig.FailureThreshold,
			MonitoringWindow:     60 * time.Second,
			MinRequestsThreshold: 10,
		}
		scm.circuitBreaker.UpdateConfig(circuitConfig)
	}

	// Update buffer manager (create new instance as it doesn't support hot updates)
	scm.bufferManager = NewStreamBufferManagerFromConfig(newConfig)

	// Update stream converter with new buffer manager
	if scm.streamConverter != nil {
		// Create new stream converter with updated buffer manager
		// Note: This would require the conversion service to be available
		// For now, we'll just update the buffer manager reference
		scm.streamConverter.bufferManager = scm.bufferManager
	}

	if len(errors) > 0 {
		return fmt.Errorf("configuration update errors: %v", errors)
	}

	return nil
}

// GetConfigManager returns the configuration manager
func (scm *StreamingComponentManager) GetConfigManager() *ConfigManager {
	return scm.configManager
}

// GetHTTPClient returns the HTTP client
func (scm *StreamingComponentManager) GetHTTPClient() *HTTPClient {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.httpClient
}

// GetRetryManager returns the retry manager
func (scm *StreamingComponentManager) GetRetryManager() *RetryManager {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.retryManager
}

// GetCircuitBreaker returns the circuit breaker
func (scm *StreamingComponentManager) GetCircuitBreaker() *CircuitBreaker {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.circuitBreaker
}

// GetBufferManager returns the buffer manager
func (scm *StreamingComponentManager) GetBufferManager() BufferManager {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.bufferManager
}

// GetStreamConverter returns the stream converter
func (scm *StreamingComponentManager) GetStreamConverter() *StreamConverter {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.streamConverter
}

// GetStreamManager returns the stream manager
func (scm *StreamingComponentManager) GetStreamManager() StreamManager {
	scm.mutex.RLock()
	defer scm.mutex.RUnlock()
	return scm.streamManager
}

// SetStreamConverter sets the stream converter (needed when conversion service is available)
func (scm *StreamingComponentManager) SetStreamConverter(converter *StreamConverter) {
	scm.mutex.Lock()
	defer scm.mutex.Unlock()
	scm.streamConverter = converter

	// Create stream manager now that we have the converter
	if converter != nil && scm.httpClient != nil {
		scm.streamManager = NewStreamManager(scm.httpClient, converter)
	}
}

// UpdateConfiguration updates the streaming configuration
func (scm *StreamingComponentManager) UpdateConfiguration(newConfig *config.StreamingConfig) error {
	return scm.configManager.UpdateConfig(newConfig)
}

// UpdateConfigurationField updates a specific configuration field
func (scm *StreamingComponentManager) UpdateConfigurationField(field string, value interface{}) error {
	return scm.configManager.UpdateConfigField(field, value)
}

// StartAutoReload starts automatic configuration reloading
func (scm *StreamingComponentManager) StartAutoReload(ctx context.Context) {
	scm.configManager.StartAutoReload(ctx)
}

// StopAutoReload stops automatic configuration reloading
func (scm *StreamingComponentManager) StopAutoReload() {
	scm.configManager.StopAutoReload()
}

// ReloadConfiguration manually reloads configuration from environment
func (scm *StreamingComponentManager) ReloadConfiguration() error {
	return scm.configManager.ReloadFromEnv()
}

// GetConfiguration returns the current streaming configuration
func (scm *StreamingComponentManager) GetConfiguration() *config.StreamingConfig {
	return scm.configManager.GetConfig()
}

// Shutdown gracefully shuts down all components
func (scm *StreamingComponentManager) Shutdown() {
	scm.configManager.StopAutoReload()

	// Additional cleanup can be added here for other components
	if scm.streamManager != nil {
		if defaultManager, ok := scm.streamManager.(*DefaultStreamManager); ok {
			defaultManager.Stop()
		}
	}
}
