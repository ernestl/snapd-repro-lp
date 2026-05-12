#!/bin/bash
# Install dependencies for snapd-repro-lp.
# Run with: sudo ./setup.sh

set -eu

echo "=== snapd-repro-lp dependency setup ==="

# Check we're running as root.
if [ "$(id -u)" -ne 0 ]; then
    echo "Error: this script must be run as root (sudo ./setup.sh)"
    exit 1
fi

REAL_USER="${SUDO_USER:-$USER}"

# --- Go ---
if command -v go &>/dev/null; then
    echo "[ok] Go is installed: $(go version)"
else
    echo "[install] Installing Go via snap..."
    snap install go --classic
    echo "[ok] Go installed: $(go version)"
fi

# --- LXD ---
if snap list lxd &>/dev/null; then
    echo "[ok] LXD is installed"
else
    echo "[install] Installing LXD..."
    snap install lxd
    echo "[ok] LXD installed"
fi

# --- LXD init ---
# Check if LXD has been initialised by looking for any storage pools.
if lxc storage list --format csv 2>/dev/null | grep -q .; then
    echo "[ok] LXD is initialised"
else
    echo "[init] Initialising LXD with defaults..."
    lxd init --auto
    echo "[ok] LXD initialised"
fi

# Verify LXD is responsive.
if lxc list --format csv &>/dev/null; then
    echo "[ok] LXD is responsive"
else
    echo "[warn] LXD is not responding. You may need to restart the LXD service:"
    echo "  sudo snap restart lxd"
fi

# --- LXD group membership ---
if id -nG "$REAL_USER" | grep -qw lxd; then
    echo "[ok] User $REAL_USER is in the lxd group"
else
    echo "[config] Adding $REAL_USER to the lxd group..."
    usermod -aG lxd "$REAL_USER"
    echo "[ok] User $REAL_USER added to lxd group"
    echo ""
    echo "*** You must log out and back in (or run 'newgrp lxd') for group changes to take effect. ***"
fi

echo ""
echo "=== Setup complete ==="
echo ""
echo "Build the tool:"
echo "  go build -o snapd-repro-lp ./..."
echo ""
echo "Test LXD:"
echo "  ./snapd-repro-lp test lxd launch 24.04"
