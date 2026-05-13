package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Legacy BuildSystemPrompt tests (kept for backward compatibility) ---

func TestBuildSystemPromptBasic(t *testing.T) {
	bug := &Bug{
		ID:          12345,
		Title:       "snap refresh hangs",
		Description: "Running snap refresh causes the system to hang.",
		WebLink:     "https://bugs.launchpad.net/snapd/+bug/12345",
		Tags:        []string{"snapd", "refresh"},
	}

	prompt := BuildSystemPrompt(bug, "snapd-repro-abc123")

	checks := []string{
		"expert Ubuntu/snapd bug reproduction agent",
		"snapd-repro-abc123",
		"Bug ID:** 12345",
		"Title:** snap refresh hangs",
		"https://bugs.launchpad.net/snapd/+bug/12345",
		"Tags:** snapd, refresh",
		"snap refresh causes the system to hang",
		"Snapd Domain Knowledge",
		"journalctl -u snapd",
		"apt-get update",
		"report_result",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("prompt missing %q", check)
		}
	}
}

func TestBuildSystemPromptWithMessages(t *testing.T) {
	bug := &Bug{
		ID:          99999,
		Title:       "terminal width issue",
		Description: "Terminal width is wrong.",
		Messages: []Message{
			{
				Author:      "alice",
				DateCreated: "2024-01-15T10:00:00Z",
				Subject:     "terminal width issue",
				Content:     "I see this bug on 24.04.",
			},
			{
				Author:      "bob",
				DateCreated: "2024-01-16T12:00:00Z",
				Subject:     "Re: terminal width issue",
				Content:     "Can confirm on 22.04 as well.",
			},
		},
	}

	prompt := BuildSystemPrompt(bug, "test-container")

	if !strings.Contains(prompt, "Comment #0") {
		t.Error("missing comment #0")
	}
	if !strings.Contains(prompt, "Comment #1") {
		t.Error("missing comment #1")
	}
	if !strings.Contains(prompt, "alice") {
		t.Error("missing author alice")
	}
	if !strings.Contains(prompt, "Can confirm on 22.04") {
		t.Error("missing bob's comment content")
	}
	if !strings.Contains(prompt, "Subject: Re: terminal width issue") {
		t.Error("missing Re: subject line")
	}
}

func TestBuildSystemPromptWithAttachments(t *testing.T) {
	bug := &Bug{
		ID:          55555,
		Title:       "snapd crash",
		Description: "snapd crashes on startup.",
		Attachments: []Attachment{
			{
				Title:    "journal.log",
				Type:     "Patch",
				FilePath: "/tmp/bug-55555/journal.log",
			},
			{
				Title: "screenshot.png",
				Type:  "Unspecified",
			},
		},
	}

	prompt := BuildSystemPrompt(bug, "test-container")

	if !strings.Contains(prompt, "journal.log") {
		t.Error("missing attachment title")
	}
	if !strings.Contains(prompt, "/tmp/bug-55555/journal.log") {
		t.Error("missing attachment file path")
	}
	if !strings.Contains(prompt, "screenshot.png") {
		t.Error("missing second attachment")
	}
	if !strings.Contains(prompt, "Attachment files have been downloaded") {
		t.Error("missing attachment note")
	}
}

func TestBuildSystemPromptNoOptionalFields(t *testing.T) {
	bug := &Bug{
		ID:          11111,
		Title:       "minimal bug",
		Description: "Just a description.",
	}

	prompt := BuildSystemPrompt(bug, "container-x")

	if !strings.Contains(prompt, "Bug ID:** 11111") {
		t.Error("missing bug ID")
	}
	if strings.Contains(prompt, "Tags:**") {
		t.Error("should not have Tags when empty")
	}
	if strings.Contains(prompt, "### Comments") {
		t.Error("should not have Comments section when empty")
	}
	if strings.Contains(prompt, "### Attachments") {
		t.Error("should not have Attachments section when empty")
	}
}

func TestBuildUserMessage(t *testing.T) {
	bug := &Bug{
		ID:    12345,
		Title: "snap refresh hangs",
	}

	msg := BuildUserMessage(bug)

	if !strings.Contains(msg, "12345") {
		t.Error("missing bug ID")
	}
	if !strings.Contains(msg, "snap refresh hangs") {
		t.Error("missing bug title")
	}
	if !strings.Contains(msg, "report_result") {
		t.Error("missing report_result instruction")
	}
}

// --- Planning prompt tests ---

func TestBuildPlanningPrompt(t *testing.T) {
	bug := &Bug{
		ID:          1662786,
		Title:       "snap list output hard to read",
		Description: "Terminal width formatting issue.",
		Tags:        []string{"noble", "resolute"},
		Attachments: []Attachment{
			{Title: "journal.log", Type: "Patch", FilePath: "journal.log"},
		},
	}

	prompt := BuildPlanningPrompt(bug)

	checks := []string{
		"expert Ubuntu/snapd bug analysis agent",
		"read_file",
		"report_plan",
		"Bug ID:** 1662786",
		"snap list output hard to read",
		"noble, resolute",
		"journal.log",
		"Codename to Version Mapping",
		"noble = 24.04",
		"jammy = 22.04",
		"focal = 20.04",
		"Snapd Domain Knowledge",
		"journalctl -u snapd",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("planning prompt missing %q", check)
		}
	}
}

func TestBuildPlanningPromptNoAttachments(t *testing.T) {
	bug := &Bug{
		ID:          99999,
		Title:       "simple bug",
		Description: "No attachments.",
	}

	prompt := BuildPlanningPrompt(bug)

	if strings.Contains(prompt, "### Attachments") {
		t.Error("should not have Attachments section when empty")
	}
	if strings.Contains(prompt, "Use the read_file tool to inspect") {
		t.Error("should not have read_file instruction when no attachments")
	}
	if !strings.Contains(prompt, "There are no attachments to review") {
		t.Error("should explicitly state there are no attachments")
	}
}

func TestBuildPlanningUserMessageNoAttachments(t *testing.T) {
	bug := &Bug{
		ID:    1662786,
		Title: "snap list output hard to read",
	}

	msg := BuildPlanningUserMessage(bug)

	if !strings.Contains(msg, "1662786") {
		t.Error("missing bug ID")
	}
	if !strings.Contains(msg, "snap list output hard to read") {
		t.Error("missing bug title")
	}
	if !strings.Contains(msg, "report_plan") {
		t.Error("missing report_plan instruction")
	}
	if strings.Contains(msg, "read_file") {
		t.Error("should not mention read_file when no attachments")
	}
}

func TestBuildPlanningUserMessageWithAttachments(t *testing.T) {
	bug := &Bug{
		ID:    1662786,
		Title: "snap list output hard to read",
		Attachments: []Attachment{
			{Title: "journal.log", Type: "Patch", FilePath: "journal.log"},
		},
	}

	msg := BuildPlanningUserMessage(bug)

	if !strings.Contains(msg, "read_file") {
		t.Error("should mention read_file when there are attachments")
	}
}

// --- Execution prompt tests ---

func TestBuildExecutionPrompt(t *testing.T) {
	plan := &ReproPlan{
		BugID:         1662786,
		Title:         "snap list output hard to read",
		UbuntuVersion: "24.04",
		Steps: []PlanStep{
			{Description: "Update packages", Command: "apt-get update"},
			{Description: "Check snap list", Command: "stty cols 80 && snap list"},
		},
		ExpectedResult: "snap list wraps poorly at 80 columns",
	}

	prompt := BuildExecutionPrompt(plan, "snapd-repro-xyz")

	checks := []string{
		"expert Ubuntu/snapd bug reproduction agent",
		"snapd-repro-xyz",
		"Ubuntu 24.04",
		"run_command",
		"report_result",
		"Reproduction Plan",
		"Bug ID:** 1662786",
		"snap list output hard to read",
		"Step 1:** Update packages",
		"apt-get update",
		"Step 2:** Check snap list",
		"stty cols 80 && snap list",
		"snap list wraps poorly at 80 columns",
		"Snapd Domain Knowledge",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("execution prompt missing %q", check)
		}
	}
}

func TestBuildExecutionUserMessage(t *testing.T) {
	plan := &ReproPlan{
		BugID: 1662786,
		Title: "snap list output hard to read",
	}

	msg := BuildExecutionUserMessage(plan)

	if !strings.Contains(msg, "1662786") {
		t.Error("missing bug ID")
	}
	if !strings.Contains(msg, "snap list output hard to read") {
		t.Error("missing bug title")
	}
	if !strings.Contains(msg, "report_result") {
		t.Error("missing report_result instruction")
	}
}

// --- Plan serialization tests ---

func TestSaveAndLoadPlan(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")

	plan := &ReproPlan{
		BugID:         12345,
		Title:         "test bug",
		UbuntuVersion: "24.04",
		Steps: []PlanStep{
			{Description: "step one", Command: "echo one"},
			{Description: "step two", Command: "echo two"},
		},
		ExpectedResult:      "something happens",
		AttachmentsReviewed: []string{"log.txt"},
		ModelUsed:           "test-model",
	}

	if err := SavePlan(plan, path); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}

	loaded, err := LoadPlan(path)
	if err != nil {
		t.Fatalf("LoadPlan failed: %v", err)
	}

	if loaded.BugID != plan.BugID {
		t.Errorf("BugID = %d, want %d", loaded.BugID, plan.BugID)
	}
	if loaded.Title != plan.Title {
		t.Errorf("Title = %q, want %q", loaded.Title, plan.Title)
	}
	if loaded.UbuntuVersion != plan.UbuntuVersion {
		t.Errorf("UbuntuVersion = %q, want %q", loaded.UbuntuVersion, plan.UbuntuVersion)
	}
	if len(loaded.Steps) != 2 {
		t.Fatalf("Steps = %d, want 2", len(loaded.Steps))
	}
	if loaded.Steps[0].Command != "echo one" {
		t.Errorf("Steps[0].Command = %q", loaded.Steps[0].Command)
	}
	if loaded.ExpectedResult != plan.ExpectedResult {
		t.Errorf("ExpectedResult = %q", loaded.ExpectedResult)
	}
	if loaded.ModelUsed != plan.ModelUsed {
		t.Errorf("ModelUsed = %q", loaded.ModelUsed)
	}
}

func TestLoadPlanNotFound(t *testing.T) {
	_, err := LoadPlan("/nonexistent/plan.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadPlanInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := LoadPlan(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSavePlanCreatesValidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.json")

	plan := &ReproPlan{
		BugID:          1,
		UbuntuVersion:  "22.04",
		Steps:          []PlanStep{{Description: "test", Command: "echo hi"}},
		ExpectedResult: "output",
	}

	if err := SavePlan(plan, path); err != nil {
		t.Fatalf("SavePlan failed: %v", err)
	}

	data, _ := os.ReadFile(path)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if raw["ubuntu_version"] != "22.04" {
		t.Errorf("ubuntu_version = %v", raw["ubuntu_version"])
	}
}

// --- Codename table tests ---

func TestUbuntuCodenamesTable(t *testing.T) {
	expected := map[string]string{
		"noble":  "24.04",
		"jammy":  "22.04",
		"focal":  "20.04",
		"bionic": "18.04",
		"xenial": "16.04",
	}
	for codename, version := range expected {
		if got, ok := UbuntuCodenames[codename]; !ok {
			t.Errorf("missing codename %q", codename)
		} else if got != version {
			t.Errorf("UbuntuCodenames[%q] = %q, want %q", codename, got, version)
		}
	}
}
