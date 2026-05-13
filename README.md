# snapd-repro-lp

Automatically reproduce snapd bugs from Launchpad using LLM-driven analysis and LXD VMs.

Given a Launchpad bug number, the tool fetches the bug report, launches an LXD
VM, uses an LLM to plan a reproduction strategy (with VM access for
investigation), then executes it inside the VM.

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

# Execute the plan in an LXD VM
./snapd-repro-lp exec 1662786
```

### Example output

```
$ ./snapd-repro-lp reproduce 1662786
Step 1/4: Fetching bug #1662786...
  Description: snap list/find output is hard to read in a standard 80 column terminal
  Link: https://bugs.launchpad.net/snapd/+bug/1662786
  Tags: [snapd-snap]
  Messages: 5, Attachments: 0
  Saved bug data to bug-1662786/bug-1662786.json

Step 2/4: Launching VM snapd-repro-a1b2c3 (ubuntu:24.04)...

Step 3/4: Planning reproduction (model: deepseek/deepseek-v4-pro)...
  Generated planning prompt: bug-1662786/planning-prompt.html
  [1/60] Waiting for LLM response...
  [1/60] run_command: snap version
  [2/60] Waiting for LLM response...
  [2/60] LLM reported plan
  Saved plan to bug-1662786/plan.json

Step 4/4: Executing plan (model: deepseek/deepseek-v4-pro)...
  Generated execution prompt: bug-1662786/execution-prompt.html
  [1/60] Waiting for LLM response...
  [1/60] run_command: apt-get update
  [1/60] run_command: COLUMNS=80 snap list
  [2/60] Waiting for LLM response...
  [2/60] LLM reported result

  Status: REPRODUCED
  Explanation: The snap list output wraps incorrectly at 80 columns...
  Saved reproducer script to bug-1662786/reproducer.sh
  Saved result to bug-1662786/result.json
  Cleaning up VM snapd-repro-a1b2c3...

Token usage: 5200 prompt + 1800 completion = 7000 total
```

## Options

```
--model string    LLM model via OpenRouter (default "deepseek/deepseek-v4-pro")
                  Can also be set via OPENROUTER_MODEL environment variable.
--max-iter int    Maximum agent iterations (default 60)
-v, --verbose     Show full tool request/output detail
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
