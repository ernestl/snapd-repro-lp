package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ToolResult holds the output from executing a tool.
type ToolResult struct {
	// Output is the text content returned to the LLM.
	Output string
	// StopAgent indicates the agent loop should stop (set by
	// report_result).
	StopAgent bool
}

// Tool is the interface that agent tools must implement.
type Tool interface {
	// Name returns the tool's name as used in the LLM API.
	Name() string
	// Definition returns the ToolDef for the LLM API tool list.
	Definition() ToolDef
	// Execute runs the tool with the given JSON arguments and returns
	// the result.
	Execute(ctx context.Context, argsJSON string) (*ToolResult, error)
}

// --- run_command tool ---

// RunCommandTool executes shell commands inside an LXD container.
type RunCommandTool struct {
	container ContainerManager
}

// NewRunCommandTool creates a new run_command tool backed by the given
// container manager.
func NewRunCommandTool(container ContainerManager) *RunCommandTool {
	return &RunCommandTool{container: container}
}

func (t *RunCommandTool) Name() string { return "run_command" }

func (t *RunCommandTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "run_command",
			Description: "Execute a shell command inside the LXD container. Use this to install packages, run scripts, inspect files, and reproduce bugs.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "The shell command to execute (run via bash -c).",
					},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}

type runCommandArgs struct {
	Command string `json:"command"`
}

func (t *RunCommandTool) Execute(ctx context.Context, argsJSON string) (*ToolResult, error) {
	var args runCommandArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing run_command args: %w", err)
	}
	if args.Command == "" {
		return &ToolResult{Output: "error: command is required"}, nil
	}

	result, err := t.container.Exec(ctx, args.Command)
	if err != nil {
		return nil, fmt.Errorf("executing command: %w", err)
	}

	output := result.Output
	// Truncate very long output to avoid blowing up context.
	const maxOutput = 50000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n...[truncated]"
	}

	if result.ExitCode != 0 {
		output += fmt.Sprintf("\n[exit code: %d]", result.ExitCode)
	}

	return &ToolResult{Output: output}, nil
}

// --- report_result tool ---

// ReportResultTool allows the LLM to report whether the bug was
// reproduced successfully, and stops the agent loop.
type ReportResultTool struct {
	// Result is populated after Execute is called.
	Result *ReproResult
}

// ReproResult holds the structured result from the LLM's reproduction
// attempt.
type ReproResult struct {
	Reproduced  bool   `json:"reproduced"`
	Explanation string `json:"explanation"`
	Script      string `json:"script"`
}

// NewReportResultTool creates a new report_result tool.
func NewReportResultTool() *ReportResultTool {
	return &ReportResultTool{}
}

func (t *ReportResultTool) Name() string { return "report_result" }

func (t *ReportResultTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "report_result",
			Description: "Report the final result of the reproduction attempt. Call this once you have determined whether the bug can be reproduced. This ends the agent session.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"reproduced": map[string]interface{}{
						"type":        "boolean",
						"description": "Whether the bug was successfully reproduced.",
					},
					"explanation": map[string]interface{}{
						"type":        "string",
						"description": "Explanation of what was tried and the outcome.",
					},
					"script": map[string]interface{}{
						"type":        "string",
						"description": "The shell script that reproduces the bug (if reproduced), or the best attempt (if not).",
					},
				},
				"required":             []string{"reproduced", "explanation", "script"},
				"additionalProperties": false,
			},
		},
	}
}

type reportResultArgs struct {
	Reproduced  bool   `json:"reproduced"`
	Explanation string `json:"explanation"`
	Script      string `json:"script"`
}

func (t *ReportResultTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args reportResultArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing report_result args: %w", err)
	}

	t.Result = &ReproResult{
		Reproduced:  args.Reproduced,
		Explanation: args.Explanation,
		Script:      args.Script,
	}

	return &ToolResult{
		Output:    "Result recorded. Agent session ending.",
		StopAgent: true,
	}, nil
}

// --- read_file tool ---

// ReadFileTool reads files from the bug directory. It is restricted to
// the configured base directory to prevent the LLM from reading
// arbitrary files on the host.
type ReadFileTool struct {
	baseDir string
}

// NewReadFileTool creates a new read_file tool restricted to the given
// base directory.
func NewReadFileTool(baseDir string) *ReadFileTool {
	return &ReadFileTool{baseDir: baseDir}
}

func (t *ReadFileTool) Name() string { return "read_file" }

func (t *ReadFileTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "read_file",
			Description: "Read a file from the bug directory (e.g. an attachment). The path is relative to the bug directory.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Relative path to the file within the bug directory (e.g. 'journal.log').",
					},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		},
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFileTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args readFileArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing read_file args: %w", err)
	}
	if args.Path == "" {
		return &ToolResult{Output: "error: path is required"}, nil
	}

	// Resolve the absolute path and verify it stays within baseDir.
	absBase, err := filepath.Abs(t.baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolving base dir: %w", err)
	}
	absPath, err := filepath.Abs(filepath.Join(t.baseDir, args.Path))
	if err != nil {
		return nil, fmt.Errorf("resolving path: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase+string(filepath.Separator)) && absPath != absBase {
		return &ToolResult{Output: "error: path escapes the bug directory"}, nil
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &ToolResult{Output: fmt.Sprintf("error: file not found: %s", args.Path)}, nil
		}
		return nil, fmt.Errorf("reading file: %w", err)
	}

	content := string(data)
	const maxFileSize = 100000
	if len(content) > maxFileSize {
		content = content[:maxFileSize] + "\n...[truncated]"
	}

	return &ToolResult{Output: content}, nil
}

// --- report_plan tool ---

// PlanStep describes a single step in a reproduction plan.
type PlanStep struct {
	Description string `json:"description"`
	Command     string `json:"command"`
}

// ReproPlan holds the structured reproduction plan produced by the
// planning LLM.
type ReproPlan struct {
	BugID               int        `json:"bug_id"`
	Title               string     `json:"title"`
	UbuntuVersion       string     `json:"ubuntu_version"`
	Steps               []PlanStep `json:"steps"`
	ExpectedResult      string     `json:"expected_result"`
	AttachmentsReviewed []string   `json:"attachments_reviewed"`
	ModelUsed           string     `json:"model_used"`
}

// ReportPlanTool allows the planning LLM to output a structured
// reproduction plan, and stops the planning agent loop.
type ReportPlanTool struct {
	// Plan is populated after Execute is called.
	Plan *ReproPlan
}

// NewReportPlanTool creates a new report_plan tool.
func NewReportPlanTool() *ReportPlanTool {
	return &ReportPlanTool{}
}

func (t *ReportPlanTool) Name() string { return "report_plan" }

func (t *ReportPlanTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "report_plan",
			Description: "Output the structured reproduction plan. Call this after you have analyzed the bug report and any attachments. This ends the planning session.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ubuntu_version": map[string]interface{}{
						"type":        "string",
						"description": "The Ubuntu version to use for reproduction (e.g. '24.04', '22.04').",
					},
					"steps": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"description": map[string]interface{}{
									"type":        "string",
									"description": "What this step does and why.",
								},
								"command": map[string]interface{}{
									"type":        "string",
									"description": "The shell command to execute.",
								},
							},
							"required": []string{"description", "command"},
						},
						"description": "Ordered list of steps to reproduce the bug.",
					},
					"expected_result": map[string]interface{}{
						"type":        "string",
						"description": "What the expected outcome looks like when the bug is reproduced.",
					},
					"attachments_reviewed": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": "List of attachment filenames that were read during planning.",
					},
				},
				"required":             []string{"ubuntu_version", "steps", "expected_result"},
				"additionalProperties": false,
			},
		},
	}
}

type reportPlanArgs struct {
	UbuntuVersion       string     `json:"ubuntu_version"`
	Steps               []PlanStep `json:"steps"`
	ExpectedResult      string     `json:"expected_result"`
	AttachmentsReviewed []string   `json:"attachments_reviewed"`
}

func (t *ReportPlanTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args reportPlanArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing report_plan args: %w", err)
	}

	t.Plan = &ReproPlan{
		UbuntuVersion:       args.UbuntuVersion,
		Steps:               args.Steps,
		ExpectedResult:      args.ExpectedResult,
		AttachmentsReviewed: args.AttachmentsReviewed,
	}

	return &ToolResult{
		Output:    "Plan recorded. Planning session ending.",
		StopAgent: true,
	}, nil
}

// --- ToolExecutor ---

// ToolExecutor dispatches tool calls to registered tools.
type ToolExecutor struct {
	tools map[string]Tool
}

// NewToolExecutor creates a ToolExecutor with the given tools.
func NewToolExecutor(tools ...Tool) *ToolExecutor {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &ToolExecutor{tools: m}
}

// ToolDefinitions returns the ToolDef list for the LLM API.
func (e *ToolExecutor) ToolDefinitions() []ToolDef {
	defs := make([]ToolDef, 0, len(e.tools))
	for _, t := range e.tools {
		defs = append(defs, t.Definition())
	}
	return defs
}

// Execute dispatches a tool call by name and returns the result.
func (e *ToolExecutor) Execute(ctx context.Context, name, argsJSON string) (*ToolResult, error) {
	tool, ok := e.tools[name]
	if !ok {
		return &ToolResult{
			Output: fmt.Sprintf("error: unknown tool %q", name),
		}, nil
	}
	return tool.Execute(ctx, argsJSON)
}
