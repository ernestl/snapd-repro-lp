# Snap Confinement and AppArmor Profile Lifecycle

How snap confinement works at runtime and how AppArmor profiles are managed
throughout the system lifecycle. Use this skill to navigate the relevant source
code when investigating confinement failures, AppArmor profile issues, or
boot-time race conditions involving snap services.

## What snap-confine does

`snap-confine` is a C binary executed by `snap run` before the confined snap
application starts. It sets up the execution environment: AppArmor profile
transitions, mount namespace setup, cgroup placement, and seccomp filters.

The source code lives in two directories:
- `cmd/snap-confine/` -- the main binary
- `cmd/libsnap-confine-private/` -- shared C library with AppArmor support,
  mount utilities, and cleanup functions

To find the version installed on a system:

```bash
snap-confine --version
# or check the snap-confine binary path
ls -la /usr/lib/snapd/snap-confine
ls -la /snap/snapd/current/usr/lib/snapd/snap-confine
```

## AppArmor profile lifecycle

### Profile file locations

```bash
# Snap-specific profiles managed by snapd
ls /var/lib/snapd/apparmor/profiles/

# Compiled profile cache
ls /var/cache/apparmor/

# System-wide AppArmor profiles (not snap-specific)
ls /etc/apparmor.d/

# snap-confine's own profile (may be in either location depending on
# whether snapd is running from the deb or the snap)
ls /etc/apparmor.d/usr.lib.snapd.snap-confine*
ls /var/lib/snapd/apparmor/profiles/snap-confine*
```

### Boot-time profile loading

On classic Ubuntu systems, AppArmor profiles for snaps are loaded by a
dedicated systemd service. The service unit template lives in the snapd
source at `data/systemd/snapd.apparmor.service.in`.

Key service ordering to understand:

```bash
# Check the service unit and its dependencies
systemctl cat snapd.apparmor.service
systemctl show snapd.apparmor.service -p After -p Before -p Wants

# Check the system-wide AppArmor service
systemctl cat apparmor.service
systemctl show apparmor.service -p After -p Before

# Verify the actual boot ordering that occurred
systemd-analyze critical-chain snapd.apparmor.service
systemd-analyze critical-chain snapd.service
```

The profile loading binary is `snapd-apparmor`, source at
`cmd/snapd-apparmor/`. It loads all profiles from
`/var/lib/snapd/apparmor/profiles/` using the AppArmor backend in
`sandbox/apparmor/profile.go`.

### Runtime profile management

When snapd installs, refreshes, or removes a snap, it updates AppArmor
profiles through the Go backend:

```bash
# Key source files for profile management
# sandbox/apparmor/profile.go  -- LoadProfiles, ReloadAllSnapProfiles
# overlord/ifacestate/         -- security profile setup during snap operations
```

### Inspecting AppArmor state

```bash
# List all loaded profiles and their modes (enforce/complain)
aa-status

# Check for AppArmor denials in kernel log
dmesg | grep DENIED
journalctl -k | grep DENIED

# View a specific snap's AppArmor profile
cat /var/lib/snapd/apparmor/profiles/snap.<snapname>.<app>

# Check if snap-confine's profile is loaded
aa-status | grep snap-confine

# Check the snapd.apparmor service status
systemctl status snapd.apparmor.service
journalctl -u snapd.apparmor.service
```

## Where to look by symptom

| Symptom | Where to look |
|---------|--------------|
| snap-confine crashes or errors | `cmd/snap-confine/snap-confine.c`, `cmd/libsnap-confine-private/` |
| AppArmor transition failure | `cmd/libsnap-confine-private/apparmor-support.c` |
| Profile not loaded at boot | `data/systemd/snapd.apparmor.service.in`, `cmd/snapd-apparmor/` |
| Profile reload issues | `sandbox/apparmor/profile.go` (LoadProfiles, ReloadAllSnapProfiles) |
| Service ordering / boot race | `data/systemd/snapd.apparmor.service.in` (After=, Before= directives) |
| Mount namespace issues | `cmd/snap-confine/ns-support.c`, `cmd/libsnap-confine-private/mount-support.c` |
| Cgroup placement issues | `cmd/snap-confine/snap-confine.c` (cgroup setup sections) |

## Searching for confinement errors

When a bug report contains a snap-confine or AppArmor-related error message,
use the code tracing approach:

```bash
cd ~/snapd

# Search the C code for error messages from snap-confine
grep -rn 'die(' cmd/snap-confine/ cmd/libsnap-confine-private/
grep -rn 'sc_die_on_error' cmd/snap-confine/ cmd/libsnap-confine-private/

# Search for AppArmor-related error handling
grep -rn 'apparmor' cmd/snap-confine/ cmd/libsnap-confine-private/
grep -rn 'aa_change' cmd/snap-confine/ cmd/libsnap-confine-private/

# Search the Go code for profile loading errors
grep -rn 'apparmor_parser' sandbox/apparmor/
grep -rn 'LoadProfiles\|ReloadAllSnapProfiles' sandbox/apparmor/

# Search for the snapd-apparmor helper
grep -rn 'loadAppArmorProfiles\|LoadProfiles' cmd/snapd-apparmor/
```

## Understanding service interactions

When investigating bugs that involve interactions between services at boot
time or during snap operations:

```bash
# List all snap-related systemd units
systemctl list-units 'snap*' --all

# Check service dependencies
systemctl list-dependencies snapd.service
systemctl list-dependencies snapd.apparmor.service

# Check what ran when during boot
systemd-analyze blame
systemd-analyze critical-chain snapd.service

# Check for failed units
systemctl list-units --state=failed
```
