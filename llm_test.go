package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newLLMTestServer(handler http.HandlerFunc) (*httptest.Server, *LLMClient) {
	ts := httptest.NewServer(handler)
	client := NewLLMClient("test-key", "test-model")
	client.baseURL = ts.URL
	return ts, client
}

func TestChatCompletionTextResponse(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer test-key")
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}

		// Verify request body.
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if req.Model != "test-model" {
			t.Errorf("Model = %q, want %q", req.Model, "test-model")
		}
		if len(req.Messages) != 1 {
			t.Fatalf("Messages = %d, want 1", len(req.Messages))
		}

		content := "The capital of France is Paris."
		resp := ChatResponse{
			ID: "chatcmpl-123",
			Choices: []Choice{
				{
					Message:      ChatMessage{Role: RoleAssistant, Content: &content},
					FinishReason: "stop",
				},
			},
			Usage: &Usage{PromptTokens: 10, CompletionTokens: 8, TotalTokens: 18},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer ts.Close()

	messages := []ChatMessage{TextMessage(RoleUser, "What is the capital of France?")}
	resp, err := client.ChatCompletion(context.Background(), messages, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want %q", resp.ID, "chatcmpl-123")
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("Choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.Choices[0].FinishReason, "stop")
	}
	if resp.Choices[0].Message.Content == nil || *resp.Choices[0].Message.Content != "The capital of France is Paris." {
		t.Errorf("Content = %v", resp.Choices[0].Message.Content)
	}
	if resp.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if resp.Usage.TotalTokens != 18 {
		t.Errorf("TotalTokens = %d, want 18", resp.Usage.TotalTokens)
	}
}

func TestChatCompletionToolCall(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		// Verify tools were sent in the request.
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		if len(req.Tools) != 1 {
			t.Fatalf("Tools = %d, want 1", len(req.Tools))
		}
		if req.Tools[0].Function.Name != "run_command" {
			t.Errorf("Tool name = %q, want %q", req.Tools[0].Function.Name, "run_command")
		}

		resp := ChatResponse{
			ID: "chatcmpl-456",
			Choices: []Choice{
				{
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: nil,
						ToolCalls: []ToolCall{
							{
								ID:   "call_abc123",
								Type: "function",
								Function: FunctionCall{
									Name:      "run_command",
									Arguments: `{"command":"snap list"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	defer ts.Close()

	tools := []ToolDef{
		{
			Type: "function",
			Function: ToolSchema{
				Name:        "run_command",
				Description: "Execute a shell command",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"command": map[string]interface{}{
							"type":        "string",
							"description": "The command to execute",
						},
					},
					"required": []string{"command"},
				},
			},
		},
	}

	messages := []ChatMessage{TextMessage(RoleUser, "List installed snaps")}
	resp, err := client.ChatCompletion(context.Background(), messages, tools)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("Choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", resp.Choices[0].FinishReason, "tool_calls")
	}

	toolCalls := resp.Choices[0].Message.ToolCalls
	if len(toolCalls) != 1 {
		t.Fatalf("ToolCalls = %d, want 1", len(toolCalls))
	}
	if toolCalls[0].ID != "call_abc123" {
		t.Errorf("ToolCall ID = %q, want %q", toolCalls[0].ID, "call_abc123")
	}
	if toolCalls[0].Type != "function" {
		t.Errorf("ToolCall Type = %q, want %q", toolCalls[0].Type, "function")
	}
	if toolCalls[0].Function.Name != "run_command" {
		t.Errorf("Function Name = %q, want %q", toolCalls[0].Function.Name, "run_command")
	}
	if toolCalls[0].Function.Arguments != `{"command":"snap list"}` {
		t.Errorf("Function Arguments = %q", toolCalls[0].Function.Arguments)
	}
}

func TestChatCompletionUnauthorized(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(apiError{
			Error: struct {
				Code    interface{} `json:"code"`
				Message string      `json:"message"`
			}{
				Code:    401,
				Message: "Invalid API key",
			},
		})
	})
	defer ts.Close()

	messages := []ChatMessage{TextMessage(RoleUser, "hello")}
	_, err := client.ChatCompletion(context.Background(), messages, nil)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if got := err.Error(); got != "API error 401: Invalid API key" {
		t.Errorf("error = %q, want %q", got, "API error 401: Invalid API key")
	}
}

func TestChatCompletionServerError(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer ts.Close()

	messages := []ChatMessage{TextMessage(RoleUser, "hello")}
	_, err := client.ChatCompletion(context.Background(), messages, nil)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestChatCompletionErrorBody(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(apiError{
			Error: struct {
				Code    interface{} `json:"code"`
				Message string      `json:"message"`
			}{
				Code:    400,
				Message: "Invalid model specified",
			},
		})
	})
	defer ts.Close()

	messages := []ChatMessage{TextMessage(RoleUser, "hello")}
	_, err := client.ChatCompletion(context.Background(), messages, nil)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if got := err.Error(); got != "API error 400: Invalid model specified" {
		t.Errorf("error = %q, want %q", got, "API error 400: Invalid model specified")
	}
}

func TestTextMessageHelper(t *testing.T) {
	msg := TextMessage(RoleUser, "hello world")
	if msg.Role != RoleUser {
		t.Errorf("Role = %q, want %q", msg.Role, RoleUser)
	}
	if msg.Content == nil || *msg.Content != "hello world" {
		t.Errorf("Content = %v, want %q", msg.Content, "hello world")
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %d, want 0", len(msg.ToolCalls))
	}
}

func TestToolResultMessageHelper(t *testing.T) {
	msg := ToolResultMessage("call_123", "run_command", "output from command")
	if msg.Role != RoleTool {
		t.Errorf("Role = %q, want %q", msg.Role, RoleTool)
	}
	if msg.Content == nil || *msg.Content != "output from command" {
		t.Errorf("Content = %v, want %q", msg.Content, "output from command")
	}
	if msg.ToolCallID != "call_123" {
		t.Errorf("ToolCallID = %q, want %q", msg.ToolCallID, "call_123")
	}
	if msg.Name != "run_command" {
		t.Errorf("Name = %q, want %q", msg.Name, "run_command")
	}
}

func TestChatCompletionContextCancelled(t *testing.T) {
	ts, client := newLLMTestServer(func(w http.ResponseWriter, r *http.Request) {
		// This should not be reached if context is cancelled before the request.
		w.WriteHeader(http.StatusOK)
	})
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	messages := []ChatMessage{TextMessage(RoleUser, "hello")}
	_, err := client.ChatCompletion(ctx, messages, nil)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
