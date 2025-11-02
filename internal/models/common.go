package models

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// APIError represents a standardized API error response
type APIError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
}

// Error implements the error interface
func (e *APIError) Error() string {
	return fmt.Sprintf("API Error %d (%s): %s", e.Code, e.Type, e.Message)
}

// NewAPIError creates a new APIError
func NewAPIError(code int, message, errorType string) *APIError {
	return &APIError{
		Code:    code,
		Message: message,
		Type:    errorType,
	}
}

// ValidationError represents validation errors
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface
func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error for field '%s': %s", e.Field, e.Message)
}

// ErrorResponse represents a standardized error response structure
type ErrorResponse struct {
	Error *APIError `json:"error"`
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Version   string `json:"version,omitempty"`
}

// StreamEvent represents a Server-Sent Event
type StreamEvent struct {
	Event string      `json:"event,omitempty"`
	Data  interface{} `json:"data"`
	ID    string      `json:"id,omitempty"`
}

// FormatSSE formats data as a Server-Sent Event string
func (se *StreamEvent) FormatSSE() string {
	var builder strings.Builder

	if se.Event != "" {
		builder.WriteString(fmt.Sprintf("event: %s\n", se.Event))
	}

	if se.ID != "" {
		builder.WriteString(fmt.Sprintf("id: %s\n", se.ID))
	}

	// Marshal data to JSON
	dataBytes, err := json.Marshal(se.Data)
	if err != nil {
		dataBytes = []byte(fmt.Sprintf(`{"error": "failed to marshal data: %s"}`, err.Error()))
	}

	builder.WriteString(fmt.Sprintf("data: %s\n\n", string(dataBytes)))

	return builder.String()
}

// JSONValidator provides utility functions for JSON validation
type JSONValidator struct{}

// ValidateJSON checks if a string is valid JSON
func (jv *JSONValidator) ValidateJSON(data string) error {
	var js json.RawMessage
	return json.Unmarshal([]byte(data), &js)
}

// ValidateAndUnmarshal validates JSON and unmarshals it into the target
func (jv *JSONValidator) ValidateAndUnmarshal(data []byte, target interface{}) error {
	if err := json.Unmarshal(data, target); err != nil {
		return &ValidationError{
			Field:   "json",
			Message: fmt.Sprintf("invalid JSON: %s", err.Error()),
		}
	}
	return nil
}

// ModelMapper provides model name mapping utilities
type ModelMapper struct {
	anthropicToOpenRouter map[string]string
}

// NewModelMapper creates a new ModelMapper with default mappings
func NewModelMapper() *ModelMapper {
	return &ModelMapper{
		anthropicToOpenRouter: map[string]string{
			"claude-3-5-sonnet-20241022": "anthropic/claude-3.5-sonnet",
			"claude-3-5-sonnet-20240620": "anthropic/claude-3.5-sonnet",
			"claude-3-opus-20240229":     "anthropic/claude-3-opus",
			"claude-3-sonnet-20240229":   "anthropic/claude-3-sonnet",
			"claude-3-haiku-20240307":    "anthropic/claude-3-haiku",
			"claude-2.1":                 "anthropic/claude-2.1",
			"claude-2.0":                 "anthropic/claude-2.0",
			"claude-instant-1.2":         "anthropic/claude-instant-1.2",
		},
	}
}

// MapAnthropicToOpenRouter maps Anthropic model names to OpenRouter model names
func (mm *ModelMapper) MapAnthropicToOpenRouter(anthropicModel string) string {
	if openRouterModel, exists := mm.anthropicToOpenRouter[anthropicModel]; exists {
		return openRouterModel
	}
	// If no mapping exists, return the original model name
	return anthropicModel
}

// AddMapping adds a new model mapping
func (mm *ModelMapper) AddMapping(anthropicModel, openRouterModel string) {
	mm.anthropicToOpenRouter[anthropicModel] = openRouterModel
}

// HTTPStatusMapper provides utilities for HTTP status code handling
type HTTPStatusMapper struct{}

// MapUpstreamError maps upstream API errors to appropriate HTTP status codes
func (hsm *HTTPStatusMapper) MapUpstreamError(upstreamStatus int) int {
	switch upstreamStatus {
	case http.StatusBadRequest:
		return http.StatusBadRequest
	case http.StatusUnauthorized:
		return http.StatusUnauthorized
	case http.StatusForbidden:
		return http.StatusForbidden
	case http.StatusNotFound:
		return http.StatusNotFound
	case http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case http.StatusInternalServerError:
		return http.StatusInternalServerError
	case http.StatusBadGateway:
		return http.StatusBadGateway
	case http.StatusServiceUnavailable:
		return http.StatusServiceUnavailable
	case http.StatusGatewayTimeout:
		return http.StatusGatewayTimeout
	default:
		// For unknown status codes, return 502 Bad Gateway
		return http.StatusBadGateway
	}
}

// ContentTypeValidator provides content type validation utilities
type ContentTypeValidator struct{}

// IsJSONContentType checks if the content type is JSON
func (ctv *ContentTypeValidator) IsJSONContentType(contentType string) bool {
	return strings.Contains(strings.ToLower(contentType), "application/json")
}

// IsStreamingContentType checks if the content type indicates streaming
func (ctv *ContentTypeValidator) IsStreamingContentType(contentType string) bool {
	lowerContentType := strings.ToLower(contentType)
	return strings.Contains(lowerContentType, "text/event-stream") ||
		strings.Contains(lowerContentType, "text/plain")
}

// RequestValidator provides request validation utilities
type RequestValidator struct {
	jsonValidator *JSONValidator
}

// NewRequestValidator creates a new RequestValidator
func NewRequestValidator() *RequestValidator {
	return &RequestValidator{
		jsonValidator: &JSONValidator{},
	}
}

// ValidateContentType validates the request content type
func (rv *RequestValidator) ValidateContentType(contentType string) error {
	ctValidator := &ContentTypeValidator{}
	if !ctValidator.IsJSONContentType(contentType) {
		return &ValidationError{
			Field:   "content-type",
			Message: "content type must be application/json",
		}
	}
	return nil
}

// ValidateAuthHeader validates the x-api-key header
func (rv *RequestValidator) ValidateAuthHeader(apiKey string) error {
	if apiKey == "" {
		return &APIError{
			Code:    http.StatusUnauthorized,
			Message: "x-api-key header is required",
			Type:    "authentication_error",
		}
	}

	if strings.TrimSpace(apiKey) == "" {
		return &APIError{
			Code:    http.StatusUnauthorized,
			Message: "x-api-key cannot be empty",
			Type:    "authentication_error",
		}
	}

	return nil
}

// StringUtils provides string manipulation utilities
type StringUtils struct{}

// TruncateString truncates a string to the specified length
func (su *StringUtils) TruncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "..."
}

// SanitizeForLogging removes sensitive information from strings for logging
func (su *StringUtils) SanitizeForLogging(s string) string {
	// Remove potential API keys or tokens
	if strings.Contains(strings.ToLower(s), "bearer ") {
		return "[REDACTED_TOKEN]"
	}

	if strings.Contains(strings.ToLower(s), "api") && strings.Contains(strings.ToLower(s), "key") {
		return "[REDACTED_API_KEY]"
	}

	return s
}
