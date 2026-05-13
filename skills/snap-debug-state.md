# Snapd State and Debug Commands

## snap debug state
Inspect the internal snapd state file directly.

```bash
# Overview of all changes
snap debug state /var/lib/snapd/state.json

# Show details of a specific change
snap debug state /var/lib/snapd/state.json --change=<id>

# Filter by change status
snap debug state /var/lib/snapd/state.json --changes=Ready
snap debug state /var/lib/snapd/state.json --changes=Doing
snap debug state /var/lib/snapd/state.json --changes=Error

# Show a specific task
snap debug state /var/lib/snapd/state.json --task=<task-id>

# Check if state is consistent
snap debug state /var/lib/snapd/state.json --is-seeded
```

## snap changes and tasks
```bash
snap changes                   # high-level change list
snap change <id>               # tasks within a change
snap tasks <id>                # alias for snap change
snap abort <id>                # abort a running change
```

## Change states
- **Do / Doing**: change is actively running.
- **Done**: change completed successfully.
- **Error**: change failed (check task logs).
- **Undone / Undoing**: change is being reverted.
- **Hold**: change is paused (e.g., waiting for restart).
- **Wait**: change is waiting for an external event.

## Seed debugging
On first boot (or after a reset), snapd "seeds" the system by installing snaps.

```bash
# Check seed status
snap debug seeding

# Check seed assertions
ls /var/lib/snapd/seed/assertions/
cat /var/lib/snapd/seed/seed.yaml

# Check if seeding is complete
snap debug state /var/lib/snapd/state.json --is-seeded

# Check seeding change
snap changes | grep -i seed
```

## Connectivity debugging
```bash
snap debug connectivity         # test connectivity to snap store
snap debug connectivity --verbose
```

## Other debug commands
```bash
snap debug paths               # show snapd paths
snap debug sandbox-features    # show sandbox capabilities (apparmor, seccomp, etc.)
snap debug model               # show device model assertion
snap debug timings --last=auto-refresh  # show timing breakdown for last auto-refresh
snap debug timings --last=install       # timing for last install
snap debug timings --change=<id>        # timing for a specific change
snap debug boot-vars           # show boot variables (UC only)
snap debug confinement         # show confinement type (strict, classic, devmode)
```

## Inspecting snapd internal state directly
The state file is at `/var/lib/snapd/state.json`. It's a JSON file but should normally be accessed through `snap debug state` rather than read directly.

```bash
# If you must inspect directly (snapd should be stopped first):
systemctl stop snapd
python3 -m json.tool /var/lib/snapd/state.json | less
systemctl start snapd
```

## Common issues
- **Change stuck in "Doing"**: Check the specific task that's stuck with `snap change <id>`. May need `snap abort <id>`.
- **Seeding failures**: Check `snap debug seeding` and `snap changes`. Common on first boot with network issues.
- **State file corruption**: Very rare. Backup is at `/var/lib/snapd/state.json.bak`.
- **Change in "Error" state**: Run `snap change <id>` to see which task failed and why.

## Analyzing an attached state.json file

Bug reports often include a `state.json` file as an attachment. You can use `snap debug state` against this offline copy — it does not need to be the live system file.

```bash
# If the attachment is in the bug directory, copy it into the container:
# (or it may already be available at a known path)

# List all changes from the attached state file
snap debug state /path/to/state.json

# Filter changes by status
snap debug state /path/to/state.json --changes=Error
snap debug state /path/to/state.json --changes=Doing
snap debug state /path/to/state.json --changes=Done
snap debug state /path/to/state.json --changes=Undone

# Inspect a specific change and its tasks
snap debug state /path/to/state.json --change=<id>

# Inspect a specific task for detailed logs
snap debug state /path/to/state.json --task=<task-id>

# Check if the system was fully seeded
snap debug state /path/to/state.json --is-seeded

# Check what snaps were installed (dot notation to query snap state)
snap debug state /path/to/state.json --snap=<name>
```

### Workflow for analyzing an attached state.json
1. Read the attached `state.json` file from the bug directory using `read_file` or copy it into the container.
2. Run `snap debug state <path>` to get an overview of all changes.
3. Look for changes in `Error` or `Doing` state — these are likely related to the bug.
4. Run `snap debug state <path> --change=<id>` on suspicious changes to see task details.
5. Run `snap debug state <path> --task=<task-id>` on failed tasks for error logs.
6. Cross-reference change timestamps with any attached journal logs.

### Parsing state.json directly with jq
For queries that `snap debug state` doesn't support, parse the JSON directly:

```bash
# Pretty-print the full state (large output)
python3 -m json.tool /path/to/state.json | head -100

# List all snap names in the state
cat /path/to/state.json | jq -r '.data.snaps | keys[]'

# Get details about a specific snap
cat /path/to/state.json | jq '.data.snaps["<name>"]'

# List all changes with their status and summary
cat /path/to/state.json | jq '.data["changes"] | to_entries[] | {id: .key, status: .value.status, summary: .value.summary}'

# Find all failed changes
cat /path/to/state.json | jq '.data["changes"] | to_entries[] | select(.value.status == "Error") | {id: .key, summary: .value.summary}'

# Get task details for a specific change
cat /path/to/state.json | jq '.data["tasks"] | to_entries[] | select(.value["change-id"] == "<change-id>") | {id: .key, kind: .value.kind, status: .value.status, summary: .value.summary}'
```
