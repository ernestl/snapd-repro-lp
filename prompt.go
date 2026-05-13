package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// UbuntuCodenames maps Ubuntu codenames to version numbers.
var UbuntuCodenames = map[string]string{
	"noble":    "24.04",
	"mantic":   "23.10",
	"lunar":    "23.04",
	"kinetic":  "22.10",
	"jammy":    "22.04",
	"impish":   "21.10",
	"hirsute":  "21.04",
	"groovy":   "20.10",
	"focal":    "20.04",
	"eoan":     "19.10",
	"disco":    "19.04",
	"cosmic":   "18.10",
	"bionic":   "18.04",
	"artful":   "17.10",
	"zesty":    "17.04",
	"yakkety":  "16.10",
	"xenial":   "16.04",
	"oracular": "24.10",
	"plucky":   "25.04",
	"resolute": "25.10",
}

// writeBugReport appends the bug report details to a string builder.
// This is shared between the planning and execution prompts.
func writeBugReport(b *strings.Builder, bug *Bug) {
	b.WriteString("## Bug Report\n\n")
	fmt.Fprintf(b, "**Bug ID:** %d\n", bug.ID)
	fmt.Fprintf(b, "**Title:** %s\n", bug.Title)
	if bug.WebLink != "" {
		fmt.Fprintf(b, "**URL:** %s\n", bug.WebLink)
	}
	if len(bug.Tags) > 0 {
		fmt.Fprintf(b, "**Tags:** %s\n", strings.Join(bug.Tags, ", "))
	}
	fmt.Fprintf(b, "\n### Description\n\n%s\n", bug.Description)

	// Messages/comments.
	if len(bug.Messages) > 0 {
		b.WriteString("\n### Comments\n\n")
		for i, msg := range bug.Messages {
			author := msg.Author
			if author == "" {
				author = "unknown"
			}
			fmt.Fprintf(b, "**Comment #%d** by %s (%s):\n", i, author, msg.DateCreated)
			if msg.Subject != "" && msg.Subject != bug.Title {
				fmt.Fprintf(b, "Subject: %s\n", msg.Subject)
			}
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		}
	}

	// Attachments listing.
	if len(bug.Attachments) > 0 {
		b.WriteString("### Attachments\n\n")
		for _, att := range bug.Attachments {
			fmt.Fprintf(b, "- **%s** (type: %s)", att.Title, att.Type)
			if att.FilePath != "" {
				fmt.Fprintf(b, " — file: %s", att.FilePath)
			}
			b.WriteString("\n")
		}
	}
}

// writeCodenameTable appends the Ubuntu codename → version mapping table.
func writeCodenameTable(b *strings.Builder) {
	b.WriteString("## Ubuntu Codename to Version Mapping\n\n")
	b.WriteString("Use this to determine the correct Ubuntu version from bug tags or descriptions:\n\n")
	// Write in a useful order (newest first).
	order := []string{
		"resolute", "plucky", "oracular", "noble", "mantic", "lunar",
		"kinetic", "jammy", "impish", "hirsute", "groovy", "focal",
		"eoan", "disco", "cosmic", "bionic", "artful", "zesty",
		"yakkety", "xenial",
	}
	for _, name := range order {
		if ver, ok := UbuntuCodenames[name]; ok {
			fmt.Fprintf(b, "- %s = %s\n", name, ver)
		}
	}
	b.WriteString("\n")
}

// writeSnapdKnowledge appends snapd domain knowledge.
func writeSnapdKnowledge(b *strings.Builder) {
	b.WriteString(`## Snapd Domain Knowledge
- snapd is the daemon that manages snaps on Ubuntu systems.
- Snap packages are in /snap/<name>/<revision>/.
- Snap data is in /var/snap/<name>/<revision>/.
- snapd state is in /var/lib/snapd/.
- snapd logs: "journalctl -u snapd" or "snap changes" / "snap change <id>".
- To refresh snapd itself: "snap refresh snapd" or "snap install snapd --channel=<ch>".
- Snap connections: "snap connections <name>".
- Snap interfaces: "snap interface <name>".
- Common debug: "snap debug state /var/lib/snapd/state.json".
- If a snap refresh is stuck, check "snap changes" and "snap change <id>" for the stuck change.

`)
}

// --- Planning prompt ---

// BuildPlanningPrompt constructs the system prompt for the planning
// phase. The planning LLM analyzes the bug report and attachments to
// produce a structured reproduction plan.
func BuildPlanningPrompt(bug *Bug) string {
	var b strings.Builder

	b.WriteString(`You are an expert Ubuntu/snapd bug analysis agent. Your goal is to analyze a Launchpad bug report and produce a structured reproduction plan.

## Instructions
1. Read the bug report carefully. Understand what the reporter observed and what conditions trigger the bug.
2. If there are attachments (log files, configs, etc.), use the read_file tool to inspect them.
3. Determine which Ubuntu version to use based on the bug tags, description, or comments. Use the codename mapping table below.
4. Plan a step-by-step reproduction strategy using shell commands.
5. Call report_plan with your structured plan.

## Tools Available
- **read_file**: Read an attachment file from the bug directory. Use this to inspect log files, config files, or any other attachments.
- **report_plan**: Output your structured reproduction plan. This ends the planning session.

## Important Guidelines
- Each step should have a clear description of what it does and why.
- Each step should have a concrete shell command that can be run in an LXD container as root.
- Always include an "apt-get update" step before installing packages.
- Use "DEBIAN_FRONTEND=noninteractive apt-get install -y ..." for unattended installs.
- If the bug involves a specific snap, include a step to install it.
- If the bug cannot be reproduced (requires specific hardware, closed-source components, etc.), still provide a best-effort plan and note the limitations in expected_result.
- List all attachments you reviewed in the attachments_reviewed field.

`)

	writeCodenameTable(&b)
	writeSnapdKnowledge(&b)
	writeBugReport(&b, bug)

	// Tell the LLM how to access attachments.
	if len(bug.Attachments) > 0 {
		b.WriteString("\nUse the read_file tool to inspect any of the attachments listed above. Pass the filename (e.g., 'journal.log') as the path argument.\n")
	}

	return b.String()
}

// BuildPlanningUserMessage constructs the initial user message for the
// planning phase.
func BuildPlanningUserMessage(bug *Bug) string {
	return fmt.Sprintf(
		"Analyze Launchpad bug #%d: %s\n\n"+
			"Review the bug report and any attachments, determine the correct Ubuntu version, "+
			"and produce a step-by-step reproduction plan. Call report_plan when ready.",
		bug.ID, bug.Title,
	)
}

// --- Execution prompt ---

// BuildExecutionPrompt constructs the system prompt for the execution
// phase. The execution LLM follows the plan in an LXD container.
func BuildExecutionPrompt(plan *ReproPlan, containerName string) string {
	var b strings.Builder

	b.WriteString(`You are an expert Ubuntu/snapd bug reproduction agent. Your goal is to execute a reproduction plan inside an LXD container and determine whether the bug can be reproduced.

## Environment
- You are operating inside an LXD container named "` + containerName + `".
- The container runs Ubuntu ` + plan.UbuntuVersion + ` and has network access.
- You execute commands via the run_command tool. All commands run as root.

## Instructions
1. Follow the reproduction plan below step by step.
2. Execute each step using the run_command tool.
3. Check the output after each step. If something fails or behaves unexpectedly, adapt your approach.
4. You may add additional diagnostic commands if needed (e.g., checking logs, verifying state).
5. Once you have determined whether the bug is reproduced, call report_result with your findings.

## Important Guidelines
- Keep commands focused and check outputs between steps.
- If a command hangs or takes too long, try a different approach.
- If you determine the bug cannot be reproduced, report that clearly with an explanation.
- Include a complete reproducer script in your report (whether or not the bug was reproduced).

`)

	writeSnapdKnowledge(&b)

	// Include the plan.
	b.WriteString("## Reproduction Plan\n\n")
	fmt.Fprintf(&b, "**Bug ID:** %d\n", plan.BugID)
	fmt.Fprintf(&b, "**Title:** %s\n", plan.Title)
	fmt.Fprintf(&b, "**Ubuntu Version:** %s\n", plan.UbuntuVersion)
	fmt.Fprintf(&b, "**Expected Result:** %s\n", plan.ExpectedResult)

	b.WriteString("\n### Steps\n\n")
	for i, step := range plan.Steps {
		fmt.Fprintf(&b, "**Step %d:** %s\n", i+1, step.Description)
		fmt.Fprintf(&b, "```bash\n%s\n```\n\n", step.Command)
	}

	return b.String()
}

// BuildExecutionUserMessage constructs the initial user message for the
// execution phase.
func BuildExecutionUserMessage(plan *ReproPlan) string {
	return fmt.Sprintf(
		"Execute the reproduction plan for bug #%d: %s\n\n"+
			"Follow the steps in the plan, running each command and checking the output. "+
			"Adapt if needed. Report your result using the report_result tool when done.",
		plan.BugID, plan.Title,
	)
}

// --- Legacy prompt (kept for backward compatibility) ---

// BuildSystemPrompt constructs a combined system prompt for a single-phase
// agent that does both planning and execution. Used by the reproduce command.
func BuildSystemPrompt(bug *Bug, containerName string) string {
	var b strings.Builder

	b.WriteString(`You are an expert Ubuntu/snapd bug reproduction agent. Your goal is to reproduce a bug from a Launchpad bug report inside an LXD container.

## Environment
- You are operating inside an LXD container named "` + containerName + `".
- The container runs Ubuntu and has network access.
- You execute commands via the run_command tool. All commands run as root.

## Instructions
1. Read the bug report carefully. Understand what the reporter observed.
2. Plan a reproduction strategy. Think step by step.
3. Set up the environment: install packages, configure services, etc.
4. Write and execute a reproduction script.
5. Observe the output and determine if the bug is reproduced.
6. Call report_result with your findings.

## Important Guidelines
- Always run "apt-get update" before installing packages.
- Use "DEBIAN_FRONTEND=noninteractive apt-get install -y ..." for unattended installs.
- If the bug involves a specific snap, install it with "snap install <name>".
- If the bug requires a specific snapd version, check "snap version" first.
- Keep commands focused and check outputs between steps rather than running huge scripts blindly.
- If a command hangs or takes too long, try a different approach.
- If you determine the bug cannot be reproduced (e.g., requires specific hardware, a race condition, or a fixed version), report that clearly.

`)

	writeSnapdKnowledge(&b)
	writeBugReport(&b, bug)

	if len(bug.Attachments) > 0 {
		b.WriteString("\nNote: Attachment files have been downloaded locally. Use run_command to read them if needed (e.g., cat the file path).\n")
	}

	return b.String()
}

// BuildUserMessage constructs the initial user message for the
// single-phase agent.
func BuildUserMessage(bug *Bug) string {
	return fmt.Sprintf(
		"Please reproduce Launchpad bug #%d: %s\n\nAnalyze the bug report above and attempt to reproduce it in the container. "+
			"Start by understanding the problem, then set up the environment and run commands to trigger the bug. "+
			"Report your result using the report_result tool when done.",
		bug.ID, bug.Title,
	)
}

// --- Plan serialization ---

// SavePlan writes a ReproPlan to a JSON file.
func SavePlan(plan *ReproPlan, path string) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling plan: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing plan: %w", err)
	}
	return nil
}

// LoadPlan reads a ReproPlan from a JSON file.
func LoadPlan(path string) (*ReproPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading plan: %w", err)
	}
	var plan ReproPlan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parsing plan: %w", err)
	}
	return &plan, nil
}
