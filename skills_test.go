package main

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// testSkillsJSON is a minimal skills.json for testing.
var testSkillsJSON = []byte(`{
  "skills": [
    {
      "name": "snap-refresh",
      "keywords": ["refresh", "update", "held"],
      "summary": "Debugging snap refresh failures.",
      "content_file": "skills/snap-refresh.md"
    },
    {
      "name": "journalctl",
      "keywords": ["logs", "journal"],
      "summary": "Using journalctl for snapd log analysis.",
      "content_file": "skills/journalctl.md"
    }
  ]
}`)

func testSkillFS() fstest.MapFS {
	return fstest.MapFS{
		"skills/snap-refresh.md": &fstest.MapFile{
			Data: []byte("# Snap Refresh Debugging\n\nDetailed commands here."),
		},
		"skills/journalctl.md": &fstest.MapFile{
			Data: []byte("# Journalctl for Snapd\n\nLog analysis commands."),
		},
	}
}

// --- SkillIndex tests ---

func TestNewSkillIndex(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	if len(idx.skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(idx.skills))
	}
	if len(idx.order) != 2 {
		t.Errorf("expected 2 ordered entries, got %d", len(idx.order))
	}
}

func TestNewSkillIndexInvalidJSON(t *testing.T) {
	_, err := NewSkillIndex([]byte("not json"), testSkillFS())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSkillIndexList(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	list := idx.List()

	if !strings.Contains(list, "snap-refresh") {
		t.Error("list missing snap-refresh")
	}
	if !strings.Contains(list, "journalctl") {
		t.Error("list missing journalctl")
	}
	if !strings.Contains(list, "refresh, update, held") {
		t.Error("list missing keywords for snap-refresh")
	}
	if !strings.Contains(list, "logs, journal") {
		t.Error("list missing keywords for journalctl")
	}
}

func TestSkillIndexDescribe(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	summary, err := idx.Describe("snap-refresh")
	if err != nil {
		t.Fatalf("Describe failed: %v", err)
	}
	if summary != "Debugging snap refresh failures." {
		t.Errorf("summary = %q", summary)
	}
}

func TestSkillIndexDescribeUnknown(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	_, err = idx.Describe("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(err.Error(), "unknown skill") {
		t.Errorf("error = %q, expected 'unknown skill'", err.Error())
	}
	if !strings.Contains(err.Error(), "snap-refresh") {
		t.Error("error should list available skills")
	}
}

func TestSkillIndexLoad(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	content, err := idx.Load("journalctl")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.Contains(content, "Journalctl for Snapd") {
		t.Errorf("content = %q", content)
	}
}

func TestSkillIndexLoadUnknown(t *testing.T) {
	idx, err := NewSkillIndex(testSkillsJSON, testSkillFS())
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	_, err = idx.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestSkillIndexLoadMissingFile(t *testing.T) {
	// Skills JSON references a file that doesn't exist in the FS.
	badJSON := []byte(`{
	  "skills": [{"name": "bad", "keywords": [], "summary": "test", "content_file": "skills/missing.md"}]
	}`)
	emptyFS := fstest.MapFS{}

	idx, err := NewSkillIndex(badJSON, emptyFS)
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	_, err = idx.Load("bad")
	if err == nil {
		t.Fatal("expected error for missing content file")
	}
}

func TestSkillIndexLoadTruncation(t *testing.T) {
	// Create a skill with content larger than 50000 bytes.
	largeContent := strings.Repeat("x", 60000)
	largeFS := fstest.MapFS{
		"skills/large.md": &fstest.MapFile{
			Data: []byte(largeContent),
		},
	}
	largeJSON := []byte(`{
	  "skills": [{"name": "large", "keywords": [], "summary": "large skill", "content_file": "skills/large.md"}]
	}`)

	idx, err := NewSkillIndex(largeJSON, largeFS)
	if err != nil {
		t.Fatalf("NewSkillIndex failed: %v", err)
	}

	content, err := idx.Load("large")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !strings.HasSuffix(content, "...[truncated]") {
		t.Error("expected truncation suffix")
	}
	if len(content) > 50100 { // 50000 + suffix
		t.Errorf("content too long: %d bytes", len(content))
	}
}

// --- DescribeSkillTool tests ---

func TestDescribeSkillTool(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewDescribeSkillTool(idx)

	if tool.Name() != "describe_skill" {
		t.Errorf("Name() = %q", tool.Name())
	}

	def := tool.Definition()
	if def.Function.Name != "describe_skill" {
		t.Errorf("Definition Name = %q", def.Function.Name)
	}

	result, err := tool.Execute(context.Background(), `{"name": "snap-refresh"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.Output != "Debugging snap refresh failures." {
		t.Errorf("output = %q", result.Output)
	}
	if result.StopAgent {
		t.Error("describe_skill should not stop agent")
	}
}

func TestDescribeSkillToolUnknown(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewDescribeSkillTool(idx)

	result, err := tool.Execute(context.Background(), `{"name": "nonexistent"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "error:") {
		t.Errorf("expected error output, got %q", result.Output)
	}
}

func TestDescribeSkillToolEmptyName(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewDescribeSkillTool(idx)

	result, err := tool.Execute(context.Background(), `{"name": ""}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "error: name is required") {
		t.Errorf("expected 'name is required' error, got %q", result.Output)
	}
}

func TestDescribeSkillToolInvalidJSON(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewDescribeSkillTool(idx)

	_, err := tool.Execute(context.Background(), "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON args")
	}
}

// --- LoadSkillTool tests ---

func TestLoadSkillTool(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewLoadSkillTool(idx)

	if tool.Name() != "load_skill" {
		t.Errorf("Name() = %q", tool.Name())
	}

	def := tool.Definition()
	if def.Function.Name != "load_skill" {
		t.Errorf("Definition Name = %q", def.Function.Name)
	}

	result, err := tool.Execute(context.Background(), `{"name": "journalctl"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "Journalctl for Snapd") {
		t.Errorf("output = %q", result.Output)
	}
	if result.StopAgent {
		t.Error("load_skill should not stop agent")
	}
}

func TestLoadSkillToolUnknown(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewLoadSkillTool(idx)

	result, err := tool.Execute(context.Background(), `{"name": "nonexistent"}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "error:") {
		t.Errorf("expected error output, got %q", result.Output)
	}
}

func TestLoadSkillToolEmptyName(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewLoadSkillTool(idx)

	result, err := tool.Execute(context.Background(), `{"name": ""}`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !strings.Contains(result.Output, "error: name is required") {
		t.Errorf("expected 'name is required' error, got %q", result.Output)
	}
}

func TestLoadSkillToolInvalidJSON(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	tool := NewLoadSkillTool(idx)

	_, err := tool.Execute(context.Background(), "not json")
	if err == nil {
		t.Fatal("expected error for invalid JSON args")
	}
}

// --- writeSkillIndex tests ---

func TestWriteSkillIndex(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())

	var b strings.Builder
	writeSkillIndex(&b, idx)
	output := b.String()

	if !strings.Contains(output, "Available Skills") {
		t.Error("missing header")
	}
	if !strings.Contains(output, "review these before investigating") {
		t.Error("skill index header should emphasize reviewing before investigating")
	}
	if !strings.Contains(output, "expert debugging commands") {
		t.Error("skill index should explain what skills contain")
	}
	if !strings.Contains(output, "snap-refresh") {
		t.Error("missing snap-refresh")
	}
	if !strings.Contains(output, "journalctl") {
		t.Error("missing journalctl")
	}
}

func TestWriteSkillIndexNil(t *testing.T) {
	var b strings.Builder
	writeSkillIndex(&b, nil)

	if b.Len() != 0 {
		t.Errorf("expected empty output for nil skills, got %q", b.String())
	}
}

// --- Integration: prompt with skills ---

func TestBuildReproducePromptWithSkills(t *testing.T) {
	idx, _ := NewSkillIndex(testSkillsJSON, testSkillFS())
	bug := &Bug{
		ID:          12345,
		Title:       "snap refresh issue",
		Description: "Refresh hangs.",
	}

	prompt := BuildReproducePrompt(bug, "test-instance", idx)

	checks := []string{
		"Available Skills",
		"snap-refresh",
		"journalctl",
		"describe_skill",
		"load_skill",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("reproduce prompt missing %q", check)
		}
	}
}
