# Snap Debug API

The `snap debug api` command makes raw REST API calls to the snapd daemon socket (`/run/snapd.socket`). This is useful for querying internal snapd state, triggering operations, and debugging issues that aren't exposed through the standard `snap` CLI.

## Basic usage

```bash
# GET request (default)
snap debug api /v2/system-info

# GET with query parameters
snap debug api '/v2/snaps?select=all'

# POST request with JSON body
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "refresh"}' /v2/snaps/core
```

## Useful API endpoints

### System information
```bash
# General system and snapd info (version, managed, etc.)
snap debug api /v2/system-info

# Snap daemon configuration
snap debug api /v2/system-info | python3 -m json.tool
```

### Snaps
```bash
# List all installed snaps
snap debug api /v2/snaps

# Get details about a specific snap
snap debug api /v2/snaps/<name>

# List all snaps including disabled revisions
snap debug api '/v2/snaps?select=all'

# Refresh a specific snap
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "refresh"}' /v2/snaps/<name>

# Install a snap
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "install"}' /v2/snaps/<name>

# Revert a snap
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "revert"}' /v2/snaps/<name>
```

### Changes and tasks
```bash
# List all changes
snap debug api /v2/changes

# List changes by status
snap debug api '/v2/changes?select=all'
snap debug api '/v2/changes?select=ready'
snap debug api '/v2/changes?select=in-progress'

# Get details of a specific change (including tasks)
snap debug api /v2/changes/<id>

# Abort a change
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "abort"}' /v2/changes/<id>
```

### Connections and interfaces
```bash
# List all connections
snap debug api /v2/connections

# List interfaces
snap debug api /v2/interfaces

# Connect a plug to a slot
snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "connect", "plugs": [{"snap": "<snap>", "plug": "<plug>"}], "slots": [{"snap": "<snap>", "slot": "<slot>"}]}' \
  /v2/interfaces
```

### Assertions
```bash
# List assertion types
snap debug api /v2/assertions

# Get specific assertions
snap debug api /v2/assertions/model
snap debug api /v2/assertions/serial
snap debug api /v2/assertions/account
```

### Snap configuration
```bash
# Get snap configuration
snap debug api '/v2/snaps/<name>/conf'

# Get a specific config key
snap debug api '/v2/snaps/<name>/conf?keys=<key>'

# Set snap configuration
snap debug api -X PUT -H 'Content-Type: application/json' \
  -d '{"<key>": "<value>"}' /v2/snaps/<name>/conf
```

### Users and login
```bash
# List system users
snap debug api /v2/users

# Get current user info
snap debug api /v2/find?name=<snap-name>
```

### Snap store queries
```bash
# Search the store
snap debug api '/v2/find?q=<search-term>'

# Find by name
snap debug api '/v2/find?name=<exact-name>'

# Find by section
snap debug api '/v2/find?section=<section>'
```

### Validation sets
```bash
# List validation sets
snap debug api /v2/validation-sets

# Get a specific validation set
snap debug api /v2/validation-sets/<account-id>/<name>
```

## Parsing output

API responses are JSON. Use `python3 -m json.tool` or `jq` to format them:

```bash
# Pretty-print with python
snap debug api /v2/system-info | python3 -m json.tool

# Pretty-print with jq (if installed)
snap debug api /v2/snaps | jq .

# Extract specific fields with jq
snap debug api /v2/snaps | jq '.result[] | {name: .name, version: .version, revision: .revision}'

# Get just the status field
snap debug api /v2/changes/<id> | jq '.result.status'
```

## Async operations

POST requests that trigger changes return immediately with a change ID:

```bash
# Trigger an operation
RESPONSE=$(snap debug api -X POST -H 'Content-Type: application/json' \
  -d '{"action": "refresh"}' /v2/snaps/<name>)

# Extract the change ID
CHANGE_ID=$(echo "$RESPONSE" | python3 -c "import sys, json; print(json.load(sys.stdin)['change'])")

# Poll for completion
snap debug api /v2/changes/$CHANGE_ID
```

## Common debugging patterns

### Check why a snap operation failed
```bash
# Get the change details
snap debug api /v2/changes/<id> | python3 -m json.tool

# Look at individual task logs within the change
snap debug api /v2/changes/<id> | jq '.result.tasks[] | select(.status == "Error") | {kind: .kind, summary: .summary, log: .log}'
```

### Inspect snapd readiness
```bash
# Check if snapd is fully seeded and ready
snap debug api /v2/system-info | jq '.result | {ready: .ready, managed: .managed}'
```

### Compare installed vs store version
```bash
# Get installed version
snap debug api /v2/snaps/<name> | jq '{installed: .result.version, revision: .result.revision, channel: .result.channel}'

# Check store for available version
snap debug api '/v2/find?name=<name>' | jq '.result[0] | {store_version: .version, channels: .channels}'
```

## Direct socket access (alternative)

If `snap debug api` is unavailable, you can use `curl` with the Unix socket directly:

```bash
curl --unix-socket /run/snapd.socket http://localhost/v2/system-info
curl --unix-socket /run/snapd.socket 'http://localhost/v2/snaps?select=all'
curl --unix-socket /run/snapd.socket -X POST \
  -H 'Content-Type: application/json' \
  -d '{"action": "refresh"}' http://localhost/v2/snaps/<name>
```
