package services

import (
	"encoding/json"
	"fmt"
	"strings"

	"anthropic-openai-gateway/internal/models"
)

// ConversionService defines the interface for format conversion between Anthropic and OpenAI APIs
type ConversionService interface {
	AnthropicToOpenAI(req *models.AnthropicRequest) (*models.OpenAIRequest, error)
	OpenAIToAnthropic(resp *models.OpenAIResponse, model string) (*models.AnthropicResponse, error)
	ValidateToolCalls(messages []models.OpenAIMessage) []models.OpenAIMessage
	MapModel(anthropicModel string) string
}

// FormatConverter implements the ConversionService interface
type FormatConverter struct {
	modelMapper *models.ModelMapper
}

// NewFormatConverter creates a new FormatConverter instance
func NewFormatConverter() *FormatConverter {
	return &FormatConverter{
		modelMapper: models.NewModelMapper(),
	}
}

// AnthropicToOpenAI converts an Anthropic request to OpenAI format
func (fc *FormatConverter) AnthropicToOpenAI(req *models.AnthropicRequest) (*models.OpenAIRequest, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("invalid anthropic request: %w", err)
	}

	openAIReq := &models.OpenAIRequest{
		Model:       fc.MapModel(req.Model),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
	}

	// Convert stop sequences
	if len(req.StopSequences) > 0 {
		if len(req.StopSequences) == 1 {
			openAIReq.Stop = req.StopSequences[0]
		} else {
			openAIReq.Stop = req.StopSequences
		}
	}

	// Convert tools
	if len(req.Tools) > 0 {
		openAITools := make([]models.OpenAITool, len(req.Tools))
		for i, tool := range req.Tools {
			openAITools[i] = models.OpenAITool{
				Type: "function",
				Function: models.OpenAIFunctionDef{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			}
		}
		openAIReq.Tools = openAITools
	}

	// Convert tool choice
	if req.ToolChoice != nil {
		openAIReq.ToolChoice = fc.convertToolChoice(req.ToolChoice)
	}

	// Convert messages
	messages, err := fc.convertAnthropicMessages(req.Messages, req.System)
	if err != nil {
		return nil, fmt.Errorf("failed to convert messages: %w", err)
	}

	openAIReq.Messages = messages

	return openAIReq, nil
}

// convertAnthropicMessages converts Anthropic messages to OpenAI format
func (fc *FormatConverter) convertAnthropicMessages(anthropicMessages []models.AnthropicMessage, system interface{}) ([]models.OpenAIMessage, error) {
	var messages []models.OpenAIMessage

	// Add system message if present
	if system != nil {
		systemContent, err := fc.extractSystemContent(system)
		if err != nil {
			return nil, fmt.Errorf("failed to process system message: %w", err)
		}
		if systemContent != "" {
			messages = append(messages, models.OpenAIMessage{
				Role:    "system",
				Content: systemContent,
			})
		}
	}

	// Convert each Anthropic message
	for _, msg := range anthropicMessages {
		openAIMessages, err := fc.convertSingleAnthropicMessage(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to convert message: %w", err)
		}
		messages = append(messages, openAIMessages...)
	}

	return messages, nil
}

// convertSingleAnthropicMessage converts a single Anthropic message to one or more OpenAI messages
func (fc *FormatConverter) convertSingleAnthropicMessage(msg models.AnthropicMessage) ([]models.OpenAIMessage, error) {
	contentBlocks, err := msg.ParseContent()
	if err != nil {
		return nil, fmt.Errorf("failed to parse message content: %w", err)
	}

	var messages []models.OpenAIMessage
	var currentMessage models.OpenAIMessage
	currentMessage.Role = msg.Role

	var textParts []string
	var toolCalls []models.OpenAIToolCall

	for _, block := range contentBlocks {
		switch block.Type {
		case "text":
			textParts = append(textParts, block.Text)

		case "tool_use":
			// Convert tool use to OpenAI tool call
			argsBytes, err := json.Marshal(block.Input)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal tool arguments: %w", err)
			}

			toolCall := models.OpenAIToolCall{
				ID:   block.ID,
				Type: "function",
				Function: models.OpenAIFunctionCall{
					Name:      block.Name,
					Arguments: string(argsBytes),
				},
			}
			toolCalls = append(toolCalls, toolCall)

		case "tool_result":
			// Tool results become separate tool messages in OpenAI format
			toolMessage := models.OpenAIMessage{
				Role:       "tool",
				ToolCallID: block.ToolUseID,
			}

			// Handle tool result content
			if block.IsError {
				toolMessage.Content = fmt.Sprintf("Error: %v", block.Content)
			} else {
				switch content := block.Content.(type) {
				case string:
					toolMessage.Content = content
				default:
					contentBytes, err := json.Marshal(content)
					if err != nil {
						return nil, fmt.Errorf("failed to marshal tool result content: %w", err)
					}
					toolMessage.Content = string(contentBytes)
				}
			}

			messages = append(messages, toolMessage)
		}
	}

	// Set content and tool calls for the main message
	if len(textParts) > 0 {
		currentMessage.Content = strings.Join(textParts, "\n")
	}

	if len(toolCalls) > 0 {
		currentMessage.ToolCalls = toolCalls
	}

	// Only add the main message if it has content or tool calls
	if currentMessage.Content != nil || len(currentMessage.ToolCalls) > 0 {
		messages = append([]models.OpenAIMessage{currentMessage}, messages...)
	}

	return messages, nil
}

// extractSystemContent extracts system content from various formats
func (fc *FormatConverter) extractSystemContent(system interface{}) (string, error) {
	switch s := system.(type) {
	case string:
		return s, nil
	case []interface{}:
		var textParts []string
		for _, item := range s {
			if itemMap, ok := item.(map[string]interface{}); ok {
				if itemType, exists := itemMap["type"]; exists && itemType == "text" {
					if text, textExists := itemMap["text"]; textExists {
						if textStr, ok := text.(string); ok {
							textParts = append(textParts, textStr)
						}
					}
				}
			}
		}
		return strings.Join(textParts, "\n"), nil
	default:
		return "", fmt.Errorf("unsupported system content type: %T", system)
	}
}

// convertToolChoice converts Anthropic tool choice to OpenAI format
func (fc *FormatConverter) convertToolChoice(toolChoice interface{}) interface{} {
	switch tc := toolChoice.(type) {
	case string:
		switch tc {
		case "auto":
			return "auto"
		case "required":
			return "required"
		default:
			return "auto"
		}
	case map[string]interface{}:
		if tcType, exists := tc["type"]; exists {
			switch tcType {
			case "tool":
				if name, nameExists := tc["name"]; nameExists {
					return map[string]interface{}{
						"type": "function",
						"function": map[string]interface{}{
							"name": name,
						},
					}
				}
			}
		}
		return "auto"
	default:
		return "auto"
	}
}

// MapModel maps Anthropic model names to OpenRouter model identifiers
func (fc *FormatConverter) MapModel(anthropicModel string) string {
	return fc.modelMapper.MapAnthropicToOpenRouter(anthropicModel)
}

// OpenAIToAnthropic converts an OpenAI response to Anthropic format
func (fc *FormatConverter) OpenAIToAnthropic(resp *models.OpenAIResponse, model string) (*models.AnthropicResponse, error) {
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in OpenAI response")
	}

	choice := resp.Choices[0]
	if choice.Message == nil {
		return nil, fmt.Errorf("no message in OpenAI choice")
	}

	anthropicResp := &models.AnthropicResponse{
		ID:    resp.ID,
		Type:  "message",
		Role:  "assistant",
		Model: model, // Use original Anthropic model name
	}

	// Convert finish reason to stop reason
	if choice.FinishReason != nil {
		anthropicResp.StopReason = fc.convertFinishReason(*choice.FinishReason)
	}

	// Convert usage information
	if resp.Usage != nil {
		anthropicResp.Usage = &models.AnthropicUsage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		}
	}

	// Convert message content
	content, err := fc.convertOpenAIMessageContent(choice.Message)
	if err != nil {
		return nil, fmt.Errorf("failed to convert message content: %w", err)
	}

	anthropicResp.Content = content

	return anthropicResp, nil
}

// convertOpenAIMessageContent converts OpenAI message content to Anthropic content blocks
func (fc *FormatConverter) convertOpenAIMessageContent(msg *models.OpenAIMessage) ([]models.AnthropicContentBlock, error) {
	var contentBlocks []models.AnthropicContentBlock

	// Add text content if present
	if msg.Content != nil {
		textContent, err := msg.ParseContent()
		if err != nil {
			return nil, fmt.Errorf("failed to parse OpenAI content: %w", err)
		}

		if textContent != "" {
			contentBlocks = append(contentBlocks, models.AnthropicContentBlock{
				Type: "text",
				Text: textContent,
			})
		}
	}

	// Convert tool calls to tool_use blocks
	for _, toolCall := range msg.ToolCalls {
		if toolCall.Type != "function" {
			continue
		}

		// Parse function arguments
		var input interface{}
		if toolCall.Function.Arguments != "" {
			if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
				return nil, fmt.Errorf("failed to parse tool call arguments: %w", err)
			}
		}

		contentBlocks = append(contentBlocks, models.AnthropicContentBlock{
			Type:  "tool_use",
			ID:    toolCall.ID,
			Name:  toolCall.Function.Name,
			Input: input,
		})
	}

	return contentBlocks, nil
}

// convertFinishReason converts OpenAI finish reason to Anthropic stop reason
func (fc *FormatConverter) convertFinishReason(finishReason string) string {
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

// ValidateToolCalls validates and pairs tool calls with their results
func (fc *FormatConverter) ValidateToolCalls(messages []models.OpenAIMessage) []models.OpenAIMessage {
	var validatedMessages []models.OpenAIMessage
	toolCallMap := make(map[string]bool)

	for _, msg := range messages {
		switch msg.Role {
		case "assistant":
			// Track tool calls made by assistant
			for _, toolCall := range msg.ToolCalls {
				toolCallMap[toolCall.ID] = false // false means not yet responded to
			}
			validatedMessages = append(validatedMessages, msg)

		case "tool":
			// Validate that this tool response corresponds to a previous tool call
			if msg.ToolCallID != "" {
				if _, exists := toolCallMap[msg.ToolCallID]; exists {
					toolCallMap[msg.ToolCallID] = true // mark as responded to
					validatedMessages = append(validatedMessages, msg)
				}
				// Skip tool messages that don't have corresponding tool calls
			}

		default:
			validatedMessages = append(validatedMessages, msg)
		}
	}

	return validatedMessages
}
