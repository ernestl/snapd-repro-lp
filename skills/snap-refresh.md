# Snap Refresh Debugging

## Check current snap versions
```bash
snap list
snap list --all  # includes disabled revisions
snap version     # snapd and snap versions
```

## Check pending and recent changes
```bash
snap changes
snap change <id>          # detailed view of a specific change
snap tasks <id>           # alias for snap change
snap change <id> | head   # summary of a change
```

## Refresh operations
```bash
snap refresh              # refresh all snaps
snap refresh <name>       # refresh a specific snap
snap refresh --list       # list available updates without installing
snap refresh <name> --channel=<channel>  # switch channel and refresh
snap refresh snapd        # refresh snapd itself
```

## Channel tracking
```bash
snap info <name>          # shows tracking channel
snap switch <name> --channel=<channel>  # switch channel without refreshing
```

## Held refreshes
```bash
snap refresh --hold       # hold all refreshes indefinitely
snap refresh --hold=24h   # hold for a duration
snap refresh --unhold     # release held refreshes
snap refresh --hold <name>    # hold a specific snap
snap refresh --unhold <name>  # unhold a specific snap
```

## Gated refreshes
```bash
snap debug gate-auto-refresh  # list snaps gating auto-refresh
```

## Refresh scheduling and configuration
```bash
snap get system refresh.timer     # check refresh timer
snap set system refresh.timer=4:00-7:00,19:00-22:10  # set refresh window
snap get system refresh.hold      # check if refreshes are held
snap set system refresh.hold=forever  # hold indefinitely
snap refresh --time                # show next refresh time
```

## Diagnosing stuck refreshes
1. Run `snap changes` and look for changes in "Doing" state.
2. Run `snap change <id>` to see which task is stuck.
3. Check `snap tasks <id>` for error messages.
4. Check snapd logs: `journalctl -u snapd --since "1 hour ago"`.
5. If a snap is mid-refresh, check `snap list --all <name>` for multiple revisions.
6. Try `snap abort <id>` to abort a stuck change.

## Common issues
- **Auto-refresh conflicts**: Check `snap changes` for "auto-refresh" changes that conflict with manual operations.
- **Snap store connectivity**: `snap debug connectivity` to test store access.
- **Held by gating snap**: Check `snap debug gate-auto-refresh` for snaps blocking refresh.
- **Metered connection**: `snap get system refresh.metered=hold` may prevent refreshes on metered connections.
