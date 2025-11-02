package models

import (
	"encoding/json"
	"fmt"
)

// AnthropicRequest represents the request structure for Anthropic Claude API
type AnthropicRequest struct {
	Model         string             `json:"model" validate:"required"`
	Messages      []AnthropicMessage `json:"messages" validate:"required,min=1"`
	System        interface{}        `json:"system,omitempty"`
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    interface{}        `json:"tool_choice,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

// AnthropicMessage represents a message in the Anthropic format
type AnthropicMessage struct {
	Role    string      `json:"role" validate:"required,oneof=user assistant system"`
	Content interface{} `json:"content" validate:"required"`
}

// AnthropicContentBlock represents different types of content blocks
type AnthropicContentBlock struct {
	Type string `json:"type" validate:"required"`
	Text string `json:"text,omitempty"`

	// For tool_use content blocks
	ID    string      `json:"id,omitempty"`
	Name  string      `json:"name,omitempty"`
	Input interface{} `json:"input,omitempty"`

	// For tool_result content blocks
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
}

// AnthropicTool represents a tool definition in Anthropic format
type AnthropicTool struct {
	Name        string                 `json:"name" validate:"required"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema" validate:"required"`
}

// AnthropicResponse represents the response structure from Anthropic Claude API
type AnthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []AnthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason,omitempty"`
	StopSequence *string                 `json:"stop_sequence,omitempty"`
	Usage        *AnthropicUsage         `json:"usage,omitempty"`
}

// AnthropicUsage represents token usage information
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicStreamEvent represents streaming events from Anthropic API
type AnthropicStreamEvent struct {
	Type  string      `json:"type"`
	Index *int        `json:"index,omitempty"`
	Delta interface{} `json:"delta,omitempty"`

	// For message_start events
	Message *AnthropicResponse `json:"message,omitempty"`

	// For content_block_start events
	ContentBlock *AnthropicContentBlock `json:"content_block,omitempty"`
}

// ParseContent parses the content field which can be either a string or array of content blocks
func (m *AnthropicMessage) ParseContent() ([]AnthropicContentBlock, error) {
	switch content := m.Content.(type) {
	case string:
		return []AnthropicContentBlock{
			{
				Type: "text",
				Text: content,
			},
		}, nil
	case []interface{}:
		var blocks []AnthropicContentBlock
		for _, item := range content {
			blockData, err := json.Marshal(item)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal content block: %w", err)
			}

			var block AnthropicContentBlock
			if err := json.Unmarshal(blockData, &block); err != nil {
				return nil, fmt.Errorf("failed to unmarshal content block: %w", err)
			}
			blocks = append(blocks, block)
		}
		return blocks, nil
	default:
		return nil, fmt.Errorf("invalid content type: %T", content)
	}
}

// Validate performs basic validation on the AnthropicRequest
func (r *AnthropicRequest) Validate() error {
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

		if msg.Role != "user" && msg.Role != "assistant" && msg.Role != "system" {
			return fmt.Errorf("message %d: invalid role %s", i, msg.Role)
		}

		if msg.Content == nil {
			return fmt.Errorf("message %d: content is required", i)
		}
	}

	return nil
}
