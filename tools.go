package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ToolResult holds the output from executing a tool.
type ToolResult struct {
	// Output is the text content returned to the LLM.
	Output string
	// StopAgent indicates the agent loop should stop (set by
	// report_result).
	StopAgent bool
	// StopMessage is an optional human-readable message displayed
	// when StopAgent is true (e.g. "LLM reported result").
	StopMessage string
	// Summary is a concise description of what the tool did,
	// displayed in the progress line (e.g. "apt-get update").
	Summary string
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

// RunCommandTool executes shell commands inside an LXD instance.
type RunCommandTool struct {
	ref *InstanceRef
}

// NewRunCommandTool creates a new run_command tool backed by the given
// instance manager. The instance is wrapped in an InstanceRef so it
// can be swapped at runtime (e.g. by RelaunchVMTool).
func NewRunCommandTool(instance InstanceManager) *RunCommandTool {
	return &RunCommandTool{ref: &InstanceRef{Instance: instance}}
}

// NewRunCommandToolFromRef creates a new run_command tool backed by a
// shared InstanceRef. Use this when the instance may be swapped at
// runtime (e.g. by RelaunchVMTool).
func NewRunCommandToolFromRef(ref *InstanceRef) *RunCommandTool {
	return &RunCommandTool{ref: ref}
}

func (t *RunCommandTool) Name() string { return "run_command" }

func (t *RunCommandTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "run_command",
			Description: "Execute a shell command inside the LXD instance. Use this to install packages, run scripts, inspect files, and reproduce bugs.",
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

	result, err := t.ref.Instance.Exec(ctx, args.Command)
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

	return &ToolResult{Output: output, Summary: summariseCmd(args.Command)}, nil
}

// summariseCmd extracts a concise summary from a shell command by
// stripping leading environment variable assignments, truncating at the
// first pipe, and removing trailing redirections.
func summariseCmd(cmd string) string {
	// Take only the first line.
	if i := strings.IndexByte(cmd, '\n'); i >= 0 {
		cmd = cmd[:i]
	}
	cmd = strings.TrimSpace(cmd)

	// Truncate at the first pipe.
	if i := strings.IndexByte(cmd, '|'); i >= 0 {
		cmd = strings.TrimSpace(cmd[:i])
	}

	// Strip trailing redirections (e.g. "2>&1", "> /dev/null").
	for {
		trimmed := strings.TrimRight(cmd, " ")
		changed := false
		for _, suffix := range []string{"2>&1", ">&2", ">/dev/null", "2>/dev/null"} {
			if strings.HasSuffix(trimmed, suffix) {
				trimmed = strings.TrimSpace(trimmed[:len(trimmed)-len(suffix)])
				changed = true
			}
		}
		if !changed {
			cmd = trimmed
			break
		}
		cmd = trimmed
	}

	// Strip leading VAR=value assignments.
	for {
		// Match KEY=value at the start (no spaces in value, or quoted).
		rest := strings.TrimSpace(cmd)
		eqIdx := strings.IndexByte(rest, '=')
		if eqIdx < 0 {
			break
		}
		// The part before '=' must look like a variable name (no spaces).
		key := rest[:eqIdx]
		if strings.ContainsAny(key, " \t") || key == "" {
			break
		}
		// Skip the value: either quoted or until next space.
		after := rest[eqIdx+1:]
		var skip int
		if len(after) > 0 && (after[0] == '"' || after[0] == '\'') {
			// Find matching close quote.
			q := after[0]
			end := strings.IndexByte(after[1:], q)
			if end < 0 {
				break // unmatched quote, stop stripping
			}
			skip = end + 2
		} else {
			// Skip until space.
			spIdx := strings.IndexAny(after, " \t")
			if spIdx < 0 {
				break // no command after value
			}
			skip = spIdx
		}
		cmd = strings.TrimSpace(after[skip:])
	}

	const maxLen = 60
	if len(cmd) > maxLen {
		return cmd[:maxLen] + "..."
	}
	return cmd
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
		Output:      "Result recorded. Agent session ending.",
		StopAgent:   true,
		StopMessage: "LLM reported result",
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
		// If the path is a directory, list its contents.
		if info, statErr := os.Stat(absPath); statErr == nil && info.IsDir() {
			entries, readErr := os.ReadDir(absPath)
			if readErr != nil {
				return nil, fmt.Errorf("reading directory: %w", readErr)
			}
			var listing strings.Builder
			fmt.Fprintf(&listing, "Contents of %s:\n", args.Path)
			for _, e := range entries {
				suffix := ""
				if e.IsDir() {
					suffix = "/"
				}
				fmt.Fprintf(&listing, "  %s%s\n", e.Name(), suffix)
			}
			fmt.Fprintf(&listing, "\n%d entries", len(entries))
			return &ToolResult{Output: listing.String(), Summary: args.Path}, nil
		}
		return nil, fmt.Errorf("reading file: %w", err)
	}

	content := string(data)
	const maxFileSize = 100000
	if len(content) > maxFileSize {
		content = content[:maxFileSize] + "\n...[truncated]"
	}

	return &ToolResult{Output: content, Summary: args.Path}, nil
}

// --- InstanceRef: shared mutable instance reference ---

// InstanceRef holds a mutable reference to the current InstanceManager.
// It is shared between RunCommandTool and RelaunchVMTool so that when
// the VM is relaunched, run_command automatically targets the new instance.
type InstanceRef struct {
	Instance InstanceManager
}

// --- relaunch_vm tool ---

// VMFactory creates a new instance manager. The caller is responsible for
// launching and eventually deleting the returned instance.
type VMFactory func() InstanceManager

// RelaunchVMTool allows the LLM to relaunch the VM with a different
// Ubuntu version mid-conversation. It deletes the current instance,
// launches a new one, and updates the shared InstanceRef.
type RelaunchVMTool struct {
	ref       *InstanceRef
	factory   VMFactory
	output    io.Writer // for progress messages
	onCleanup func(old InstanceManager) // optional callback after old instance is deleted
}

// NewRelaunchVMTool creates a new relaunch_vm tool.
func NewRelaunchVMTool(ref *InstanceRef, factory VMFactory, output io.Writer) *RelaunchVMTool {
	return &RelaunchVMTool{
		ref:     ref,
		factory: factory,
		output:  output,
	}
}

func (t *RelaunchVMTool) Name() string { return "relaunch_vm" }

func (t *RelaunchVMTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "relaunch_vm",
			Description: "Relaunch the LXD VM with a different Ubuntu version. Use this when the bug requires a specific Ubuntu version that differs from the current one (24.04). The current VM is deleted and a new one is launched. All previous state inside the VM is lost.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"ubuntu_version": map[string]interface{}{
						"type":        "string",
						"description": "The Ubuntu version to launch (e.g. '22.04', '20.04', '24.04').",
					},
				},
				"required":             []string{"ubuntu_version"},
				"additionalProperties": false,
			},
		},
	}
}

type relaunchVMArgs struct {
	UbuntuVersion string `json:"ubuntu_version"`
}

func (t *RelaunchVMTool) Execute(ctx context.Context, argsJSON string) (*ToolResult, error) {
	var args relaunchVMArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing relaunch_vm args: %w", err)
	}
	if args.UbuntuVersion == "" {
		return &ToolResult{Output: "error: ubuntu_version is required"}, nil
	}

	// Delete the current instance.
	old := t.ref.Instance
	oldName := old.Name()
	_, _ = fmt.Fprintf(t.output, "  Deleting VM %s...\n", oldName)
	if err := old.Delete(); err != nil {
		return &ToolResult{Output: fmt.Sprintf("error: failed to delete current VM: %v", err)}, nil
	}

	if t.onCleanup != nil {
		t.onCleanup(old)
	}

	// Launch a new instance.  Update the shared reference immediately so
	// that a failed launch does not leave ref pointing at the deleted VM
	// (which would cause an infinite delete-retry loop on the same name).
	newManager := t.factory()
	t.ref.Instance = newManager
	_, _ = fmt.Fprintf(t.output, "  Launching VM %s (ubuntu:%s)...\n", newManager.Name(), args.UbuntuVersion)
	status, err := newManager.LaunchCached(args.UbuntuVersion, "vm")
	if err != nil {
		return &ToolResult{Output: fmt.Sprintf("error: failed to launch new VM: %v", err)}, nil
	}

	switch status {
	case CacheHit:
		_, _ = fmt.Fprintf(t.output, "  (from cached snapshot)\n")
	case CacheMiss:
		_, _ = fmt.Fprintf(t.output, "  (created cache for ubuntu:%s)\n", args.UbuntuVersion)
	case CacheFallback:
		_, _ = fmt.Fprintf(t.output, "  (fresh launch, cache unavailable)\n")
	}

	summary := fmt.Sprintf("ubuntu:%s → %s", args.UbuntuVersion, newManager.Name())
	output := fmt.Sprintf("VM relaunched successfully.\nNew instance: %s\nUbuntu version: %s\nAll previous VM state has been reset.", newManager.Name(), args.UbuntuVersion)
	return &ToolResult{Output: output, Summary: summary}, nil
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

// --- query_snapd_revisions tool ---

// SnapdRevision represents a single entry from the snapd snap store
// revision-version mapping.
type SnapdRevision struct {
	Revision int
	Version  string
	Arch     string
	Status   string
	Created  time.Time
}

// parseRevisionMap parses the tabular revision-version mapping text
// into a slice of SnapdRevision entries. The input format has a
// 2-line header (column names + separator), data rows, and an
// optional trailing "Total:" line. Each data row is whitespace-
// separated: REVISION VERSION ARCH STATUS CREATED(YYYY-MM-DD).
func parseRevisionMap(data string) ([]SnapdRevision, error) {
	var revisions []SnapdRevision

	scanner := bufio.NewScanner(strings.NewReader(data))
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip the 2-line header.
		if lineNum <= 2 {
			continue
		}

		// Skip empty lines and the trailing "Total:" summary.
		if line == "" || strings.HasPrefix(line, "Total:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue // malformed line, skip
		}

		rev, err := strconv.Atoi(fields[0])
		if err != nil {
			continue // not a data row
		}

		created, err := time.Parse("2006-01-02", fields[len(fields)-1])
		if err != nil {
			continue // unparseable date
		}

		// Status may be a single word at fields[3] (e.g. "Published")
		// and date at fields[4]. But some versions have spaces in
		// status like "AutomaticallyRejected". The version is always
		// fields[1], arch is fields[2], date is the last field, and
		// status is everything between arch and date.
		version := fields[1]
		arch := fields[2]
		status := strings.Join(fields[3:len(fields)-1], "")

		revisions = append(revisions, SnapdRevision{
			Revision: rev,
			Version:  version,
			Arch:     arch,
			Status:   status,
			Created:  created,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning revision map: %w", err)
	}

	if len(revisions) == 0 {
		return nil, fmt.Errorf("no revisions parsed from input")
	}

	return revisions, nil
}

// QueryRevisionsTool lets the LLM query the snapd snap revision-to-
// version mapping by date range, architecture, revision number, or
// version string.
type QueryRevisionsTool struct {
	revisions []SnapdRevision
}

// NewQueryRevisionsTool creates a new query_snapd_revisions tool
// backed by the given parsed revision data.
func NewQueryRevisionsTool(revisions []SnapdRevision) *QueryRevisionsTool {
	return &QueryRevisionsTool{revisions: revisions}
}

func (t *QueryRevisionsTool) Name() string { return "query_snapd_revisions" }

func (t *QueryRevisionsTool) Definition() ToolDef {
	return ToolDef{
		Type: "function",
		Function: ToolSchema{
			Name:        "query_snapd_revisions",
			Description: "Query the snapd snap store revision-to-version mapping. Use this to look up which snapd version corresponds to a revision number mentioned in a bug report, or to find revisions for a specific version or time range. At least one filter parameter must be provided.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"revision": map[string]interface{}{
						"type":        "integer",
						"description": "Look up a specific snap store revision number.",
					},
					"version": map[string]interface{}{
						"type":        "string",
						"description": "Filter by version string (prefix match, e.g. '2.63' matches '2.63', '2.63.1', '2.63+git...').",
					},
					"architecture": map[string]interface{}{
						"type":        "string",
						"description": "Filter by architecture (e.g. 'amd64', 'arm64', 'armhf', 'ppc64el', 's390x', 'riscv64').",
					},
					"since": map[string]interface{}{
						"type":        "string",
						"description": "Start date filter in YYYY-MM-DD format (inclusive).",
					},
					"until": map[string]interface{}{
						"type":        "string",
						"description": "End date filter in YYYY-MM-DD format (inclusive).",
					},
				},
				"additionalProperties": false,
			},
		},
	}
}

type queryRevisionsArgs struct {
	Revision     *int   `json:"revision,omitempty"`
	Version      string `json:"version,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	Since        string `json:"since,omitempty"`
	Until        string `json:"until,omitempty"`
}

const maxRevisionResults = 200

func (t *QueryRevisionsTool) Execute(_ context.Context, argsJSON string) (*ToolResult, error) {
	var args queryRevisionsArgs
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parsing query_snapd_revisions args: %w", err)
	}

	// Require at least one filter.
	if args.Revision == nil && args.Version == "" && args.Architecture == "" && args.Since == "" && args.Until == "" {
		return &ToolResult{Output: "error: at least one filter parameter is required (revision, version, architecture, since, or until)"}, nil
	}

	// Parse date filters.
	var sinceTime, untilTime time.Time
	var err error
	if args.Since != "" {
		sinceTime, err = time.Parse("2006-01-02", args.Since)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("error: invalid 'since' date format (use YYYY-MM-DD): %v", err)}, nil
		}
	}
	if args.Until != "" {
		untilTime, err = time.Parse("2006-01-02", args.Until)
		if err != nil {
			return &ToolResult{Output: fmt.Sprintf("error: invalid 'until' date format (use YYYY-MM-DD): %v", err)}, nil
		}
	}

	// Filter revisions.
	var matches []SnapdRevision
	for i := range t.revisions {
		r := &t.revisions[i]

		if args.Revision != nil && r.Revision != *args.Revision {
			continue
		}
		if args.Version != "" && !strings.HasPrefix(r.Version, args.Version) {
			continue
		}
		if args.Architecture != "" && r.Arch != args.Architecture {
			continue
		}
		if !sinceTime.IsZero() && r.Created.Before(sinceTime) {
			continue
		}
		if !untilTime.IsZero() && r.Created.After(untilTime) {
			continue
		}

		matches = append(matches, *r)
		if len(matches) > maxRevisionResults {
			break
		}
	}

	if len(matches) == 0 {
		return &ToolResult{Output: "No revisions found matching the given filters.", Summary: "0 results"}, nil
	}

	// Format output.
	var b strings.Builder
	truncated := false
	if len(matches) > maxRevisionResults {
		matches = matches[:maxRevisionResults]
		truncated = true
	}

	fmt.Fprintf(&b, "REVISION  VERSION                     ARCH     STATUS                 CREATED\n")
	fmt.Fprintf(&b, "--------  --------------------------  -------  ---------------------  ----------\n")
	for _, r := range matches {
		fmt.Fprintf(&b, "%-9d %-27s %-8s %-22s %s\n",
			r.Revision, r.Version, r.Arch, r.Status, r.Created.Format("2006-01-02"))
	}

	if truncated {
		fmt.Fprintf(&b, "\n...[showing first %d results, narrow your filters for more specific results]", maxRevisionResults)
	} else {
		fmt.Fprintf(&b, "\n%d results", len(matches))
	}

	summary := fmt.Sprintf("%d results", len(matches))
	if truncated {
		summary = fmt.Sprintf("%d+ results (truncated)", maxRevisionResults)
	}

	return &ToolResult{Output: b.String(), Summary: summary}, nil
}
