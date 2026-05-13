# Snap Debug Timings

The `snap debug timings` command shows detailed timing breakdowns for snapd operations. This is essential for diagnosing slow snap installs, refreshes, and other operations by revealing exactly which internal tasks are taking the most time.

## Basic usage

```bash
# Show timings for the last operation of a given type
snap debug timings --last=auto-refresh
snap debug timings --last=install
snap debug timings --last=refresh
snap debug timings --last=remove
snap debug timings --last=connect
snap debug timings --last=seed

# Show timings for a specific change by ID
snap debug timings --change=<id>

# Ensure timings (write timing data to stdout in a structured way)
snap debug timings --ensure=<ensure-type>
```

## Finding the right change ID

```bash
# List recent changes to find the ID
snap changes

# Filter for specific operation types
snap changes | grep -i refresh
snap changes | grep -i install
snap changes | grep -i seed

# Then get timings for that change
snap debug timings --change=<id>
```

## Understanding the output

The output shows a tree of operations with timing information:

```
ID    Status  Doing      Undoing    Summary
40    Done    1.2s       -          Task summary here
 ├─   0.3s              read-info
 ├─   0.5s              download
 └─   0.4s              mount
```

Key columns:
- **Doing**: Time spent performing the operation
- **Undoing**: Time spent reverting (if the operation failed)
- **Summary**: Description of the task or sub-operation

## Common debugging scenarios

### Slow snap refresh
```bash
# Check what the last auto-refresh did
snap debug timings --last=auto-refresh

# If a specific refresh was slow, find its change ID
snap changes | grep -i refresh
snap debug timings --change=<id>
```

### Slow first boot / seeding
```bash
# Check seed timing breakdown
snap debug timings --last=seed

# This shows how long each snap took to install during seeding
```

### Slow snap install
```bash
# Find the install change
snap changes | grep -i install
snap debug timings --change=<id>

# Common slow phases:
# - download: network/store issues
# - mount: filesystem or snap size issues
# - setup-profiles: AppArmor profile compilation
# - connect: interface auto-connection
```

### Slow snap removal
```bash
snap changes | grep -i remove
snap debug timings --change=<id>
```

## Diagnosing specific slow phases

### Download phase is slow
- Check network connectivity: `snap debug connectivity`
- Check if the snap is large: `snap info <name>` shows download size
- Check CDN issues: `curl -s -o /dev/null -w '%{time_total}' https://api.snapcraft.io/`

### setup-profiles is slow
- AppArmor profile compilation can be slow on low-resource systems
- Check: `aa-status | wc -l` for total profile count
- Many snaps = many profiles = slower compilation

### connect phase is slow
- Auto-connection of many interfaces can add up
- Check: `snap connections <name>` after install to see how many connections were made

## Combining with other debug tools

```bash
# Get the change ID from timings output, then inspect tasks
snap debug timings --last=auto-refresh
snap change <id>

# Check logs during the slow period
snap debug timings --last=install
# Note the timestamps, then:
journalctl -u snapd --since "<start-time>" --until "<end-time>"

# Use the API for more detail
snap debug api /v2/changes/<id> | jq '.result.tasks[] | {kind: .kind, status: .status, doing_time: .progress}'
```

## Timing data availability

- Timing data is only available for recent operations (snapd keeps a limited history).
- If the change has been garbage-collected, timings won't be available.
- Use `snap changes` to verify the change still exists before querying timings.
- On Ubuntu Core, seeding timings are particularly useful for first-boot performance analysis.

## Ensure timings

The `--ensure` flag shows timings for snapd's internal "ensure" loop iterations:

```bash
# Show timings for specific ensure types
snap debug timings --ensure=auto-refresh
snap debug timings --ensure=seed
```

This is lower-level than `--change` and shows snapd's internal scheduling and decision-making timings.
