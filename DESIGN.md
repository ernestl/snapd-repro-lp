# snapd-repro-lp Design

A Go CLI tool that fetches Launchpad bug reports for snapd, uses an LLM to
analyze them, and automatically attempts to reproduce the bug in an LXD VM.

## Architecture

The tool uses a **single-phase approach**: a single agent session receives the
full bug context, analyzes the bug, and attempts reproduction inside an LXD VM.
The LLM can investigate the environment, load domain-specific skills, determine
the correct Ubuntu version (relaunching the VM if needed), and execute
reproduction commands — all in one conversation.

LXD VM lifecycle is **host-controlled** — the LLM never creates or destroys
instances directly. A VM is launched before the LLM runs, and cleaned up after.
The LLM can request a VM relaunch with a different Ubuntu version via the
`relaunch_vm` tool, but the host manages the actual lifecycle. This is a
deliberate security boundary.

```
reproduce
 |
 v
 Fetch bug
 Launch default VM (24.04)
 Analyze + reproduce w/ LLM (single session)
   - LLM can call relaunch_vm to switch Ubuntu version
   - LLM investigates, reproduces, adapts
 Write result.json + reproducer.sh
 Delete VM
```

### VM-first design

The host always launches an LXD **virtual machine** (not a container). VMs
provide full systemd, support all snaps, and allow nested LXD. If the bug
specifically requires a container, the LLM can launch one inside the VM using
nested LXD.

A default VM (Ubuntu 24.04) is launched before the agent runs so it can
investigate the environment (check package versions, inspect attached state
files, test hypotheses). The LLM can call `relaunch_vm` to switch to a
different Ubuntu version mid-conversation.

## Components

### Launchpad Fetcher (`launchpad.go`)

Fetches bug data from the Launchpad REST API:
- Bug metadata (title, description, tags)
- Messages with pagination
- Attachments with deduplication (filename collisions get `_1`, `_2` suffixes)

All data is saved to a `bug-<id>/` directory with a JSON summary in the root
and downloaded attachment files in a `bug-<id>/attachments/` subdirectory.

### LLM Client (`llm.go`)

Raw HTTP client for the OpenRouter API (OpenAI-compatible). No streaming --
simple request/response. Configurable model via `--model` flag, defaults to
`deepseek/deepseek-v4-pro`. Includes a 5-minute HTTP timeout.

### Agent Loop (`agent.go`)

A generic agent loop that is not tied to any specific tool or phase:

1. Send system prompt + user message to the LLM.
2. If the LLM returns tool calls, execute them via the `ToolExecutor`.
3. If any tool sets `StopAgent: true`, stop and return `AgentResult{StoppedByTool: toolName}`.
4. If the LLM responds with text (no tool call), stop and return `AgentResult{LastMessage: text}`.
5. Repeat up to `--max-iter` iterations (default 60).

If the iteration budget is exhausted, the agent returns a soft result
(`MaxIterationsReached: true`) with a summary of recent tool activity rather
than a hard error. This ensures `result.json` is always produced.

The caller creates the appropriate tools, wires them into a `ToolExecutor`, and
inspects the specific tool for structured output after the agent returns (e.g.,
`reportResult.Result`).

**Agent output** uses a consistent colour scheme:
- Cyan: all LLM-initiated actions (tool calls, stop messages, text responses)
- Yellow: errors
- Dim: verbose detail (tool request/output)
- Plain: status lines (`Waiting for LLM response...`)

Non-stop tools display a summary after execution (e.g.
`run_command: apt-get update`). Stop tools display a human-readable message
(e.g. `LLM reported result`). In verbose mode (`-v`), the full tool request JSON
and output are printed after each tool call.

### Tool System (`tools.go`)

Tools implement the `Tool` interface (`Name`, `Definition`, `Execute`) and are
registered with a `ToolExecutor` that dispatches by name.

`ToolResult` includes optional fields:
- `StopAgent` / `StopMessage`: stop the agent loop with a human-readable message
- `Summary`: concise description for the progress line (e.g. the command string
  for `run_command`, the filename for `read_file`)

**Tools:**
- `run_command` -- Execute a shell command inside the LXD VM. Backed by a shared
  `InstanceRef` so that when `relaunch_vm` swaps the VM, `run_command`
  automatically targets the new instance. Output larger than 50KB is truncated.
- `read_file` -- Read a file or list a directory within `bug-<id>/attachments/`.
  Sandboxed: path traversal outside the attachments directory is rejected.
  Directories are listed with their entries; files larger than 100KB are truncated.
- `relaunch_vm` -- Delete the current VM and launch a new one with a different
  Ubuntu version. Updates the shared `InstanceRef` so `run_command` targets the
  new VM. The host provides a `VMFactory` function that creates new `LXDManager`
  instances.
- `report_result` -- Submit the reproduction result (`ReproResult`: reproduced
  bool, explanation, script). Sets `StopAgent: true`.
- `describe_skill` / `load_skill` -- Browse and load domain-specific skill
  documents (e.g. snap testing patterns) that get injected into the conversation.
- `query_snapd_revisions` -- Query the snapd snap revision-to-version mapping
  by date range, architecture, revision number, or version string.

**Shared mutable instance reference (`InstanceRef`):**
- `RunCommandTool` and `RelaunchVMTool` share an `InstanceRef` pointer. When
  `relaunch_vm` swaps the VM, `run_command` automatically targets the new
  instance without needing to be reconfigured.

### LXD Manager (`lxd.go`)

Manages LXD instance lifecycle by shelling out to `lxc`:
- `NewLXDManager()` generates a unique instance name (`snapd-repro-<random>`).
- `Launch(version, instanceType)` runs `lxc launch ubuntu:<version> <name> [--vm]`.
- `LaunchCached(version, instanceType)` launches from a cached golden VM snapshot
  when available, falling back to `Launch()` if any cache step fails.
- `Exec(command)` runs `lxc exec <name> -- bash -c "<command>"`.
- `Delete()` runs `lxc delete --force <name>`.

The `InstanceManager` interface allows substituting a mock for testing.

### VM Snapshot Cache (`lxd.go`)

To avoid the full boot + cloud-init + snap-seeding wait on every run, the LXD
manager maintains "golden" base VMs with an LXD snapshot of the
fully-initialised state. Both initial VM launch and `relaunch_vm` use the cache
via `LaunchCached()`.

**Golden VMs** are named `snapd-repro-base-<version>` (e.g.
`snapd-repro-base-2404`). Each golden VM has a snapshot named `ready` that
captures the state after network, cloud-init, and snap seeding have completed.

**Cache miss (first use of a version):**

```
lxc launch ubuntu:24.04 snapd-repro-base-2404 --vm
  waitForNetwork
  cloud-init status --wait
  snap wait system seed.loaded
lxc stop snapd-repro-base-2404
lxc snapshot snapd-repro-base-2404 ready
lxc copy snapd-repro-base-2404/ready snapd-repro-<work>
lxc start snapd-repro-<work>
  waitForNetwork (fast — cloud-init already done)
```

**Cache hit (subsequent uses):**

```
lxc copy snapd-repro-base-2404/ready snapd-repro-<work>
lxc start snapd-repro-<work>
  waitForNetwork (fast)
```

**Fallback:** if any cache operation fails (existence check, golden creation,
copy, or start), `LaunchCached()` falls back to a normal `Launch()` so the tool
always works even if caching has issues.

### Prompts (`prompt.go`)

Builds the system and user prompts for the reproduce agent. The reproduce prompt
includes the full bug report (description, messages, attachment list), Ubuntu
codename-to-version mapping, VM instance name, skills index, snapd domain
knowledge, reproduction methodology, incremental approach guidance, and
troubleshooting/adaptation instructions. The LLM is expected to analyze the
bug, load relevant skills, determine the correct Ubuntu version (using
`relaunch_vm` if needed), investigate, and reproduce — all in one session.

`UbuntuCodenames` maps release codenames (noble, jammy, focal, etc.) to version
numbers so the LLM can determine the right Ubuntu version from bug tags.

### Prompt HTML Output (`htmloutput.go`)

Each agent run saves its full system prompt and user message as a self-contained
HTML file (`reproduce-prompt.html`) for debugging.
Always written regardless of `--verbose`.

### Skills System (`skills.go`)

An embedded library of domain-specific knowledge documents (e.g. snap testing
patterns, LXD usage). Skills are indexed in `skills.json` and stored as markdown
files under `skills/`. The agent exposes `describe_skill` (list available
skills) and `load_skill` (inject a skill's content into the conversation) tools
so the LLM can pull in relevant knowledge on demand.

## CLI Commands

```
snapd-repro-lp reproduce <bug-ref>      # Fetch + analyze + reproduce in one step
```

**Global flags:** `--model`, `--max-iter`, `--verbose`
**Reproduce flags:** `--output-dir`/`-o`, `--force`/`-f`

## Data Flow

```
Launchpad API
     |
     v
bug-<id>/
   +-- bug-<id>.json            (bug metadata + messages)
   +-- attachments/
   |   +-- <files>...           (downloaded attachments)
   +-- reproduce-prompt.html    (reproduce prompt for inspection)
   +-- reproduce-log.txt        (agent interaction log)
   +-- result.json              (reproduction result)
   +-- reproducer.sh            (extracted script)
```

## Dependencies

- Go 1.26+
- LXD (snap) -- `setup.sh` handles installation and initialization
- `github.com/spf13/cobra` -- CLI framework
- `OPENROUTER_API_KEY` environment variable

## Key Design Decisions

- **Single-phase agent** -- the `reproduce` command runs one agent session that
  both analyzes and reproduces the bug. The LLM maintains full context across
  investigation and reproduction in a single conversation.
- **No agent SDK** -- raw HTTP to OpenRouter. Keeps dependencies minimal and
  the tool call loop simple to debug.
- **Agent is tool-agnostic** -- `NewAgent` takes an `LLMClient` and
  `ToolExecutor`, not specific tool types. Any tool can stop the loop by setting
  `StopAgent: true`. The caller reads structured output from the tool directly.
- **Host-controlled VM lifecycle** -- the LLM never creates or deletes LXD
  instances directly. VMs are launched and cleaned up by the host code. The LLM
  can request a version change via `relaunch_vm`, but the host manages the
  actual lifecycle through the `VMFactory` pattern.
- **VM-first, always** -- all reproduction uses VMs (not containers). VMs
  support full systemd, all snaps, and nested LXD. If a container is needed,
  the LLM launches one inside the VM.
- **Shared mutable instance reference** -- `RunCommandTool` and
  `RelaunchVMTool` share an `InstanceRef` pointer so that when `relaunch_vm`
  swaps the VM, `run_command` automatically targets the new instance.
- **`read_file` is sandboxed to `attachments/`** -- the LLM can only
  read files within `bug-<id>/attachments/`. The bug JSON and runtime artifacts
  in the `bug-<id>/` root are not visible to the LLM. Path escape attempts are
  rejected.
- **LXD via CLI, not API** -- shelling out to `lxc` is simpler and avoids the
  LXD Go client dependency. The `InstanceManager` interface keeps it testable.
- **Ubuntu version from LLM** -- the LLM infers the Ubuntu version from
  bug tags/description using the codename mapping. A default VM (24.04) is used
  initially; the LLM can call `relaunch_vm` to switch to a different version.
- **VM snapshot cache** -- golden base VMs are maintained per Ubuntu version
  with an LXD snapshot of the fully-initialised state (network + cloud-init +
  snap seeding). `LaunchCached()` copies from the snapshot and starts the copy,
  avoiding the full initialisation wait on cache hits. On cache miss the golden
  VM is created on first use. All cache operations fall back to a normal launch
  on failure.
