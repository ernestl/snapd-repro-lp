package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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
	// stopped because the LLM returned a text response or hit max
	// iterations.
	StoppedByTool string
	// LastMessage is the LLM's last text message (if it stopped with
	// finish_reason "stop" instead of calling a tool).
	LastMessage string
	// MaxIterationsReached is true when the agent loop ended because
	// it exhausted the configured iteration budget.
	MaxIterationsReached bool
	// RecentActivity summarises the last few tool calls and their
	// outputs so the caller can understand what the agent was working
	// on when it ran out of iterations.
	RecentActivity string
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
		a.logf("iteration %d: sending %d messages", i+1, len(messages))

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

		a.logf("finish_reason: %s", resp.Choices[0].FinishReason)

		choice := resp.Choices[0]

		// Append the assistant message to the conversation.
		messages = append(messages, choice.Message)

		// Show LLM text content if present (may accompany tool calls).
		if choice.Message.Content != nil && *choice.Message.Content != "" {
			a.progressf("[%d/%d] %s", i+1, maxIter, cyan("LLM: "+truncate(*choice.Message.Content, 500)))
		}

		// If the LLM returned tool calls, execute them.
		if choice.FinishReason == "tool_calls" || len(choice.Message.ToolCalls) > 0 {
			for _, tc := range choice.Message.ToolCalls {
				a.progressf("[%d/%d] Tool: %s", i+1, maxIter, tc.Function.Name)
				a.progressf("  %s", dim("args: "+truncate(tc.Function.Arguments, 500)))

				result, err := a.executor.Execute(ctx, tc.Function.Name, tc.Function.Arguments)
				if err != nil {
					a.progressf("[%d/%d] Tool error (%s): %s", i+1, maxIter, tc.Function.Name, err.Error())
					// Feed the error back to the LLM as a tool result message,
					// so it can adapt and try a different approach.
					errorContent := fmt.Sprintf("error executing tool %s: %s", tc.Function.Name, err.Error())
					messages = append(messages, ToolResultMessage(tc.ID, tc.Function.Name, errorContent))
					continue
				}

				// Append the tool result message.
				messages = append(messages, ToolResultMessage(tc.ID, tc.Function.Name, result.Output))

				a.progressf("  %s", dim("result: "+truncate(result.Output, 500)))

				if result.StopAgent {
					a.progressf("[%d/%d] %s", i+1, maxIter, bold("Agent stopped by "+tc.Function.Name))
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

	a.progressf("[%d/%d] %s", maxIter, maxIter, yellow("Agent reached max iterations without a result"))
	return &AgentResult{
		MaxIterationsReached: true,
		RecentActivity:       summariseRecentActivity(messages),
	}, nil
}

// summariseRecentActivity extracts the last few tool calls and their
// outputs from the conversation history so we can report what the agent
// was busy with when it ran out of iterations.
func summariseRecentActivity(messages []ChatMessage) string {
	// Walk backward and collect up to the last 6 tool-related messages
	// (3 assistant tool-call messages + their 3 tool-result messages).
	const maxMessages = 6
	var relevant []ChatMessage
	for i := len(messages) - 1; i >= 0 && len(relevant) < maxMessages; i-- {
		m := messages[i]
		switch m.Role {
		case RoleTool:
			relevant = append(relevant, m)
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				relevant = append(relevant, m)
			} else if m.Content != nil && *m.Content != "" {
				relevant = append(relevant, m)
			}
		}
	}

	// Reverse so they're in chronological order.
	for i, j := 0, len(relevant)-1; i < j; i, j = i+1, j-1 {
		relevant[i], relevant[j] = relevant[j], relevant[i]
	}

	var b strings.Builder
	for _, m := range relevant {
		switch m.Role {
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				for _, tc := range m.ToolCalls {
					b.WriteString(fmt.Sprintf("-> %s(%s)\n",
						tc.Function.Name,
						truncate(tc.Function.Arguments, 200)))
				}
			} else if m.Content != nil {
				b.WriteString(fmt.Sprintf("LLM: %s\n", truncate(*m.Content, 300)))
			}
		case RoleTool:
			name := m.Name
			if name == "" {
				name = "tool"
			}
			content := ""
			if m.Content != nil {
				content = truncate(*m.Content, 300)
			}
			b.WriteString(fmt.Sprintf("<- %s: %s\n", name, content))
		}
	}

	if b.Len() == 0 {
		return "(no tool activity recorded)"
	}
	return b.String()
}

// progressf always prints progress messages to the output writer.
func (a *Agent) progressf(format string, args ...interface{}) {
	_, _ = fmt.Fprintf(a.output, format+"\n", args...)
}

// logf prints debug messages only when verbose mode is enabled.
func (a *Agent) logf(format string, args ...interface{}) {
	if a.config.Verbose {
		_, _ = fmt.Fprintf(a.output, "[agent] "+format+"\n", args...)
	}
}

// truncate returns s truncated to maxLen with "..." appended if needed.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
