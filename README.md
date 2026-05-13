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

# Optionally set the model (defaults to deepseek/deepseek-v4-pro)
export OPENROUTER_MODEL="deepseek/deepseek-v4-pro"
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
./snapd-repro-lp exec 1662786
```

### Example output

```
$ ./snapd-repro-lp plan 1662786
Bug #1662786: snap list/find output is hard to read in a standard 80 column terminal
URL: https://bugs.launchpad.net/snapd/+bug/1662786
Tags: [snapd-snap]
Messages: 5
Attachments: 0
Saved bug data to bug-1662786/bug-1662786.json
Saved planning prompt to bug-1662786/planning-prompt.html

Planning reproduction (model: deepseek/deepseek-v4-pro)...
[1/60] Waiting for LLM response...
[1/60] Tool: report_plan
  args: {"ubuntu_version":"24.04","instance_type":"container",...}
  result: {"status":"ok"}
[1/60] Agent stopped by report_plan

=== Reproduction Plan ===
Ubuntu version: 24.04
Steps: 10
  1. Check snap version
     $ snap version
  ...
Saved plan to bug-1662786/plan.json

Run the plan with:
  snapd-repro-lp exec 1662786
```

## Options

```
--model string    LLM model via OpenRouter (default "deepseek/deepseek-v4-pro")
                  Can also be set via OPENROUTER_MODEL environment variable.
--max-iter int    Maximum agent iterations (default 60)
-v, --verbose     Show detailed LLM debug output
```

**plan flags:**
```
-o, --output-dir string   Directory to write output (default: current directory)
-f, --force               Overwrite existing bug directory without prompting
```

**exec flags:**
```
-o, --output-dir string   Directory containing bug output (default: current directory)
    --ubuntu string       Override the Ubuntu version from the plan (e.g. 22.04)
```

**reproduce flags:**
```
-o, --output-dir string   Directory to write output (default: current directory)
-f, --force               Overwrite existing bug directory without prompting
    --ubuntu string       Override the Ubuntu version from the plan (e.g. 22.04)
```

See `DESIGN.md` for architecture details.
