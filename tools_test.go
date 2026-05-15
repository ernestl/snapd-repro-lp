package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockInstance implements InstanceManager for testing tools.
type mockInstance struct {
	name     string
	execFunc func(command string) (*ExecResult, error)
	launched bool
	deleted  bool
}

func (m *mockInstance) Launch(image string, instanceType string) error {
	m.launched = true
	return nil
}

func (m *mockInstance) Exec(_ context.Context, command string) (*ExecResult, error) {
	if m.execFunc != nil {
		return m.execFunc(command)
	}
	return &ExecResult{Output: "ok\n", ExitCode: 0}, nil
}

func (m *mockInstance) Delete() error {
	m.deleted = true
	return nil
}

func (m *mockInstance) Name() string {
	return m.name
}

func TestRunCommandSuccess(t *testing.T) {
	mc := &mockInstance{
		name: "test-container",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{
				Output:   "core22 20240101\nsnapd 2.61\n",
				ExitCode: 0,
			}, nil
		},
	}

	tool := NewRunCommandTool(mc)
	result, err := tool.Execute(context.Background(),`{"command": "snap list"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopAgent {
		t.Error("StopAgent should be false")
	}
	if !strings.Contains(result.Output, "core22") {
		t.Errorf("Output = %q, want to contain 'core22'", result.Output)
	}
	if result.Summary != "snap list" {
		t.Errorf("Summary = %q, want %q", result.Summary, "snap list")
	}
	// No exit code line for success.
	if strings.Contains(result.Output, "[exit code:") {
		t.Errorf("Output should not contain exit code for success")
	}
}

func TestRunCommandNonZeroExit(t *testing.T) {
	mc := &mockInstance{
		name: "test-container",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{
				Output:   "command not found\n",
				ExitCode: 127,
			}, nil
		},
	}

	tool := NewRunCommandTool(mc)
	result, err := tool.Execute(context.Background(),`{"command": "nonexistent"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "[exit code: 127]") {
		t.Errorf("Output = %q, want to contain '[exit code: 127]'", result.Output)
	}
}

func TestRunCommandExecError(t *testing.T) {
	mc := &mockInstance{
		name: "test-container",
		execFunc: func(command string) (*ExecResult, error) {
			return nil, fmt.Errorf("container not reachable")
		},
	}

	tool := NewRunCommandTool(mc)
	_, err := tool.Execute(context.Background(),`{"command": "ls"}`)
	if err == nil {
		t.Fatal("expected error when container exec fails")
	}
	if !strings.Contains(err.Error(), "container not reachable") {
		t.Errorf("error = %q, want 'container not reachable'", err.Error())
	}
}

func TestRunCommandEmptyCommand(t *testing.T) {
	mc := &mockInstance{name: "test-container"}
	tool := NewRunCommandTool(mc)

	result, err := tool.Execute(context.Background(),`{"command": ""}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "error: command is required") {
		t.Errorf("Output = %q, want 'error: command is required'", result.Output)
	}
}

func TestRunCommandInvalidJSON(t *testing.T) {
	mc := &mockInstance{name: "test-container"}
	tool := NewRunCommandTool(mc)

	_, err := tool.Execute(context.Background(),`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestRunCommandTruncation(t *testing.T) {
	// Create output larger than 50000 bytes.
	bigOutput := strings.Repeat("x", 60000)
	mc := &mockInstance{
		name: "test-container",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{Output: bigOutput, ExitCode: 0}, nil
		},
	}

	tool := NewRunCommandTool(mc)
	result, err := tool.Execute(context.Background(),`{"command": "big"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(result.Output, "...[truncated]") {
		t.Error("expected truncation marker")
	}
	if len(result.Output) > 60000 {
		t.Errorf("output too large: %d bytes", len(result.Output))
	}
}

func TestRunCommandDefinition(t *testing.T) {
	mc := &mockInstance{name: "test-container"}
	tool := NewRunCommandTool(mc)

	if tool.Name() != "run_command" {
		t.Errorf("Name = %q, want %q", tool.Name(), "run_command")
	}

	def := tool.Definition()
	if def.Type != "function" {
		t.Errorf("Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "run_command" {
		t.Errorf("Function.Name = %q, want %q", def.Function.Name, "run_command")
	}
}

func TestReportResultSuccess(t *testing.T) {
	tool := NewReportResultTool()
	result, err := tool.Execute(context.Background(),`{
		"reproduced": true,
		"explanation": "Bug reproduced by installing snap X and doing Y.",
		"script": "#!/bin/bash\nsnap install X\ndo Y"
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true")
	}
	if result.StopMessage != "LLM reported result" {
		t.Errorf("StopMessage = %q, want %q", result.StopMessage, "LLM reported result")
	}
	if tool.Result == nil {
		t.Fatal("Result should be set")
	}
	if !tool.Result.Reproduced {
		t.Error("Reproduced should be true")
	}
	if !strings.Contains(tool.Result.Explanation, "Bug reproduced") {
		t.Errorf("Explanation = %q", tool.Result.Explanation)
	}
	if !strings.Contains(tool.Result.Script, "snap install X") {
		t.Errorf("Script = %q", tool.Result.Script)
	}
}

func TestReportResultNotReproduced(t *testing.T) {
	tool := NewReportResultTool()
	result, err := tool.Execute(context.Background(),`{
		"reproduced": false,
		"explanation": "Could not reproduce. Needs specific hardware.",
		"script": "#!/bin/bash\necho 'best attempt'"
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true")
	}
	if tool.Result.Reproduced {
		t.Error("Reproduced should be false")
	}
}

func TestReportResultInvalidJSON(t *testing.T) {
	tool := NewReportResultTool()
	_, err := tool.Execute(context.Background(),`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReportResultDefinition(t *testing.T) {
	tool := NewReportResultTool()

	if tool.Name() != "report_result" {
		t.Errorf("Name = %q, want %q", tool.Name(), "report_result")
	}

	def := tool.Definition()
	if def.Type != "function" {
		t.Errorf("Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "report_result" {
		t.Errorf("Function.Name = %q, want %q", def.Function.Name, "report_result")
	}
}

func TestToolExecutorDispatch(t *testing.T) {
	mc := &mockInstance{
		name: "test-container",
		execFunc: func(command string) (*ExecResult, error) {
			return &ExecResult{Output: "hello\n", ExitCode: 0}, nil
		},
	}
	runCmd := NewRunCommandTool(mc)
	report := NewReportResultTool()
	executor := NewToolExecutor(runCmd, report)

	// Test tool definitions.
	defs := executor.ToolDefinitions()
	if len(defs) != 2 {
		t.Fatalf("ToolDefinitions = %d, want 2", len(defs))
	}

	// Dispatch run_command.
	result, err := executor.Execute(context.Background(),"run_command", `{"command": "echo hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "hello") {
		t.Errorf("Output = %q, want to contain 'hello'", result.Output)
	}

	// Dispatch report_result.
	result, err = executor.Execute(context.Background(),"report_result", `{"reproduced": true, "explanation": "done", "script": "echo done"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true for report_result")
	}
}

func TestToolExecutorUnknownTool(t *testing.T) {
	executor := NewToolExecutor()
	result, err := executor.Execute(context.Background(),"nonexistent", `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "unknown tool") {
		t.Errorf("Output = %q, want 'unknown tool'", result.Output)
	}
}

// --- read_file tests ---

func TestReadFileSuccess(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "journal.log"), []byte("snapd error log content"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	result, err := tool.Execute(context.Background(),`{"path": "journal.log"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Output != "snapd error log content" {
		t.Errorf("Output = %q, want %q", result.Output, "snapd error log content")
	}
	if result.StopAgent {
		t.Error("StopAgent should be false")
	}
	if result.Summary != "journal.log" {
		t.Errorf("Summary = %q, want %q", result.Summary, "journal.log")
	}
}

func TestReadFileNotFound(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	result, err := tool.Execute(context.Background(),`{"path": "nonexistent.txt"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "file not found") {
		t.Errorf("Output = %q, want 'file not found'", result.Output)
	}
}

func TestReadFilePathEscape(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	result, err := tool.Execute(context.Background(),`{"path": "../../../etc/passwd"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "escapes the bug directory") {
		t.Errorf("Output = %q, want 'escapes the bug directory'", result.Output)
	}
}

func TestReadFileEmptyPath(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	result, err := tool.Execute(context.Background(),`{"path": ""}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "error: path is required") {
		t.Errorf("Output = %q, want 'error: path is required'", result.Output)
	}
}

func TestReadFileTruncation(t *testing.T) {
	dir := t.TempDir()
	bigContent := strings.Repeat("x", 120000)
	if err := os.WriteFile(filepath.Join(dir, "big.log"), []byte(bigContent), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFileTool(dir)
	result, err := tool.Execute(context.Background(),`{"path": "big.log"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(result.Output, "...[truncated]") {
		t.Error("expected truncation marker")
	}
	if len(result.Output) > 120000 {
		t.Errorf("output too large: %d bytes", len(result.Output))
	}
}

func TestReadFileInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	tool := NewReadFileTool(dir)

	_, err := tool.Execute(context.Background(),`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReadFileDefinition(t *testing.T) {
	tool := NewReadFileTool("/tmp")

	if tool.Name() != "read_file" {
		t.Errorf("Name = %q, want %q", tool.Name(), "read_file")
	}

	def := tool.Definition()
	if def.Type != "function" {
		t.Errorf("Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "read_file" {
		t.Errorf("Function.Name = %q, want %q", def.Function.Name, "read_file")
	}
}

// --- report_plan tests ---

func TestReportPlanSuccess(t *testing.T) {
	tool := NewReportPlanTool()
	result, err := tool.Execute(context.Background(),`{
		"instance_type": "vm",
		"ubuntu_version": "24.04",
		"steps": [
			{"description": "Install snapd", "command": "apt-get install -y snapd"},
			{"description": "Check snap list", "command": "snap list"}
		],
		"expected_result": "snap list output wraps at 80 columns",
		"attachments_reviewed": ["journal.log"]
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true")
	}
	if result.StopMessage != "LLM reported plan" {
		t.Errorf("StopMessage = %q, want %q", result.StopMessage, "LLM reported plan")
	}
	if tool.Plan == nil {
		t.Fatal("Plan should be set")
	}
	if tool.Plan.InstanceType != "vm" {
		t.Errorf("InstanceType = %q, want %q", tool.Plan.InstanceType, "vm")
	}
	if tool.Plan.UbuntuVersion != "24.04" {
		t.Errorf("UbuntuVersion = %q, want %q", tool.Plan.UbuntuVersion, "24.04")
	}
	if len(tool.Plan.Steps) != 2 {
		t.Fatalf("Steps = %d, want 2", len(tool.Plan.Steps))
	}
	if tool.Plan.Steps[0].Command != "apt-get install -y snapd" {
		t.Errorf("Step[0].Command = %q", tool.Plan.Steps[0].Command)
	}
	if tool.Plan.ExpectedResult != "snap list output wraps at 80 columns" {
		t.Errorf("ExpectedResult = %q", tool.Plan.ExpectedResult)
	}
	if len(tool.Plan.AttachmentsReviewed) != 1 || tool.Plan.AttachmentsReviewed[0] != "journal.log" {
		t.Errorf("AttachmentsReviewed = %v", tool.Plan.AttachmentsReviewed)
	}
}

func TestReportPlanContainer(t *testing.T) {
	tool := NewReportPlanTool()
	result, err := tool.Execute(context.Background(),`{
		"instance_type": "container",
		"ubuntu_version": "22.04",
		"steps": [{"description": "test", "command": "echo test"}],
		"expected_result": "test passes"
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true")
	}
	if tool.Plan.InstanceType != "container" {
		t.Errorf("InstanceType = %q, want %q", tool.Plan.InstanceType, "container")
	}
}

func TestReportPlanNoAttachments(t *testing.T) {
	tool := NewReportPlanTool()
	result, err := tool.Execute(context.Background(),`{
		"ubuntu_version": "22.04",
		"steps": [{"description": "test", "command": "echo test"}],
		"expected_result": "test passes"
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StopAgent {
		t.Error("StopAgent should be true")
	}
	if tool.Plan.UbuntuVersion != "22.04" {
		t.Errorf("UbuntuVersion = %q, want %q", tool.Plan.UbuntuVersion, "22.04")
	}
	if tool.Plan.AttachmentsReviewed != nil {
		t.Errorf("AttachmentsReviewed should be nil, got %v", tool.Plan.AttachmentsReviewed)
	}
}

func TestReportPlanInvalidJSON(t *testing.T) {
	tool := NewReportPlanTool()
	_, err := tool.Execute(context.Background(),`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReportPlanDefinition(t *testing.T) {
	tool := NewReportPlanTool()

	if tool.Name() != "report_plan" {
		t.Errorf("Name = %q, want %q", tool.Name(), "report_plan")
	}

	def := tool.Definition()
	if def.Type != "function" {
		t.Errorf("Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "report_plan" {
		t.Errorf("Function.Name = %q, want %q", def.Function.Name, "report_plan")
	}
}

// --- parseRevisionMap tests ---

const testRevisionData = `REVISION  VERSION                     ARCH     STATUS                 CREATED
--------  --------------------------  -------  ---------------------  ----------
100       2.63.1                      amd64    Published              2024-03-15
99        2.63.1                      arm64    Published              2024-03-15
98        2.63                        amd64    Published              2024-03-10
97        2.63                        arm64    Published              2024-03-10
96        2.62.1                      amd64    Published              2024-02-20
95        2.62.1                      armhf    Published              2024-02-20
94        2.62                        amd64    AutomaticallyRejected  2024-02-15

Total: 7 revisions
`

func TestParseRevisionMap(t *testing.T) {
	revs, err := parseRevisionMap(testRevisionData)
	if err != nil {
		t.Fatalf("parseRevisionMap failed: %v", err)
	}
	if len(revs) != 7 {
		t.Fatalf("expected 7 revisions, got %d", len(revs))
	}

	// Check first entry.
	r := revs[0]
	if r.Revision != 100 {
		t.Errorf("Revision = %d, want 100", r.Revision)
	}
	if r.Version != "2.63.1" {
		t.Errorf("Version = %q, want %q", r.Version, "2.63.1")
	}
	if r.Arch != "amd64" {
		t.Errorf("Arch = %q, want %q", r.Arch, "amd64")
	}
	if r.Status != "Published" {
		t.Errorf("Status = %q, want %q", r.Status, "Published")
	}
	expected := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	if !r.Created.Equal(expected) {
		t.Errorf("Created = %v, want %v", r.Created, expected)
	}

	// Check AutomaticallyRejected status is parsed correctly.
	r = revs[6]
	if r.Revision != 94 {
		t.Errorf("Revision = %d, want 94", r.Revision)
	}
	if r.Status != "AutomaticallyRejected" {
		t.Errorf("Status = %q, want %q", r.Status, "AutomaticallyRejected")
	}
}

func TestParseRevisionMapEmpty(t *testing.T) {
	_, err := parseRevisionMap("")
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "no revisions parsed") {
		t.Errorf("error = %q, want 'no revisions parsed'", err.Error())
	}
}

func TestParseRevisionMapHeaderOnly(t *testing.T) {
	data := "REVISION  VERSION  ARCH  STATUS  CREATED\n--------  -------  ----  ------  -------\n"
	_, err := parseRevisionMap(data)
	if err == nil {
		t.Fatal("expected error for header-only input")
	}
}

func TestParseRevisionMapSkipsMalformed(t *testing.T) {
	data := `REVISION  VERSION  ARCH  STATUS  CREATED
--------  -------  ----  ------  -------
100       2.63.1   amd64 Published 2024-03-15
bad line
99        2.63     amd64 Published 2024-03-10
`
	revs, err := parseRevisionMap(data)
	if err != nil {
		t.Fatalf("parseRevisionMap failed: %v", err)
	}
	if len(revs) != 2 {
		t.Errorf("expected 2 revisions (skipping malformed), got %d", len(revs))
	}
}

// --- QueryRevisionsTool tests ---

func testRevisions() []SnapdRevision {
	revs, _ := parseRevisionMap(testRevisionData)
	return revs
}

func TestQueryRevisionsToolDefinition(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	if tool.Name() != "query_snapd_revisions" {
		t.Errorf("Name = %q, want %q", tool.Name(), "query_snapd_revisions")
	}

	def := tool.Definition()
	if def.Type != "function" {
		t.Errorf("Type = %q, want %q", def.Type, "function")
	}
	if def.Function.Name != "query_snapd_revisions" {
		t.Errorf("Function.Name = %q, want %q", def.Function.Name, "query_snapd_revisions")
	}
}

func TestQueryRevisionsToolByRevision(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"revision": 100}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StopAgent {
		t.Error("StopAgent should be false")
	}
	if !strings.Contains(result.Output, "2.63.1") {
		t.Errorf("output should contain version 2.63.1, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "amd64") {
		t.Errorf("output should contain arch amd64, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "1 results") {
		t.Errorf("should find exactly 1 result, got %q", result.Summary)
	}
}

func TestQueryRevisionsToolByVersion(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"version": "2.63"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "2.63" prefix matches 2.63 and 2.63.1 — 4 entries total.
	if !strings.Contains(result.Output, "4 results") {
		t.Errorf("expected 4 results for version prefix '2.63', got output: %q", result.Output)
	}
}

func TestQueryRevisionsToolByVersionExact(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	// "2.62.1" should match only 2.62.1 entries, not 2.62.
	result, err := tool.Execute(context.Background(), `{"version": "2.62.1"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "2 results") {
		t.Errorf("expected 2 results for version '2.62.1', got output: %q", result.Output)
	}
}

func TestQueryRevisionsToolByArch(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"architecture": "arm64"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "2 results") {
		t.Errorf("expected 2 arm64 results, got output: %q", result.Output)
	}
	// Should not contain amd64 entries.
	lines := strings.Split(result.Output, "\n")
	for _, line := range lines[2:] { // skip header
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "\n") || strings.Contains(line, "results") {
			continue
		}
		if strings.Contains(line, "amd64") {
			t.Errorf("arm64 filter should not include amd64 entries: %q", line)
		}
	}
}

func TestQueryRevisionsToolByDateRange(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"since": "2024-03-01", "until": "2024-03-11"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should match 2024-03-10 entries (rev 97, 98) but not 2024-03-15 or 2024-02-*.
	if !strings.Contains(result.Output, "98") {
		t.Errorf("should include revision 98, got: %q", result.Output)
	}
	if strings.Contains(result.Output, "100") {
		t.Errorf("should not include revision 100 (2024-03-15), got: %q", result.Output)
	}
	if strings.Contains(result.Output, "96") {
		t.Errorf("should not include revision 96 (2024-02-20), got: %q", result.Output)
	}
}

func TestQueryRevisionsToolCombinedFilters(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	// Filter: version=2.63, arch=amd64 → should match rev 98 and 100.
	result, err := tool.Execute(context.Background(), `{"version": "2.63", "architecture": "amd64"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "2 results") {
		t.Errorf("expected 2 results for version=2.63 + arch=amd64, got: %q", result.Output)
	}
}

func TestQueryRevisionsToolNoFilters(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "error: at least one filter") {
		t.Errorf("expected error for no filters, got: %q", result.Output)
	}
}

func TestQueryRevisionsToolInvalidJSON(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	_, err := tool.Execute(context.Background(), `not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestQueryRevisionsToolInvalidSinceDate(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"since": "not-a-date"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "error: invalid 'since' date") {
		t.Errorf("expected date format error, got: %q", result.Output)
	}
}

func TestQueryRevisionsToolInvalidUntilDate(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"until": "2024/03/15"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "error: invalid 'until' date") {
		t.Errorf("expected date format error, got: %q", result.Output)
	}
}

func TestQueryRevisionsToolNoResults(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"version": "9.99"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "No revisions found") {
		t.Errorf("expected 'No revisions found', got: %q", result.Output)
	}
	if result.Summary != "0 results" {
		t.Errorf("Summary = %q, want %q", result.Summary, "0 results")
	}
}

func TestQueryRevisionsToolRevisionNotFound(t *testing.T) {
	tool := NewQueryRevisionsTool(testRevisions())

	result, err := tool.Execute(context.Background(), `{"revision": 99999}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "No revisions found") {
		t.Errorf("expected 'No revisions found', got: %q", result.Output)
	}
}

func TestQueryRevisionsToolTruncation(t *testing.T) {
	// Create more than maxRevisionResults entries.
	var revs []SnapdRevision
	for i := 0; i < maxRevisionResults+50; i++ {
		revs = append(revs, SnapdRevision{
			Revision: 10000 + i,
			Version:  "2.70",
			Arch:     "amd64",
			Status:   "Published",
			Created:  time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		})
	}

	tool := NewQueryRevisionsTool(revs)
	result, err := tool.Execute(context.Background(), `{"version": "2.70"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "narrow your filters") {
		t.Errorf("expected truncation notice, got: %q", result.Output)
	}
	if !strings.Contains(result.Summary, "truncated") {
		t.Errorf("Summary should indicate truncation, got: %q", result.Summary)
	}
}

func TestSummariseCmd(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"snap list", "snap list"},
		{"COLUMNS=80 snap list", "snap list"},
		{"COLUMNS=80 snap list 2>&1 | cat -A | head -10", "snap list"},
		{"DEBIAN_FRONTEND=noninteractive apt-get install -y snapd", "apt-get install -y snapd"},
		{"echo hello > /dev/null", "echo hello > /dev/null"}, // > is not stripped (only specific redirects)
		{"snap version 2>&1", "snap version"},
		{"cat /var/log/syslog | grep snapd | tail -20", "cat /var/log/syslog"},
		{"A=1 B=2 some-cmd --flag", "some-cmd --flag"},
		{"echo hello\necho world", "echo hello"},
		{"", ""},
	}

	for _, tt := range tests {
		got := summariseCmd(tt.input)
		if got != tt.want {
			t.Errorf("summariseCmd(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
