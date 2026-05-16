package main

import (
	"fmt"
	"path/filepath"
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
				fmt.Fprintf(b, " — file: %s", filepath.Base(att.FilePath))
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

// writeSkillIndex appends a compact listing of available skills.
// If skills is nil, this is a no-op.
func writeSkillIndex(b *strings.Builder, skills *SkillIndex) {
	if skills == nil {
		return
	}
	b.WriteString("## Available Skills (IMPORTANT: review these before investigating)\n\n")
	b.WriteString("Skills contain expert debugging commands and workflows for specific snap/snapd problem domains.\n")
	b.WriteString("Use describe_skill to check relevance, then load_skill on the most relevant ones before proceeding.\n\n")
	b.WriteString(skills.List())
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
- To simulate a narrow terminal (e.g., for output-formatting bugs), use COLUMNS=80 before the command. Do NOT use stty cols — it requires a TTY that lxc exec does not provide.
- Reproduction environments use LXD virtual machines. VMs support full systemd, all snaps, and nested LXD. If a container is needed, launch one inside the VM using nested LXD.

`)
}

// --- Single-phase reproduce prompt ---

// BuildReproducePrompt constructs the system prompt for the single-phase
// reproduce command. The LLM receives the full bug context and is
// expected to analyze, investigate, and reproduce the bug in one session.
func BuildReproducePrompt(bug *Bug, instanceName string, skills *SkillIndex) string {
	var b strings.Builder

	b.WriteString(`You are an expert Ubuntu/snapd bug reproduction agent. Your goal is to analyze a Launchpad bug report and reproduce the ORIGINAL BUG inside an LXD VM.

## Environment
- You have access to an LXD VM named "` + instanceName + `" running Ubuntu ` + defaultUbuntuVersion + `.
- This is a virtual machine, which supports full systemd, all snaps, and nested LXD. You can install and use the lxd snap inside this VM if you need to launch nested containers.
- You execute commands via the run_command tool. All commands run as root.
- The VM has network access.

## Critical: Reproduce the Bug, Not the Fix
- Your goal is to trigger the BROKEN behavior described in the bug report.
- Do NOT install fixed versions of snapd or any package from -proposed or SRU channels.
- Use the stock/release version of the software that exhibits the bug.
- If the bug report mentions a version where the bug was introduced, target that version.
- If the bug report mentions a version where the bug was fixed, explicitly AVOID that version.
- "Reproduced" means you observed the BROKEN behavior described in the bug report. If the software works correctly, that means the bug was NOT reproduced.

## Instructions
1. Read the bug report carefully. Understand what the reporter observed and what conditions trigger the bug.
2. **Verify the environment.** Before anything else, check the VM matches what the bug requires:
   - Ubuntu version: run "lsb_release -a" and compare with the bug's tags/description. If a different version is needed, call relaunch_vm immediately.
   - Kernel version: run "uname -r" and compare with any kernel version mentioned in the bug.
   - snapd version: run "snap version" to see both the snapd deb and re-exec versions. Note that the running snapd may be re-executing from /snap/snapd/<rev>/usr/lib/snapd/snapd; "snap version" shows this. Install the correct snapd snap revision/channel if the bug requires a specific version.
   - AppArmor: run "aa-status --version" and "cat /sys/kernel/security/apparmor/features/domain/version" to check the AppArmor parser and kernel feature versions.
   - Any other component mentioned in the bug (e.g., specific kernel module, LXD version, specific snap version).
   If the bug mechanism is well understood and it is clear that a specific version is NOT required for reproduction, you may skip installing that exact version — we want the simplest reproducer possible. But you must explicitly state which checks you are skipping and why.
3. BEFORE investigating, review the available skills below. Use describe_skill to check which are relevant to the bug's domain (e.g., AppArmor, services, refresh), then load_skill on the most relevant ones. Skills contain expert commands and workflows that will make your investigation significantly more efficient — skipping this step often leads to wasted iterations.
4. If there are attachments (log files, configs, state.json, etc.), use the read_file tool to inspect them. For complex state files, you can also copy them into the VM and use run_command to interrogate them (e.g., "snap debug state state.json").
5. Determine which Ubuntu version to use based on the bug tags, description, or comments. Use the codename mapping table below. If the required version differs from the current VM (` + defaultUbuntuVersion + `), call relaunch_vm to get a VM with the correct Ubuntu version.
6. Investigate the bug using run_command: check package versions, explore snap state, test commands, and verify assumptions about the environment.
7. Set up the environment: install packages, configure services, install snaps as needed.
8. Execute reproduction steps: run commands to trigger the bug. Check output after each step.
9. Once you have genuinely attempted reproduction (including workarounds for any obstacles), call report_result with your findings. Do NOT ask for confirmation — call the tool directly.

## Tools Available
- **run_command**: Execute a shell command inside the LXD VM. Use this to install packages, run scripts, inspect files, and reproduce bugs.
- **read_file**: Read an attachment file from the bug directory. Use this to inspect log files, config files, or any other attachments.
- **describe_skill**: Get a short description of a debugging skill to check its relevance before loading.
- **load_skill**: Load the full content of a debugging skill with detailed commands and workflows.
- **query_snapd_revisions**: Look up the snapd snap revision-to-version mapping by date, architecture, revision number, or version string.
- **relaunch_vm**: Relaunch the VM with a different Ubuntu version. Use this when the bug requires a specific Ubuntu version that differs from the current one. All previous VM state is lost.
- **report_result**: Report whether the bug was reproduced. This ends the session.

## Important Guidelines
- Always run "apt-get update" before installing packages.
- Use "DEBIAN_FRONTEND=noninteractive apt-get install -y ..." for unattended installs.
- If the bug involves a specific snap, install it with "snap install <name>".
- If the bug requires a specific snapd version, check "snap version" first.
- Do NOT upgrade snapd or other packages to versions that contain the fix for this bug.
- Keep commands focused and check outputs between steps rather than running huge scripts blindly.
- If a command hangs or takes too long, try a different approach.
- Include a complete reproducer script in your report (whether or not the bug was reproduced).
- Commands run via lxc exec without a pseudo-TTY. Avoid commands that require a TTY (e.g., stty, dialog, interactive prompts). To simulate terminal width, set COLUMNS (e.g., COLUMNS=80 snap list).
- Some snaps cannot run inside unprivileged LXD containers (e.g., lxd, multipass). The VM supports all snaps. If the bug is specifically about behavior inside a container, use the VM to install and configure LXD, then launch a nested container within it.
- NEVER ask the user for permission or confirmation. Always call report_result directly when you have a determination.

## Reproduction Methodology

Use this structured approach when analyzing the bug and designing your reproduction, especially for non-trivial bugs (race conditions, intermittent failures, timing-dependent behavior):

1. **Identify the marker.** Find a specific observable indicator of the bug: an error message, log line, exit code, or behavioral symptom described in the bug report. This is your success criterion — reproduction is confirmed when this marker is observed.
2. **Check out the correct source version.** Clone snapd and check out the tag matching the installed snapd version. Snapd releases are tagged with the version number (e.g. "2.63", "2.64.1"). The deb version has "+ubuntu..." appended but the tag does not. Run "snap version" to see the running version, then "git tag -l '<version>*'" to find the exact tag. This is CRITICAL — error messages, code paths, and race windows may differ between versions, so tracing the wrong version can lead you to code that does not exist in the running binary.
3. **Trace the marker to source code.** Search for the static part of the error message in the correct version. Identify the file, function, and line. Use the snapd-code-tracing skill — it covers how to distinguish static from dynamic parts of error messages and navigate the codebase. This step is MANDATORY — do not skip it.
4. **Understand the execution path.** Read the code around the marker and work backward: what conditions cause execution to reach that point? What calls this function? What state must exist? Form a concrete hypothesis about the triggering scenario based on what you read in the code, not just the bug description.
5. **Identify the triggering conditions.** Determine which combination of events, state, or timing leads to the marker. For race conditions, identify the specific concurrent operations that must overlap: which code path is the "victim" and which is the "interrupter"? What is the precise window of vulnerability?
6. **Instrument for observability.** Add logging to verify your hypothesis and measure timing. Build snapd from source with logger.Noticef markers at key points. For race conditions, place TWO markers:
   - **Problem marker:** at the code line where the bug manifests (the error path).
   - **Trigger marker:** at the code line where the suspected concurrent operation occurs.
   Then compare timestamps in the journal output. If both markers fire but are far apart, adjust your trigger to narrow the gap. If only one fires, your hypothesis about which code path is involved may be wrong. See the snapd-code-tracing skill for how to build and run a modified snapd.
7. **Trigger the conditions.** Based on your understanding of the code path, construct a targeted trigger. Your trigger should be derived from the code analysis in steps 2-5, not from generic parallel loops. Iterate based on instrumentation output — use the timestamp gap between your markers to guide adjustments.

**MANDATORY: Steps 1-5 must be completed before attempting step 7.** You must demonstrate that you understand the code path and triggering conditions from the source code before trying to trigger the bug. Jumping straight from the bug description to brute-force trigger loops is not acceptable.

### Anti-pattern: blind timing loops

Do NOT write bash loops that randomly restart services, reload profiles, or run parallel operations hoping to hit a timing window. These almost never work because:
- The race window is typically microseconds, and random timing has near-zero probability of hitting it.
- Without understanding the code path, you cannot know which operations actually race.
- Brute-force loops waste iteration budget without producing insight.

Instead: trace the error to source code (at the correct version), understand what concurrent operations create the race, instrument with logger.Noticef markers at both the problem site and the trigger site, then construct a targeted trigger that forces the operations to overlap. Use the timestamp gap between your markers to iterate.

## Incremental Approach

Minimize time-to-answer by starting with the simplest possible reproduction and only adding complexity when the simpler attempt proves insufficient:

- **Start with code analysis, not commands.** For any non-trivial bug (race conditions, intermittent failures, unclear error paths), your first action should be cloning the relevant source code and tracing the error marker. This is almost always faster than trial-and-error.
- **Start minimal when executing.** If the bug involves a snap service, try with one snap before installing three. If it involves a config change, try the single relevant setting before replicating an entire production environment.
- **Escalate systematically.** When a minimal attempt does not trigger the bug, identify specifically what is missing based on your code analysis and add only that. Do not jump from "simple attempt didn't work" to "rebuild snapd from source with custom instrumentation" — there are intermediate steps (adjusting timing, changing service ordering, adding concurrency).
- **Avoid premature assumptions about difficulty.** Do not assume a race condition requires thousands of iterations or complex tooling to trigger. Many race conditions reproduce within a few attempts under the right conditions — if you understand the code path and target the right window.

## Troubleshooting and Adaptation
- If a step fails (e.g., a package or snap is not found, a command errors out), you MUST try to work around the problem rather than immediately giving up.
- **Do NOT report failure after a single error.** Exhaust alternative approaches first:
  - If a snap is not found, search for similar snaps ("snap find <keyword>"), use a different snap that has the same relevant feature (e.g., any snap with services), or build/sideload a minimal test snap.
  - If a package is missing, check alternative repositories, try different package names, or find another way to achieve the same setup.
  - If a command fails, read error messages carefully, check logs (journalctl, /var/log/), and investigate the root cause before deciding it blocks reproduction.
- Think about WHAT each step is trying to achieve, not just the literal command. If the specific tool or package is unavailable, find another way to achieve the same goal.
- Only report "not reproduced" when you have genuinely attempted the reproduction through alternative means and either confirmed the bug does not manifest OR exhausted all reasonable approaches. A missing package or snap is NOT sufficient reason to report failure — it means you need to adapt.

`)

	writeCodenameTable(&b)
	writeSkillIndex(&b, skills)
	writeSnapdKnowledge(&b)
	writeBugReport(&b, bug)

	// Tell the LLM how to access attachments.
	if len(bug.Attachments) > 0 {
		b.WriteString("\nUse the read_file tool to inspect any of the attachments listed above. Pass the filename (e.g., 'journal.log') as the path argument.\n")
	} else {
		b.WriteString("\nThere are no attachments to review in this bug report.\n")
	}

	return b.String()
}

// BuildReproduceUserMessage constructs the initial user message for the
// single-phase reproduce command.
func BuildReproduceUserMessage(bug *Bug) string {
	msg := fmt.Sprintf(
		"Reproduce Launchpad bug #%d: %s\n\n"+
			"Start by using describe_skill to review which skills are relevant, then load the most applicable ones. "+
			"Analyze the bug report, determine the correct Ubuntu version (use relaunch_vm if a different version is needed), "+
			"investigate the environment, and attempt to reproduce the bug. "+
			"Report your result using the report_result tool when done.",
		bug.ID, bug.Title,
	)
	if len(bug.Attachments) > 0 {
		msg += "\n\nUse read_file to inspect the bug's attachments."
	}
	return msg
}
