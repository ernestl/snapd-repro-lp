# LXD Instance Management

## Overview

LXD supports two instance types:
- **Virtual machines (VMs)**: full hypervisor with systemd, kernel, and hardware emulation. Use for most bug reproduction.
- **Containers**: lightweight, shared-kernel isolation. Use only when the bug is specifically about container behavior.

## Launching a VM (default for bug reproduction)

```bash
lxc launch ubuntu:<version> <name> --vm
# Example:
lxc launch ubuntu:24.04 my-vm --vm

# Access the VM:
lxc exec my-vm -- bash
```

VMs boot slower (30-60s) but support all snaps, full systemd, and nested virtualization.

## Launching a container

```bash
lxc launch ubuntu:<version> <name>
# Example:
lxc launch ubuntu:24.04 my-container

# Access the container:
lxc exec my-container -- bash
```

Containers boot fast (~5s) but have limited systemd and cannot run snaps that need specific privileges (e.g., lxd, multipass, docker).

## Nested container inside a VM (for container-specific bugs)

When a bug is about behavior inside a container, launch a VM first, then create a nested container inside it:

```bash
# On the host: launch a VM
lxc launch ubuntu:24.04 repro-vm --vm

# Inside the VM: install and configure LXD
lxc exec repro-vm -- bash
apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y snapd
snap install lxd
lxd init --auto

# Launch a nested container inside the VM
lxc launch ubuntu:24.04 inner-container

# Run commands in the nested container
lxc exec inner-container -- bash -c "<command>"
```

### LXD init options for nested setups

```bash
# Non-interactive with defaults:
lxd init --auto

# With specific network/storage:
lxd init --auto --network-address=0.0.0.0 --storage-driver=zfs
```

## Common operations

```bash
# List instances
lxc list

# Stop an instance
lxc stop <name>

# Start an instance
lxc start <name>

# Delete an instance
lxc delete <name> --force

# Check instance info
lxc info <name>

# Push a file into an instance
lxc file push <local-path> <name>/<remote-path>

# Pull a file from an instance
lxc file pull <name>/<remote-path> <local-path>
```

## When to use each type

| Bug type | Instance type | Notes |
|----------|--------------|-------|
| General snapd bug | VM | Full systemd, all snaps work |
| Snap install/refresh failure | VM | Some snaps fail in containers |
| Terminal output formatting | VM | Containers work too but VM is default |
| Container-specific behavior | VM + nested container | Bug manifests inside a container |
| systemd integration | VM | Containers have limited systemd |
| Snap services | VM | Services rely on systemd |

## Troubleshooting

- **VM won't start**: ensure KVM is available (`ls /dev/kvm`). Some environments lack hardware virtualization.
- **Nested container can't reach network**: check `lxc network list` inside the VM. The `lxdbr0` bridge should exist after `lxd init`.
- **Snap install fails in container**: this is expected for snaps needing privileges. Use a VM instead.
- **lxd init fails**: try `lxd init --auto` with no interactive prompts. If ZFS is unavailable, it falls back to dir backend.
