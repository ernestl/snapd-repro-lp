package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// mockLLMServer creates an httptest.Server that returns pre-configured
// responses for each call (in order). It also returns an LLMClient
// pointed at the server.
func mockLLMServer(t *testing.T, responses []ChatResponse) (*httptest.Server, *LLMClient) {
	t.Helper()
	callIndex := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if callIndex >= len(responses) {
			t.Fatalf("unexpected LLM call #%d (only %d responses configured)", callIndex+1, len(responses))
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(responses[callIndex]); err != nil {
			t.Errorf("encoding response: %v", err)
		}
		callIndex++
	}))
	client := NewLLMClient("test-key", "test-model")
	client.baseURL = ts.URL
	return ts, client
}

func strPtr(s string) *string { return &s }

func TestAgentSingleToolCallThenReport(t *testing.T) {
	responses := []ChatResponse{
		// Iteration 1: LLM calls run_command.
		{
			Choices: []Choice{{
				FinishReason: "tool_calls",
				Message: ChatMessage{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: FunctionCall{
							Name:      "run_command",
							Arguments: `{"command":"snap version"}`,
						},
					}},
				},
			}},
			Usage: &Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		},
		// Iteration 2: LLM calls report_result.
		{
			Choices: []Choice{{
				FinishReason: "tool_calls",
				Message: ChatMessage{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{{
						ID:   "call_2",
						Type: "function",
						Function: FunctionCall{
							Name:      "report_result",
							Arguments: `{"reproduced":true,"explanation":"Bug reproduced with snap version","script":"snap version"}`,
						},
					}},
				},
			}},
			Usage: &Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230},
		},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	mc := &mockContainer{
		name: "test",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{Output: "snap    2.61\nsnapd   2.61\n", ExitCode: 0}, nil
		},
	}

	runCmd := NewRunCommandTool(mc)
	reportTool := NewReportResultTool()
	executor := NewToolExecutor(runCmd, reportTool)

	var logBuf bytes.Buffer
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: 10,
		Verbose:       true,
		Output:        &logBuf,
	})

	agentResult, err := agent.Run(context.Background(), "system prompt", "reproduce the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agentResult.StoppedByTool != "report_result" {
		t.Errorf("StoppedByTool = %q, want %q", agentResult.StoppedByTool, "report_result")
	}

	// Check the report tool has the result.
	if reportTool.Result == nil {
		t.Fatal("reportTool.Result should be set")
	}
	if !reportTool.Result.Reproduced {
		t.Error("expected Reproduced = true")
	}
	if !strings.Contains(reportTool.Result.Explanation, "snap version") {
		t.Errorf("Explanation = %q", reportTool.Result.Explanation)
	}

	// Check token usage accumulation.
	if agent.TotalUsage.TotalTokens != 350 {
		t.Errorf("TotalTokens = %d, want 350", agent.TotalUsage.TotalTokens)
	}

	// Check verbose log output (includes both progress and debug lines).
	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "[1/10] Waiting for LLM response") {
		t.Errorf("log missing iteration 1 progress marker")
	}
	if !strings.Contains(logOutput, "Tool: run_command") {
		t.Errorf("log missing run_command tool progress")
	}
	if !strings.Contains(logOutput, "Agent stopped by report_result") {
		t.Errorf("log missing stop message")
	}
}

func TestAgentLLMStopsWithText(t *testing.T) {
	responses := []ChatResponse{
		{
			Choices: []Choice{{
				FinishReason: "stop",
				Message: ChatMessage{
					Role:    RoleAssistant,
					Content: strPtr("I cannot reproduce this bug."),
				},
			}},
		},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	reportTool := NewReportResultTool()
	executor := NewToolExecutor(reportTool)

	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 5})

	agentResult, err := agent.Run(context.Background(), "system", "repro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentResult.StoppedByTool != "" {
		t.Errorf("StoppedByTool = %q, want empty", agentResult.StoppedByTool)
	}
	if agentResult.LastMessage != "I cannot reproduce this bug." {
		t.Errorf("LastMessage = %q", agentResult.LastMessage)
	}
}

func TestAgentMaxIterations(t *testing.T) {
	response := ChatResponse{
		Choices: []Choice{{
			FinishReason: "tool_calls",
			Message: ChatMessage{
				Role: RoleAssistant,
				ToolCalls: []ToolCall{{
					ID:   "call_loop",
					Type: "function",
					Function: FunctionCall{
						Name:      "run_command",
						Arguments: `{"command":"echo looping"}`,
					},
				}},
			},
		}},
	}

	responses := []ChatResponse{response, response, response}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	mc := &mockContainer{
		name: "test",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{Output: "looping\n", ExitCode: 0}, nil
		},
	}

	runCmd := NewRunCommandTool(mc)
	reportTool := NewReportResultTool()
	executor := NewToolExecutor(runCmd, reportTool)

	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 3})

	_, err := agent.Run(context.Background(), "system", "repro")
	if err == nil {
		t.Fatal("expected error for max iterations")
	}
	if !strings.Contains(err.Error(), "max iterations (3)") {
		t.Errorf("error = %q, want 'max iterations'", err.Error())
	}
}

func TestAgentLLMError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	llmClient := NewLLMClient("key", "model")
	llmClient.baseURL = ts.URL

	reportTool := NewReportResultTool()
	executor := NewToolExecutor(reportTool)
	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 5})

	_, err := agent.Run(context.Background(), "system", "repro")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "iteration 1") {
		t.Errorf("error = %q, want 'iteration 1'", err.Error())
	}
}

func TestAgentNoChoices(t *testing.T) {
	responses := []ChatResponse{
		{Choices: []Choice{}},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	reportTool := NewReportResultTool()
	executor := NewToolExecutor(reportTool)
	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 5})

	_, err := agent.Run(context.Background(), "system", "repro")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error = %q, want 'no choices'", err.Error())
	}
}

func TestAgentMultipleToolCallsInOneResponse(t *testing.T) {
	responses := []ChatResponse{
		{
			Choices: []Choice{{
				FinishReason: "tool_calls",
				Message: ChatMessage{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{
						{
							ID:   "call_a",
							Type: "function",
							Function: FunctionCall{
								Name:      "run_command",
								Arguments: `{"command":"snap list"}`,
							},
						},
						{
							ID:   "call_b",
							Type: "function",
							Function: FunctionCall{
								Name:      "report_result",
								Arguments: `{"reproduced":false,"explanation":"not repro","script":"snap list"}`,
							},
						},
					},
				},
			}},
		},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	mc := &mockContainer{
		name: "test",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{Output: "ok\n", ExitCode: 0}, nil
		},
	}

	runCmd := NewRunCommandTool(mc)
	reportTool := NewReportResultTool()
	executor := NewToolExecutor(runCmd, reportTool)
	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 5})

	agentResult, err := agent.Run(context.Background(), "system", "repro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agentResult.StoppedByTool != "report_result" {
		t.Errorf("StoppedByTool = %q, want %q", agentResult.StoppedByTool, "report_result")
	}
	if reportTool.Result.Reproduced {
		t.Error("expected Reproduced = false")
	}
}

func TestAgentVerboseOff(t *testing.T) {
	responses := []ChatResponse{
		{
			Choices: []Choice{{
				FinishReason: "stop",
				Message: ChatMessage{
					Role:    RoleAssistant,
					Content: strPtr("done"),
				},
			}},
		},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	var logBuf bytes.Buffer
	reportTool := NewReportResultTool()
	executor := NewToolExecutor(reportTool)
	agent := NewAgent(llmClient, executor, AgentConfig{
		MaxIterations: 5,
		Verbose:       false,
		Output:        &logBuf,
	})

	_, err := agent.Run(context.Background(), "system", "repro")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	logOutput := logBuf.String()
	// Progress messages should always appear.
	if !strings.Contains(logOutput, "[1/5] Waiting for LLM response") {
		t.Errorf("expected progress output even when Verbose=false, got %q", logOutput)
	}
	// Verbose debug lines (prefixed [agent]) should NOT appear.
	if strings.Contains(logOutput, "[agent]") {
		t.Errorf("expected no [agent] debug output when Verbose=false, got %q", logOutput)
	}
}

func TestAgentPlanningPhase(t *testing.T) {
	// Test that the agent works with report_plan tool (planning phase).
	responses := []ChatResponse{
		// LLM calls read_file.
		{
			Choices: []Choice{{
				FinishReason: "tool_calls",
				Message: ChatMessage{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{{
						ID:   "call_1",
						Type: "function",
						Function: FunctionCall{
							Name:      "read_file",
							Arguments: `{"path":"test.txt"}`,
						},
					}},
				},
			}},
		},
		// LLM calls report_plan.
		{
			Choices: []Choice{{
				FinishReason: "tool_calls",
				Message: ChatMessage{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{{
						ID:   "call_2",
						Type: "function",
						Function: FunctionCall{
							Name:      "report_plan",
							Arguments: `{"ubuntu_version":"24.04","steps":[{"description":"test step","command":"echo test"}],"expected_result":"test output","attachments_reviewed":["test.txt"]}`,
						},
					}},
				},
			}},
		},
	}

	ts, llmClient := mockLLMServer(t, responses)
	defer ts.Close()

	// Create a temp dir with a test file for read_file.
	dir := t.TempDir()
	writeTestFile(t, dir, "test.txt", "test content")

	readFile := NewReadFileTool(dir)
	reportPlan := NewReportPlanTool()
	executor := NewToolExecutor(readFile, reportPlan)
	agent := NewAgent(llmClient, executor, AgentConfig{MaxIterations: 5})

	agentResult, err := agent.Run(context.Background(), "system", "plan")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if agentResult.StoppedByTool != "report_plan" {
		t.Errorf("StoppedByTool = %q, want %q", agentResult.StoppedByTool, "report_plan")
	}

	if reportPlan.Plan == nil {
		t.Fatal("Plan should be set")
	}
	if reportPlan.Plan.UbuntuVersion != "24.04" {
		t.Errorf("UbuntuVersion = %q, want %q", reportPlan.Plan.UbuntuVersion, "24.04")
	}
	if len(reportPlan.Plan.Steps) != 1 {
		t.Fatalf("Steps = %d, want 1", len(reportPlan.Plan.Steps))
	}
}

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}
