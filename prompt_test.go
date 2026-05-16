package main

import (
	"strings"
	"testing"
)

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

// --- Single-phase reproduce prompt tests ---

func TestBuildReproducePrompt(t *testing.T) {
	bug := &Bug{
		ID:          1662786,
		Title:       "snap list output hard to read",
		Description: "Terminal width formatting issue.",
		Tags:        []string{"noble", "resolute"},
		Attachments: []Attachment{
			{Title: "journal.log", Type: "Patch", FilePath: "journal.log"},
		},
	}

	prompt := BuildReproducePrompt(bug, "test-instance", nil)

	checks := []string{
		// Role and environment.
		"expert Ubuntu/snapd bug reproduction agent",
		"analyze a Launchpad bug report and reproduce the ORIGINAL BUG",
		"test-instance",
		"Ubuntu " + defaultUbuntuVersion,
		"virtual machine",

		// Tools.
		"run_command",
		"read_file",
		"describe_skill",
		"load_skill",
		"query_snapd_revisions",
		"relaunch_vm",
		"report_result",

		// Bug report.
		"Bug ID:** 1662786",
		"snap list output hard to read",
		"noble, resolute",
		"journal.log",

		// Reference data.
		"Codename to Version Mapping",
		"noble = 24.04",
		"jammy = 22.04",
		"focal = 20.04",
		"Snapd Domain Knowledge",
		"journalctl -u snapd",

		// Methodology sections.
		"## Reproduction Methodology",
		"Identify the marker",
		"Check out the correct source version",
		"Trace the marker to source code",
		"Understand the execution path",
		"Identify the triggering conditions",
		"Instrument for observability",
		"Trigger the conditions",
		"MANDATORY: Steps 1-5 must be completed",
		"Anti-pattern: blind timing loops",

		// Environment verification.
		"Verify the environment",
		"lsb_release",
		"snap version",
		"aa-status",

		// Code version awareness.
		"Check out the correct source version",
		"Snapd releases are tagged with the version number",
		"tracing the wrong version",

		// Instrumentation strategy.
		"logger.Noticef markers",
		"Problem marker",
		"Trigger marker",
		"timestamp gap",

		// Incremental approach.
		"## Incremental Approach",
		"Start with code analysis",

		// Troubleshooting.
		"## Troubleshooting and Adaptation",
		"Do NOT report failure after a single error",

		// Critical directives.
		"Reproduce the Bug, Not the Fix",
		"Do NOT install fixed versions",
		"NEVER ask the user for permission",
	}
	for _, check := range checks {
		if !strings.Contains(prompt, check) {
			t.Errorf("reproduce prompt missing %q", check)
		}
	}

	// Skill review should be step 3 in instructions (after env verification).
	if !strings.Contains(prompt, "3. BEFORE investigating, review the available skills") {
		t.Error("reproduce prompt should have skill review at step 3")
	}

	// Version relaunch should be mentioned in instructions.
	if !strings.Contains(prompt, "call relaunch_vm to get a VM with the correct Ubuntu version") {
		t.Error("reproduce prompt should instruct to use relaunch_vm for version changes")
	}

	// Attachments instruction should be present.
	if !strings.Contains(prompt, "Use the read_file tool to inspect any of the attachments") {
		t.Error("reproduce prompt should have read_file instruction for attachments")
	}
}

func TestBuildReproducePromptNoAttachments(t *testing.T) {
	bug := &Bug{
		ID:          99999,
		Title:       "simple bug",
		Description: "No attachments.",
	}

	prompt := BuildReproducePrompt(bug, "test-instance", nil)

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

func TestBuildReproducePromptContainsAllToolNames(t *testing.T) {
	bug := &Bug{
		ID:          12345,
		Title:       "test",
		Description: "test",
	}

	prompt := BuildReproducePrompt(bug, "test-vm", nil)

	tools := []string{
		"run_command", "read_file", "describe_skill", "load_skill",
		"query_snapd_revisions", "relaunch_vm", "report_result",
	}
	for _, tool := range tools {
		if !strings.Contains(prompt, tool) {
			t.Errorf("reproduce prompt missing tool %q", tool)
		}
	}

	// Should NOT mention report_plan (that's the old two-phase tool).
	if strings.Contains(prompt, "report_plan") {
		t.Error("reproduce prompt should not mention report_plan")
	}
}

func TestBuildReproduceUserMessage(t *testing.T) {
	bug := &Bug{
		ID:    1662786,
		Title: "snap list output hard to read",
	}

	msg := BuildReproduceUserMessage(bug)

	if !strings.Contains(msg, "1662786") {
		t.Error("missing bug ID")
	}
	if !strings.Contains(msg, "snap list output hard to read") {
		t.Error("missing bug title")
	}
	if !strings.Contains(msg, "report_result") {
		t.Error("missing report_result instruction")
	}
	if !strings.Contains(msg, "describe_skill") {
		t.Error("user message should nudge skill review")
	}
	if !strings.Contains(msg, "relaunch_vm") {
		t.Error("user message should mention relaunch_vm")
	}
	if strings.Contains(msg, "read_file") {
		t.Error("should not mention read_file when no attachments")
	}
}

func TestBuildReproduceUserMessageWithAttachments(t *testing.T) {
	bug := &Bug{
		ID:    1662786,
		Title: "snap list output hard to read",
		Attachments: []Attachment{
			{Title: "journal.log", Type: "Patch", FilePath: "journal.log"},
		},
	}

	msg := BuildReproduceUserMessage(bug)

	if !strings.Contains(msg, "read_file") {
		t.Error("should mention read_file when there are attachments")
	}
}
