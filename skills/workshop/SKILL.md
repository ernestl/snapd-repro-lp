---
name: workshop
description: Manage Workshop development environments. Use when the user needs to launch, manage, or interact with Workshop containers for reproducible dev setups, run commands inside Workshop environments, debug Workshop issues, or understand workshop.yaml definitions.
---

# Workshop

Manage ephemeral development environments defined in `workshop.yaml`. Workshop provides reproducible, sandboxed dev environments using LXD containers and SDKs.

## Core Concepts

- **Workshop**: A sandboxed LXD container defined by `workshop.yaml`
- **SDKs**: Modular components (Go, Node.js, tools, etc.) installed into workshops
- **Actions**: Named shell scripts defined in `workshop.yaml` and invoked via `workshop run`
- **Interfaces**: Connection mechanisms (mount, tunnel, ssh-agent, etc.) between SDKs and the host

## Workshop Definition (`workshop.yaml`)

```yaml
name: my-project          # Required: workshop name (lowercase, digits, hyphens)
base: ubuntu@24.04        # Required: base image (ubuntu@20.04, 22.04, 24.04, 26.04)
sdks:                     # List of SDKs to install
  - name: go
    channel: latest/stable  # Snap-like channel format: <TRACK>/<RISK>/<BRANCH>
  - name: node
    channel: 22/stable
connections:               # Optional: plug-to-slot connections
  - plug: go:mod-cache
    slot: system:mount
actions:                   # Optional: named shell scripts
  build: |
    GOTOOLCHAIN=auto go build -o myapp .
  test: |
    GOTOOLCHAIN=auto go test ./...
  lint: |
    golangci-lint run
```

### Multiple Workshops

For multiple workshops, use `.workshop/` directory:
```
.workshop/
  dev.yaml      # name: dev
  test.yaml     # name: test
```

### SDK Types

| Type | Name Format | Description |
|------|-------------|-------------|
| Store SDK | `go`, `node` | From SDK Store, requires `channel` |
| In-project SDK | `project-<name>` | Defined in `.workshop/<name>/sdk.yaml` |
| Try SDK | `try-<name>` | Local SDK from `sdkcraft try` |
| System SDK | `system` | Built-in, provides host resources (mount, gpu, camera, ssh-agent, desktop) |

### Interfaces

- **mount**: Filesystem access between host/workshop and between SDKs
- **tunnel**: Network socket forwarding (TCP/UDP/Unix)
- **ssh-agent**: Forward SSH agent into workshop
- **gpu**: GPU device access
- **camera**: Camera device access
- **desktop**: Desktop/X11 access

## CLI Commands

### Lifecycle

```bash
# Create and start a workshop from definition
workshop launch [WORKSHOP]

# Start a stopped workshop
workshop start [WORKSHOP]

# Stop a running workshop (preserves state)
workshop stop [WORKSHOP]

# Update workshop to match definition changes
workshop refresh [WORKSHOP]

# Delete workshop (can rebuild with launch)
workshop remove [WORKSHOP]
```

### Running Commands

```bash
# Run a named action from workshop.yaml
workshop run [WORKSHOP] -- <ACTION> [ARGS...]

# Run an ad-hoc command
workshop exec [WORKSHOP] -- <COMMAND>...

# Open interactive shell
workshop shell [WORKSHOP]
```

#### Examples

```bash
# Run build action
workshop run -- build

# Run tests with forwarded arguments
workshop run -- test -run TestFoo ./pkg/...

# Run with environment variables
workshop run --env GO111MODULE=off -w /project -- build

# Execute one-off command
workshop exec -- go version

# Run as root
workshop exec --uid 0 -- id

# Interactive shell
workshop shell
```

### Information

```bash
# List all workshops in project
workshop list

# List all workshops system-wide
workshop list --global

# Show workshop details (YAML output)
workshop info [WORKSHOP]

# List available actions
workshop actions [WORKSHOP]

# Show recent changes
workshop changes

# Show tasks for a change
workshop tasks [CHANGE_ID]
```

### Connections

```bash
# Connect a plug to a slot
workshop connect <WORKSHOP>/<SDK>:<PLUG> [<WORKSHOP>/<SDK>][:<SLOT>]

# Disconnect
workshop disconnect <WORKSHOP>/<SDK>:<PLUG> [<WORKSHOP>/<SDK>][:<SLOT>]

# List connections
workshop connections [WORKSHOP]
```

### Other

```bash
# Remount with different host source
workshop remount [WORKSHOP]

# Show warnings
workshop warnings [WORKSHOP]

# Acknowledge warnings
workshop okay [WORKSHOP]

# Sketch SDK for prototyping
workshop sketch-sdk [WORKSHOP]
```

## Global Flags

```
-h, --help         Show help
-p, --project      Project directory path
-v, --version      Show Workshop version
```

## Common Flags

```
--no-wait          Return change ID without waiting
--verbose          Show stdout/stderr from hooks
--wait-on-error    Pause on error for debugging
--abort            Abort paused operation
--continue         Resume paused operation
--timeout          Command timeout (ns, us, ms, s, m, h)
--env KEY=VALUE    Set environment variable
--cwd DIR          Set working directory
--uid ID           Run as specific user
--gid ID           Run as specific group
--interactive      Force interactive mode
--non-interactive  Force non-interactive mode
```

## Workshop Statuses

| Status | Description |
|--------|-------------|
| Off | Definition exists but workshop not launched |
| Pending | Operation in progress |
| Ready | Running and available |
| Waiting | Running but awaiting action |
| Stopped | Launched but not running |

## Common Workflows

### Initial Setup

```bash
# 1. Create workshop.yaml in project root
# 2. Launch the workshop
workshop launch

# 3. Verify it's running
workshop list

# 4. Run your build
workshop run -- build
```

### Iterative Development

```bash
# Edit code on host, build inside workshop
workshop run -- build

# Run specific tests
workshop run -- test -run TestSomething ./...

# Open shell for debugging
workshop shell

# If definition changes, refresh
workshop refresh
```

### With Git Worktrees (AI Agents)

```bash
# Launch shared workshop
workshop launch

# Create worktree for agent
git worktree add agent-1

# Run agent in worktree
workshop run -w /project/agent-1 -- build
```

## Troubleshooting

```bash
# Check workshop status
workshop info

# View recent changes and errors
workshop changes
workshop tasks <CHANGE_ID>

# View warnings
workshop warnings

# If launch fails, try with wait-on-error
workshop launch --wait-on-error
# Fix the issue, then:
workshop launch --continue
# Or abort:
workshop launch --abort
```

## Shell Completion

```bash
# Bash
source <(workshop completion bash)

# Zsh
source <(workshop completion zsh)

# Fish
workshop completion fish | source
```

## References

- GitHub: https://github.com/canonical/workshop
- Docs: https://canonical-workshop.readthedocs-hosted.com/stable/
- SDK Crafting: https://canonical-workshop.readthedocs-hosted.com/stable/tutorial/part-4-craft-sdks/
