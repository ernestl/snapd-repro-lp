# snapd-repro-lp

Automatically reproduce snapd bugs from Launchpad using LLM-driven analysis and LXD VMs.

Given a Launchpad bug number, the tool fetches the bug report, launches an LXD
VM, and uses an LLM to analyze the bug and attempt reproduction — all in a
single agent session. The LLM can investigate the environment, load
domain-specific debugging skills, switch Ubuntu versions, and execute
reproduction steps inside the VM.

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

```bash
./snapd-repro-lp reproduce 1662786
```

### Example output

```
$ ./snapd-repro-lp reproduce 1662786
Step 1/3: Fetching bug #1662786...
  Description: snap list/find output is hard to read in a standard 80 column terminal
  Link: https://bugs.launchpad.net/snapd/+bug/1662786
  Tags: [snapd-snap]
  Messages: 5, Attachments: 0
  Saved bug data to bug-1662786/bug-1662786.json

Step 2/3: Launching VM snapd-repro-a1b2c3 (ubuntu:24.04)...

Step 3/3: Reproducing bug (model: deepseek/deepseek-v4-pro)...
  Generated reproduce prompt: bug-1662786/reproduce-prompt.html
  [1/60] Waiting for LLM response...
  [1/60] describe_skill: snap-refresh
  [1/60] load_skill: journalctl
  [2/60] Waiting for LLM response...
  [2/60] run_command: snap version
  [3/60] Waiting for LLM response...
  [3/60] run_command: COLUMNS=80 snap list
  [4/60] Waiting for LLM response...
  [4/60] LLM reported result

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

**reproduce flags:**
```
-o, --output-dir string   Directory to write output (default: current directory)
-f, --force               Overwrite existing bug directory without prompting
```

See `DESIGN.md` for architecture details.
