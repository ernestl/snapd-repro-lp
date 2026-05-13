package main

import (
	"context"
	"fmt"
	"io"
	"os"
)

// AgentConfig holds configuration for an agent run.
type AgentConfig struct {
	MaxIterations int
	Verbose       bool
	Output        io.Writer // For progress/status messages. Defaults to os.Stderr.
}

// AgentResult holds the outcome of an agent run.
type AgentResult struct {
	// StoppedByTool is the name of the tool that stopped the agent
	// (e.g. "report_result" or "report_plan"). Empty if the agent
	// stopped because the LLM returned a text response.
	StoppedByTool string
	// LastMessage is the LLM's last text message (if it stopped with
	// finish_reason "stop" instead of calling a tool).
	LastMessage string
}

// Agent orchestrates the LLM agent loop: sending messages to the LLM,
// dispatching tool calls, and collecting results.
type Agent struct {
	llm      *LLMClient
	executor *ToolExecutor
	config   AgentConfig
	output   io.Writer

	// TotalUsage tracks cumulative token usage across all iterations.
	TotalUsage Usage
}

// NewAgent creates a new Agent.
func NewAgent(llm *LLMClient, executor *ToolExecutor, config AgentConfig) *Agent {
	out := config.Output
	if out == nil {
		out = os.Stderr
	}
	return &Agent{
		llm:      llm,
		executor: executor,
		config:   config,
		output:   out,
	}
}

// Run executes the agent loop with the given system prompt and initial
// user message. It returns an AgentResult indicating how the agent
// stopped. The caller should inspect the specific tool (e.g.
// ReportResultTool.Result or ReportPlanTool.Plan) for structured output.
func (a *Agent) Run(ctx context.Context, systemPrompt, userMessage string) (*AgentResult, error) {
	messages := []ChatMessage{
		TextMessage(RoleSystem, systemPrompt),
		TextMessage(RoleUser, userMessage),
	}

	tools := a.executor.ToolDefinitions()
	maxIter := a.config.MaxIterations
	if maxIter <= 0 {
		maxIter = 60
	}

	for i := 0; i < maxIter; i++ {
		a.progressf("[%d/%d] Waiting for LLM response...", i+1, maxIter)

		resp, err := a.llm.ChatCompletion(ctx, messages, tools)
		if err != nil {
			return nil, fmt.Errorf("iteration %d: %w", i+1, err)
		}

		// Accumulate token usage.
		if resp.Usage != nil {
			a.TotalUsage.PromptTokens += resp.Usage.PromptTokens
			a.TotalUsage.CompletionTokens += resp.Usage.CompletionTokens
			a.TotalUsage.TotalTokens += resp.Usage.TotalTokens
		}

		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("iteration %d: no choices in response", i+1)
		}

		choice := resp.Choices[0]

		// Append the assistant message to the conversation.
		messages = append(messages, choice.Message)

		// Show LLM text content if present (may accompany tool calls).
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			a.progressf("[%d/%d] LLM: %s", i+1, maxIter, truncate(*choice.Message.Content, 500))
		}

		// If the LLM returned tool calls, execute them.
		if choice.FinishReason == "tool_calls" || len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				a.progressf("[%d/%d] Tool: %s", i+1, maxIter, tc.Function.Name)
				a.progressf("  args: %s", truncate(tc.Function.Arguments, 500))

				result, err := a.executor.Execute(tc.Function.Name, tc.Function.Arguments)
				if err != nil {
					return nil, fmt.Errorf("iteration %d: tool %s: %w", i+1, tc.Function.Name, err)
				}

				// Append the tool result message.
				messages = append(messages, ToolResultMessage(tc.ID, tc.Function.Name, result.Output))

				a.progressf("  result: %s", truncate(result.Output, 500))

				if result.StopAgent {
					a.progressf("[%d/%d] Agent stopped by %s", i+1, maxIter, tc.Function.Name)
					return &AgentResult{StoppedByTool: tc.Function.Name}, nil
				}
			}
			continue
		}

		// If the LLM stopped without tool calls, we're done (it
		// chose to respond with text instead of calling a tool).
		if choice.FinishReason == "stop" {
			content := ""
			if choice.Message.Content != nil {
				content = *choice.Message.Content
			}
			a.progressf("[%d/%d] LLM stopped with text response", i+1, maxIter)
			return &AgentResult{LastMessage: content}, nil
		}
	}

	return nil, fmt.Errorf("agent reached max iterations (%d) without a result", maxIter)
}

// progressf always prints progress messages to the output writer.
func (a *Agent) progressf(format string, args ...interface{}) {
	fmt.Fprintf(a.output, format+"\n", args...)
}

// logf prints debug messages only when verbose mode is enabled.
func (a *Agent) logf(format string, args ...interface{}) {
	if a.config.Verbose {
		fmt.Fprintf(a.output, "[agent] "+format+"\n", args...)
	}
}

// truncate returns s truncated to maxLen with "..." appended if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
