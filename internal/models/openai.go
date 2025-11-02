package models

import (
	"encoding/json"
	"fmt"
)

// OpenAIRequest represents the request structure for OpenAI API
type OpenAIRequest struct {
	Model            string          `json:"model" validate:"required"`
	Messages         []OpenAIMessage `json:"messages" validate:"required,min=1"`
	MaxTokens        *int            `json:"max_tokens,omitempty"`
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"top_p,omitempty"`
	N                *int            `json:"n,omitempty"`
	Stream           bool            `json:"stream,omitempty"`
	Stop             interface{}     `json:"stop,omitempty"`
	PresencePenalty  *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64        `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int  `json:"logit_bias,omitempty"`
	User             string          `json:"user,omitempty"`
	Tools            []OpenAITool    `json:"tools,omitempty"`
	ToolChoice       interface{}     `json:"tool_choice,omitempty"`
}

// OpenAIMessage represents a message in the OpenAI format
type OpenAIMessage struct {
	Role       string           `json:"role" validate:"required,oneof=system user assistant tool"`
	Content    interface{}      `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCalls  []OpenAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

// OpenAIToolCall represents a tool call in OpenAI format
type OpenAIToolCall struct {
	ID       string             `json:"id" validate:"required"`
	Type     string             `json:"type" validate:"required"`
	Function OpenAIFunctionCall `json:"function" validate:"required"`
}

// OpenAIFunctionCall represents a function call within a tool call
type OpenAIFunctionCall struct {
	Name      string `json:"name" validate:"required"`
	Arguments string `json:"arguments" validate:"required"`
}

// OpenAITool represents a tool definition in OpenAI format
type OpenAITool struct {
	Type     string            `json:"type" validate:"required"`
	Function OpenAIFunctionDef `json:"function" validate:"required"`
}

// OpenAIFunctionDef represents a function definition
type OpenAIFunctionDef struct {
	Name        string                 `json:"name" validate:"required"`
	Description string                 `json:"description,omitempty"`
	Parameters  map[string]interface{} `json:"parameters,omitempty"`
}

// OpenAIResponse represents the response structure from OpenAI API
type OpenAIResponse struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []OpenAIChoice `json:"choices"`
	Usage             *OpenAIUsage   `json:"usage,omitempty"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

// OpenAIChoice represents a choice in the OpenAI response
type OpenAIChoice struct {
	Index        int            `json:"index"`
	Message      *OpenAIMessage `json:"message,omitempty"`
	Delta        *OpenAIMessage `json:"delta,omitempty"`
	FinishReason *string        `json:"finish_reason"`
	Logprobs     interface{}    `json:"logprobs,omitempty"`
}

// OpenAIUsage represents token usage information
type OpenAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// OpenAIStreamResponse represents a streaming response chunk from OpenAI API
type OpenAIStreamResponse struct {
	ID                string         `json:"id"`
	Object            string         `json:"object"`
	Created           int64          `json:"created"`
	Model             string         `json:"model"`
	Choices           []OpenAIChoice `json:"choices"`
	SystemFingerprint string         `json:"system_fingerprint,omitempty"`
}

// OpenAIError represents an error response from OpenAI API
type OpenAIError struct {
	Error struct {
		Message string      `json:"message"`
		Type    string      `json:"type"`
		Param   interface{} `json:"param"`
		Code    interface{} `json:"code"`
	} `json:"error"`
}

// ParseContent parses the content field which can be string, array, or null
func (m *OpenAIMessage) ParseContent() (string, error) {
	if m.Content == nil {
		return "", nil
	}

	switch content := m.Content.(type) {
	case string:
		return content, nil
	case []interface{}:
		// Handle array of content parts (multimodal)
		for _, part := range content {
			if partMap, ok := part.(map[string]interface{}); ok {
				if partType, exists := partMap["type"]; exists && partType == "text" {
					if text, textExists := partMap["text"]; textExists {
						if textStr, ok := text.(string); ok {
							return textStr, nil
						}
					}
				}
			}
		}
		return "", nil
	default:
		return "", fmt.Errorf("invalid content type: %T", content)
	}
}

// GetFunctionArguments parses the function arguments JSON string
func (fc *OpenAIFunctionCall) GetFunctionArguments() (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(fc.Arguments), &args); err != nil {
		return nil, fmt.Errorf("failed to parse function arguments: %w", err)
	}
	return args, nil
}

// Validate performs basic validation on the OpenAIRequest
func (r *OpenAIRequest) Validate() error {
	if r.Model == "" {
		return fmt.Errorf("model is required")
	}

	if len(r.Messages) == 0 {
		return fmt.Errorf("messages are required")
	}

	for i, msg := range r.Messages {
		if msg.Role == "" {
			return fmt.Errorf("message %d: role is required", i)
		}

		validRoles := map[string]bool{
			"system":    true,
			"user":      true,
			"assistant": true,
			"tool":      true,
		}

		if !validRoles[msg.Role] {
			return fmt.Errorf("message %d: invalid role %s", i, msg.Role)
		}

		// Tool messages must have tool_call_id
		if msg.Role == "tool" && msg.ToolCallID == "" {
			return fmt.Errorf("message %d: tool messages must have tool_call_id", i)
		}

		// Validate tool calls
		for j, toolCall := range msg.ToolCalls {
			if toolCall.ID == "" {
				return fmt.Errorf("message %d, tool_call %d: id is required", i, j)
			}
			if toolCall.Type == "" {
				return fmt.Errorf("message %d, tool_call %d: type is required", i, j)
			}
			if toolCall.Function.Name == "" {
				return fmt.Errorf("message %d, tool_call %d: function name is required", i, j)
			}
		}
	}

	return nil
}
