# snapd-repro-lp Design

A Go CLI tool that fetches Launchpad bug reports for snapd, uses an LLM to
analyze them, and automatically attempts to reproduce the bug in an LXD
container.

## Architecture

The tool uses a **two-phase approach**: a planning phase that analyzes the bug
and produces a structured plan, followed by an execution phase that carries out
the plan inside an LXD container. The phases are independent -- you can run
them separately or together.

```
plan              exec                reproduce
 |                 |                   |
 v                 v                   v
 Fetch bug         Load plan.json      Fetch bug
 Analyze w/ LLM    Launch LXD          Analyze w/ LLM  (plan)
 Write plan.json   Run plan w/ LLM     Write plan.json
                   Write result.json   Launch LXD      (exec)
                   Write reproducer.sh Run plan w/ LLM
                   Delete container    Write results
                                       Delete container
```

### Why two phases?

- **Inspectable plans**: The plan is saved as `plan.json` -- you can review,
  edit, or replay it before spending LXD resources.
- **Different models**: You can use a cheaper/faster model for planning and a
  more capable one for execution.
- **Debuggability**: If execution fails, you can re-run `exec` against the same
  plan without re-fetching and re-analyzing the bug.

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
`reportPlan.Plan` or `reportResult.Result`).

Progress output (`[1/20] Waiting for LLM response...`, `[1/20] Tool: run_command`)
is always printed to stderr. Additional debug detail is shown with `--verbose`.

### Tool System (`tools.go`)

Tools implement the `Tool` interface (`Name`, `Definition`, `Execute`) and are
registered with a `ToolExecutor` that dispatches by name.

**Planning phase tools:**
- `read_file` -- Read a file or list a directory within `bug-<id>/attachments/`.
  Sandboxed: path traversal outside the attachments directory is rejected.
  Directories are listed with their entries; files larger than 100KB are truncated.
- `report_plan` -- Submit a structured reproduction plan (`ReproPlan`). Sets
  `StopAgent: true`.
- `describe_skill` / `load_skill` -- Browse and load domain-specific skill
  documents (e.g. snap testing patterns) that get injected into the conversation.

**Execution phase tools:**
- `run_command` -- Execute a shell command in the LXD container via
  `ContainerManager.Exec()`. Output larger than 50KB is truncated.
- `report_result` -- Submit the reproduction result (`ReproResult`: reproduced
  bool, explanation, script). Sets `StopAgent: true`.
- `describe_skill` / `load_skill` -- Same skill tools as the planning phase.

### LXD Manager (`lxd.go`)

Manages LXD container lifecycle by shelling out to `lxc`:
- `NewLXDManager()` generates a unique container name (`snapd-repro-<random>`).
- `Launch(version)` runs `lxc launch ubuntu:<version> <name>`.
- `Exec(command)` runs `lxc exec <name> -- bash -c "<command>"`.
- `Delete()` runs `lxc delete --force <name>`.

The `ContainerManager` interface allows substituting a mock for testing.

### Prompts (`prompt.go`)

Builds system and user prompts for each phase:
- **Planning prompt**: Includes the full bug report (description, messages,
  attachment list), Ubuntu codename-to-version mapping, and instructions to
  produce a `ReproPlan` via `report_plan`.
- **Execution prompt**: Includes the plan steps, container name, Ubuntu version,
  and instructions to follow the plan adaptively and report via `report_result`.

`UbuntuCodenames` maps release codenames (noble, jammy, focal, etc.) to version
numbers so the planning LLM can determine the right Ubuntu version from bug tags.

`SavePlan`/`LoadPlan` handle JSON serialization of `ReproPlan`.

### Prompt HTML Output (`htmloutput.go`)

Each agent run saves its full system prompt and user message as a self-contained
HTML file (`planning-prompt.html`, `execution-prompt.html`) for debugging.
Always written regardless of `--verbose`.

### Skills System (`skills.go`)

An embedded library of domain-specific knowledge documents (e.g. snap testing
patterns, LXD usage). Skills are indexed in `skills.json` and stored as markdown
files under `skills/`. Both phases expose `describe_skill` (list available
skills) and `load_skill` (inject a skill's content into the conversation) tools
so the LLM can pull in relevant knowledge on demand.

## CLI Commands

```
snapd-repro-lp plan <bug-ref>           # Fetch + analyze, write plan.json
snapd-repro-lp exec <bug-ref>           # Load plan, run in LXD container
snapd-repro-lp reproduce <bug-ref>      # plan + exec in one step
```

**Global flags:** `--model`, `--max-iter`, `--verbose`
**Plan flags:** `--output-dir`/`-o`, `--force`/`-f`
**Exec flags:** `--output-dir`/`-o`, `--ubuntu` (override version from plan)
**Reproduce flags:** `--output-dir`/`-o`, `--force`/`-f`, `--ubuntu`

## Data Flow

```
Launchpad API
     |
     v
bug-<id>/
   +-- bug-<id>.json            (bug metadata + messages)
   +-- attachments/
   |   +-- <files>...           (downloaded attachments)
   +-- planning-prompt.html     (planning prompt for inspection)
   +-- plan.json                (from planning phase)
   +-- execution-prompt.html    (execution prompt for inspection)
   +-- result.json              (from execution phase)
   +-- reproducer.sh            (extracted script)
```

## Dependencies

- Go 1.26+
- LXD (snap) -- `setup.sh` handles installation and initialization
- `github.com/spf13/cobra` -- CLI framework
- `OPENROUTER_API_KEY` environment variable

## Key Design Decisions

- **No agent SDK** -- raw HTTP to OpenRouter. Keeps dependencies minimal and
  the tool call loop simple to debug.
- **Agent is tool-agnostic** -- `NewAgent` takes an `LLMClient` and
  `ToolExecutor`, not specific tool types. Any tool can stop the loop by setting
  `StopAgent: true`. The caller reads structured output from the tool directly.
- **`read_file` is sandboxed to `attachments/`** -- the planning LLM can only
  read files within `bug-<id>/attachments/`. The bug JSON and runtime artifacts
  in the `bug-<id>/` root are not visible to the LLM. Path escape attempts are
  rejected.
- **LXD via CLI, not API** -- shelling out to `lxc` is simpler and avoids the
  LXD Go client dependency. The `ContainerManager` interface keeps it testable.
- **Ubuntu version from LLM** -- the planning LLM infers the Ubuntu version from
  bug tags/description using the codename mapping. `--ubuntu` on `exec` is an
  optional override.
