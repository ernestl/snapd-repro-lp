# Snap Connections and Interfaces Debugging

## List connections
```bash
snap connections              # all connections on the system
snap connections <name>       # connections for a specific snap
snap connections --all        # include unconnected plugs/slots
snap interface <interface>    # details about a specific interface
snap interfaces               # summary of all interfaces
```

## Connect and disconnect
```bash
snap connect <snap>:<plug> <snap>:<slot>
snap connect <snap>:<plug>            # connect to system slot
snap disconnect <snap>:<plug> <snap>:<slot>
snap disconnect <snap>:<plug>         # disconnect from any slot
```

## Common interfaces
- **network**: outbound network access
- **network-bind**: listen on network ports
- **home**: access to user home directory
- **removable-media**: access to /media and /mnt
- **desktop**: access to desktop session (fonts, themes, etc.)
- **x11**: X11 display access
- **wayland**: Wayland display access
- **audio-playback**: play audio
- **camera**: camera device access
- **browser-support**: browser sandboxing support
- **personal-files**: access to specific dotfiles (requires store approval)
- **system-files**: access to specific system paths (requires store approval)

## Diagnosing permission denials

### Check AppArmor denials
```bash
dmesg | grep DENIED
journalctl -k | grep DENIED
aa-status                      # AppArmor status and profiles
cat /var/lib/snapd/apparmor/profiles/snap.<name>.<app>  # view profile
```

### Check seccomp denials
```bash
journalctl -k | grep seccomp
cat /var/lib/snapd/seccomp/bpf/snap.<name>.<app>.src  # view seccomp filter source
```

### Debug connection issues
1. Run `snap connections <name>` to see current connections.
2. Look for missing connections (unconnected plugs).
3. Try connecting the interface: `snap connect <snap>:<plug>`.
4. If auto-connect fails, check if the interface requires manual connection.
5. Check `snap interface <interface>` to see which snaps provide the slot.

## Slot providers
```bash
snap interface <interface>  # shows all plugs and slots for an interface
```

Most interfaces are provided by the system (core/snapd snap). Some are provided by other snaps (content interfaces, dbus interfaces).

## Content interfaces
```bash
snap connections | grep content  # find content-sharing connections
```

Content interfaces allow snaps to share files. Common for themes, libraries, and platform snaps.

## Common issues
- **Missing auto-connection**: Some interfaces are not auto-connected. Check the snap's `snap.yaml` for `plugs` declarations.
- **AppArmor DENIED after connect**: The snap may need to be restarted after connecting an interface.
- **Interface not found**: The slot-providing snap may not be installed.
