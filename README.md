# snapd-repro-lp

Automatically reproduce snapd bugs from Launchpad using LLM-driven analysis and LXD containers.

Given a Launchpad bug number, the tool fetches the bug report, uses an LLM to
plan a reproduction strategy, then executes it inside a disposable LXD
container.

## Development

### Workshop (recommended)

The project includes a [Workshop](https://github.com/canonical/workshop) definition
that provides a reproducible development environment with Go and all required
tools pre-installed:

```bash
# Launch the workshop container
workshop launch

# Build, test, and lint
workshop run -- build
workshop run -- test
workshop run -- lint

# Or open an interactive shell
workshop shell
```

### Local setup

Alternatively, set up the host directly:

```bash
# Install Go and LXD (Ubuntu)
sudo ./setup.sh

# Set your OpenRouter API key
export OPENROUTER_API_KEY="sk-or-..."
```

## Quick Start

### One-shot: plan and execute together

```bash
./snapd-repro-lp reproduce 1662786
```

### Two-step: plan first, then execute

```bash
# Analyze the bug and produce a plan
./snapd-repro-lp plan 1662786

# Review the plan
cat bug-1662786/plan.json

# Execute the plan in an LXD container
./snapd-repro-lp exec bug-1662786/plan.json
```

### Example output

```
$ ./snapd-repro-lp plan 1662786
Bug #1662786: snap list/find output is hard to read in a standard 80 column terminal
URL: https://bugs.launchpad.net/snapd/+bug/1662786
Tags: [snapd-snap]
Messages: 5
Attachments: 0

Planning reproduction (model: anthropic/claude-sonnet-4)...
[1/20] Waiting for LLM response...
[1/20] Tool: report_plan
[1/20] Agent stopped by report_plan

=== Reproduction Plan ===
Ubuntu version: 24.04
Steps: 10
  1. Check snap version
     $ snap version
  ...
Plan saved to bug-1662786/plan.json

Run the plan with:
  snapd-repro-lp exec bug-1662786/plan.json
```

## Options

```
--model string    LLM model via OpenRouter (default "anthropic/claude-sonnet-4")
--max-iter int    Maximum agent iterations (default 20)
-v, --verbose     Show detailed LLM debug output
```

See `DESIGN.md` for architecture details.
