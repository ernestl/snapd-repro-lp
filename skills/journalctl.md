# Journalctl for Snapd Log Analysis

## Basic snapd log commands
```bash
journalctl -u snapd                    # all snapd logs
journalctl -u snapd -n 100            # last 100 lines
journalctl -u snapd -f                 # follow logs in real time
journalctl -u snapd --no-pager         # disable paging
```

## Time-based filtering
```bash
journalctl -u snapd --since "5 min ago"
journalctl -u snapd --since "1 hour ago"
journalctl -u snapd --since "2024-01-15"
journalctl -u snapd --since "2024-01-15 10:00:00" --until "2024-01-15 12:00:00"
journalctl -u snapd --since today
journalctl -u snapd --since yesterday
journalctl -u snapd -b              # since last boot
journalctl -u snapd -b -1           # previous boot
```

## Priority filtering
```bash
journalctl -u snapd -p err            # errors only
journalctl -u snapd -p warning        # warnings and above
journalctl -u snapd -p debug          # debug and above (very verbose)
```

## Snap service logs
```bash
# Logs for a specific snap service
journalctl -u snap.<name>.<service>.service
journalctl -u snap.<name>.<service>.service -n 50
journalctl -u snap.<name>.<service>.service --since "10 min ago"

# All snap-related units
journalctl -u 'snap.*' --since "1 hour ago"
```

## Kernel logs (AppArmor/seccomp denials)
```bash
journalctl -k                          # kernel messages
journalctl -k | grep DENIED           # AppArmor denials
journalctl -k | grep audit            # all audit messages
journalctl -k | grep seccomp          # seccomp denials
dmesg | grep DENIED                   # alternative for AppArmor
```

## Output formatting
```bash
journalctl -u snapd -o json           # JSON output
journalctl -u snapd -o json-pretty    # pretty-printed JSON
journalctl -u snapd -o short-precise  # timestamps with microseconds
journalctl -u snapd -o verbose        # all fields
journalctl -u snapd -o cat            # message text only (no metadata)
```

## Searching log content
```bash
journalctl -u snapd -g "error"        # grep for pattern (systemd 245+)
journalctl -u snapd | grep -i "error"  # traditional grep
journalctl -u snapd | grep -i "failed"
journalctl -u snapd | grep -i "cannot"  # snapd uses "cannot" for errors
```

## Disk usage and rotation
```bash
journalctl --disk-usage                # total journal disk usage
journalctl --vacuum-size=500M          # reduce to 500MB
journalctl --vacuum-time=7d            # keep only last 7 days
```

## Debug logging for snapd
To enable debug logging temporarily:
```bash
snap set system debug.snapd.log=true
# ... reproduce the issue ...
journalctl -u snapd --since "1 min ago"
snap set system debug.snapd.log=false
```

## Useful patterns for bug reproduction
1. **Before reproducing**: Note the current time or run `journalctl -u snapd -n 1` to get a timestamp.
2. **Reproduce the bug**.
3. **After reproducing**: `journalctl -u snapd --since "<timestamp>"` to get only relevant logs.

```bash
# Capture a timestamp, reproduce, then grab logs
BEFORE=$(date +"%Y-%m-%d %H:%M:%S")
# ... run reproduction commands ...
journalctl -u snapd --since "$BEFORE" --no-pager
```

## Common log patterns to look for
- `"cannot"` — snapd error messages typically start with "cannot".
- `"timeout"` — operation timeouts.
- `"retry"` — snapd retrying an operation.
- `"conflict"` — change conflicts.
- `"DENIED"` — AppArmor permission denials (in kernel log).
- `"auto-refresh"` — auto-refresh related messages.
