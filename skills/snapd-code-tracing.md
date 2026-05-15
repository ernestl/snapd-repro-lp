# Snapd Code Tracing

Techniques for finding the snapd source code responsible for a bug, instrumenting
it for deeper debugging, and running a modified build.

## Getting the source code

```bash
git clone https://github.com/snapcore/snapd.git
cd snapd

# Check the installed snapd version to match the source
snap version

# Checkout the matching tag (e.g. 2.63)
git tag -l '2.63*'
git checkout 2.63
```

## Snapd code structure

| Directory | What lives there |
|-----------|------------------|
| `cmd/snap/` | CLI command handlers (`cmd_install.go`, `cmd_refresh.go`, `cmd_list.go`, ...) |
| `cmd/snapd/` | Entry point for the `snapd` daemon |
| `daemon/` | REST API endpoint handlers over `/run/snapd.socket` (`api_snaps.go`, `api_general.go`, ...) |
| `overlord/` | State engine orchestration (`overlord.go`) |
| `overlord/snapstate/` | Snap lifecycle: install, refresh, remove, revert |
| `overlord/ifacestate/` | Interface connection management, security profile setup |
| `overlord/devicestate/` | Device identity, model assertion, remodel, boot |
| `overlord/assertstate/` | Assertion database |
| `overlord/hookstate/` | Snap hook execution; `hookstate/ctlcmd/` implements `snapctl` |
| `snap/` | Snap metadata types (parsed from `snap.yaml`) |
| `interfaces/` | Interface framework; `interfaces/builtin/` has each interface definition |
| `store/` | Snap store HTTP client |
| `client/` | Go client library for the snapd REST API (used by `cmd/snap/`) |
| `logger/` | Logging infrastructure |
| `httputil/` | HTTP client utilities, logged transport, retry logic |
| `wrappers/` | Generates systemd units, desktop files, etc. for installed snaps |
| `sandbox/` | AppArmor, seccomp, cgroup sandbox backends |
| `sandbox/apparmor/` | AppArmor backend: profile compilation, loading/reloading, caching, and runtime management via `apparmor_parser` |
| `cmd/snap-confine/` | C binary for snap confinement setup: AppArmor profile transitions, mount namespace setup, cgroup management. Executed by `snap run` before the confined app starts |
| `cmd/libsnap-confine-private/` | Shared C library used by snap-confine: AppArmor support, mount utilities, cleanup functions |
| `cmd/snapd-apparmor/` | Helper binary invoked by `snapd.apparmor.service` at boot to load all snap AppArmor profiles |
| `data/systemd/` | Systemd unit templates for snapd services (`snapd.service`, `snapd.apparmor.service`, etc.) |

### How a CLI command flows through the code

```
snap install foo
  -> cmd/snap/cmd_install.go     (CLI argument parsing)
  -> client/snaps.go             (REST call to snapd socket)
  -> daemon/api_snaps.go         (HTTP handler, creates a state change)
  -> overlord/snapstate/         (task handlers: download, mount, setup, link, etc.)
```

## Searching for error messages

The most effective way to find relevant code is to search for error messages seen
in the bug report. The key technique is identifying which parts of the message
are **static** (hardcoded string literals) and which are **dynamic** (interpolated
from variables at runtime).

### Snapd error conventions

- Almost all errors use `fmt.Errorf("cannot <verb> ...: %w", err)`.
- The prefix `"cannot "` is lowercase by Go convention.
- Errors are wrapped with context as they propagate up the call stack.
- Dynamic values include: snap names, file paths, revision numbers, interface
  names, error messages from lower layers, timestamps, UUIDs.

### Dissecting an error message: examples

**Example 1**: `cannot install "firefox": snap not found`
- Static: `cannot install` and `snap not found`
- Dynamic: `"firefox"` (snap name, inserted via `%q`)
- Search: `grep -rn 'cannot install' cmd/ daemon/ overlord/`

**Example 2**: `cannot connect core:network to firefox:network: permission denied`
- Static: `cannot connect` and `permission denied`
- Dynamic: `core:network`, `firefox:network` (snap:interface names)
- Search: `grep -rn 'cannot connect' overlord/ifacestate/ interfaces/`

**Example 3**: `cannot refresh "snapd": snap "snapd" has running apps (snapd)`
- Static: `cannot refresh` and `has running apps`
- Dynamic: `"snapd"` (snap name), `(snapd)` (app list)
- Search: `grep -rn 'has running apps' overlord/snapstate/`

**Example 4**: `error: cannot communicate with server: Post "http://localhost/v2/snaps/firefox": dial unix /run/snapd.socket: connect: no such file or directory`
- This is a multi-layer wrapped error. Search from the outermost static part.
- Static: `cannot communicate with server`
- Search: `grep -rn 'cannot communicate with server' client/`

### Rules of thumb for static vs dynamic

| Likely static | Likely dynamic |
|---------------|----------------|
| `"cannot <verb>"` | Snap names, quoted strings |
| `"has running apps"` | File paths |
| `"not found"`, `"already exists"` | Revision numbers |
| `"permission denied"` | Interface names (`<snap>:<plug>`) |
| `"no state entry"` | Error messages from lower layers (after `:`) |
| `"conflict"`, `"held"` | Timestamps, UUIDs, change IDs |

### Search strategy

```bash
# Start broad: search the likely subsystem
grep -rn 'cannot refresh' overlord/snapstate/

# If too many hits, add more static context
grep -rn 'has running apps' overlord/snapstate/

# Search across the whole codebase if unsure where it originates
grep -rn 'cannot communicate with server' .

# Search for the error type if it's a specific error variable
grep -rn 'ErrSnapNotFound' store/ overlord/
```

## Navigating from a search hit

Once you find where an error is generated:

```bash
# Find the function name from context, then search for callers
grep -rn 'functionName' overlord/ daemon/

# Find where a task kind is registered (e.g. "install-snap")
grep -rn '"install-snap"' overlord/

# Find a REST API endpoint handler
grep -rn '"POST /v2/snaps"' daemon/
# or search by the URL path segment
grep -rn '/v2/snaps' daemon/api*.go
```

### Entry points by bug symptom

| Symptom | Where to look |
|---------|--------------|
| `snap install` fails | `cmd/snap/cmd_install.go` -> `overlord/snapstate/snapstate.go` |
| `snap refresh` fails | `cmd/snap/cmd_snap_op.go` -> `overlord/snapstate/autorefresh.go` |
| Interface connection denied | `overlord/ifacestate/` -> `interfaces/builtin/<name>.go` |
| Hook fails | `overlord/hookstate/` -> `overlord/hookstate/ctlcmd/` |
| Seeding/first boot issue | `overlord/devicestate/firstboot.go` |
| Store communication error | `store/store.go` -> `httputil/` |
| Systemd service issue | `wrappers/services.go` |
| AppArmor/seccomp denial | `interfaces/builtin/<name>.go` -> `sandbox/apparmor/` |
| snap-confine / confinement error | `cmd/snap-confine/` -> `cmd/libsnap-confine-private/` |
| AppArmor profile loading/reload | `sandbox/apparmor/profile.go` -> `cmd/snapd-apparmor/` |
| Boot-time profile loading | `data/systemd/snapd.apparmor.service.in` -> `cmd/snapd-apparmor/` |

## Debug environment variables

### SNAPD_DEBUG

Enables all `logger.Debugf()` output. Messages are prefixed with `DEBUG:`.

```bash
# Run the system snapd with debug logging
sudo SNAPD_DEBUG=1 snapd

# Can also be enabled via kernel command line
# Add snapd.debug=1 to kernel cmdline (for early boot debugging)
```

### SNAPD_DEBUG_HTTP

Controls HTTP request/response logging for the snapd daemon's communication
with the snap store. Uses a bitfield:

| Value | Effect |
|-------|--------|
| 1 | Log HTTP requests |
| 2 | Log HTTP responses |
| 3 | Log requests + responses (headers only) |
| 5 | Log requests with bodies |
| 7 | Log requests + responses + bodies (most verbose) |

```bash
sudo SNAPD_DEBUG=1 SNAPD_DEBUG_HTTP=7 snapd
```

### SNAP_CLIENT_DEBUG_HTTP

Same bitfield as `SNAPD_DEBUG_HTTP`, but for the `snap` CLI client's
communication with the local snapd daemon socket. Useful for debugging
REST API interactions.

```bash
SNAP_CLIENT_DEBUG_HTTP=3 snap install firefox
```

## Building and running a modified snapd

### Building from source

```bash
cd ~/snapd

# Install build dependencies
ln -sfn packaging/ubuntu-16.04 debian
sudo apt build-dep -y .

# Build just the snapd daemon
go build -o /tmp/snapd ./cmd/snapd

# Build the snap CLI
go build -o /tmp/snap ./cmd/snap
```

### Adding instrumentation

Add debug logging to the code to trace execution paths:

```go
// Use logger.Debugf for output visible with SNAPD_DEBUG=1
logger.Debugf(">>> entering doInstall: snap=%q, revision=%s", snapName, rev)

// Use logger.Noticef for output always visible in the journal
logger.Noticef(">>> checkpoint: snap=%q state=%v", snapName, st)
```

Rebuild after making changes: `go build -o /tmp/snapd ./cmd/snapd`

### Running the local build (foreground)

```bash
# Stop the system snapd
sudo systemctl stop snapd.service snapd.socket

# Run your build in the foreground (output goes to terminal)
sudo SNAPD_DEBUG=1 /tmp/snapd
```

### Running the local build via systemd (output goes to journal)

To run the local build as a systemd service so that output is captured in the
journal (visible via `journalctl -u snapd`):

```bash
# Create a systemd override
sudo systemctl edit snapd.service
```

Add the following content to the override file:

```ini
[Service]
ExecStart=
ExecStart=/tmp/snapd
Environment=SNAPD_DEBUG=1
Environment=SNAPD_DEBUG_HTTP=3
```

Note: the blank `ExecStart=` line is required to clear the original `ExecStart`
before setting the new one.

```bash
# Reload and restart with the local build
sudo systemctl daemon-reload
sudo systemctl restart snapd.service

# Watch the debug output in the journal
journalctl -u snapd -f

# When done, revert to the stock snapd
sudo systemctl revert snapd.service
sudo systemctl daemon-reload
sudo systemctl restart snapd.service
```

## Logger patterns to search for

When searching the snapd codebase for logging statements:

| Pattern | When it fires |
|---------|---------------|
| `logger.Noticef(` | Always logged (user-visible messages) |
| `logger.Debugf(` | Only with `SNAPD_DEBUG=1` |
| `logger.Panicf(` | Fatal errors (causes panic) |
| `fmt.Errorf(` | Error construction (most common) |
| `errors.New(` | Simple error construction |
| `log.Panicf(` | Standard library panic (rare in snapd) |

```bash
# Find all debug log statements in a subsystem
grep -rn 'logger.Debugf' overlord/snapstate/

# Find all notice-level messages
grep -rn 'logger.Noticef' overlord/snapstate/
```
