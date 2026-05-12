package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const defaultOpenRouterURL = "https://openrouter.ai/api/v1"

// Role represents the role of a message in a chat conversation.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ChatMessage represents a single message in a chat conversation.
type ChatMessage struct {
	Role       Role       `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

// ToolCall represents a tool call requested by the assistant.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the function name and arguments for a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolDef defines a tool that the LLM can call.
type ToolDef struct {
	Type     string     `json:"type"`
	Function ToolSchema `json:"function"`
}

// ToolSchema describes a tool's name, description, and parameter schema.
type ToolSchema struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters"`
}

// chatRequest is the request body for the OpenRouter chat completions API.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Tools    []ToolDef     `json:"tools,omitempty"`
}

// ChatResponse is the response from the OpenRouter chat completions API.
type ChatResponse struct {
	ID      string   `json:"id"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`
}

// Choice represents a single completion choice.
type Choice struct {
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage holds token usage statistics for a completion.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// apiError represents an error response from the OpenRouter API.
type apiError struct {
	Error struct {
		Code    interface{} `json:"code"`
		Message string      `json:"message"`
	} `json:"error"`
}

// LLMClient is an HTTP client for the OpenRouter chat completions API.
type LLMClient struct {
	apiKey  string
	baseURL string
	model   string
	client  *http.Client
}

// NewLLMClient creates a new LLM client for the OpenRouter API.
// apiKey is the OpenRouter API key. model is the model identifier
// (e.g. "anthropic/claude-sonnet-4").
func NewLLMClient(apiKey, model string) *LLMClient {
	return &LLMClient{
		apiKey:  apiKey,
		baseURL: defaultOpenRouterURL,
		model:   model,
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// ChatCompletion sends a chat completion request to the OpenRouter API
// and returns the response. The request includes the configured model,
// the provided messages, and optionally tool definitions.
func (c *LLMClient) ChatCompletion(ctx context.Context, messages []ChatMessage, tools []ToolDef) (*ChatResponse, error) {
	reqBody := chatRequest{
		Model:    c.model,
		Messages: messages,
		Tools:    tools,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to parse an API error message from the response body.
		var ae apiError
		if err := json.NewDecoder(resp.Body).Decode(&ae); err == nil && ae.Error.Message != "" {
			return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, ae.Error.Message)
		}
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, c.baseURL)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &chatResp, nil
}

// TextMessage creates a ChatMessage with text content.
func TextMessage(role Role, content string) ChatMessage {
	return ChatMessage{
		Role:    role,
		Content: &content,
	}
}

// ToolResultMessage creates a ChatMessage for a tool result.
func ToolResultMessage(toolCallID, name, content string) ChatMessage {
	return ChatMessage{
		Role:       RoleTool,
		Content:    &content,
		ToolCallID: toolCallID,
		Name:       name,
	}
}
