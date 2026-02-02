#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
# NASBot - Kernel Panic Auto-Reboot Setup
# ═══════════════════════════════════════════════════════════════════
# This script configures the system to automatically reboot after
# a kernel panic, so the NAS can recover without manual intervention.
#
# Usage: sudo ./setup_kernel_panic.sh
# ═══════════════════════════════════════════════════════════════════

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  NASBot - Kernel Panic Auto-Reboot Setup${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}❌ This script must be run as root (sudo)${NC}"
    exit 1
fi

# Seconds to wait before rebooting after kernel panic
PANIC_TIMEOUT=10

echo -e "${YELLOW}This will configure the system to automatically reboot"
echo -e "$PANIC_TIMEOUT seconds after a kernel panic.${NC}"
echo

# Check current setting
CURRENT=$(cat /proc/sys/kernel/panic 2>/dev/null || echo "0")
echo -e "Current setting: kernel.panic = ${CURRENT}"

if [ "$CURRENT" -gt 0 ]; then
    echo -e "${GREEN}✅ Auto-reboot is already enabled (${CURRENT}s timeout)${NC}"
    read -p "Update to ${PANIC_TIMEOUT}s? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Keeping current setting."
        exit 0
    fi
fi

# Apply immediately
echo -e "\n${YELLOW}Applying settings...${NC}"
echo $PANIC_TIMEOUT > /proc/sys/kernel/panic
echo -e "${GREEN}✓ Immediate: kernel.panic = $PANIC_TIMEOUT${NC}"

# Make persistent via sysctl.conf
SYSCTL_FILE="/etc/sysctl.conf"
SYSCTL_D="/etc/sysctl.d/99-kernel-panic.conf"

# Check if setting already exists
if grep -q "^kernel.panic" "$SYSCTL_FILE" 2>/dev/null; then
    # Update existing
    sed -i "s/^kernel.panic.*/kernel.panic = $PANIC_TIMEOUT/" "$SYSCTL_FILE"
    echo -e "${GREEN}✓ Updated $SYSCTL_FILE${NC}"
else
    # Add to sysctl.d (cleaner approach)
    echo "# Auto-reboot after kernel panic (NASBot)" > "$SYSCTL_D"
    echo "kernel.panic = $PANIC_TIMEOUT" >> "$SYSCTL_D"
    echo -e "${GREEN}✓ Created $SYSCTL_D${NC}"
fi

# Also handle kernel.panic_on_oops (reboot on kernel oops too)
echo 1 > /proc/sys/kernel/panic_on_oops
if [ -f "$SYSCTL_D" ]; then
    echo "kernel.panic_on_oops = 1" >> "$SYSCTL_D"
fi

# Verify
echo
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}✅ Configuration complete!${NC}"
echo -e "${GREEN}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "Current settings:"
echo "  kernel.panic = $(cat /proc/sys/kernel/panic)"
echo "  kernel.panic_on_oops = $(cat /proc/sys/kernel/panic_on_oops)"
echo
echo -e "${YELLOW}The system will now automatically reboot ${PANIC_TIMEOUT}s after:${NC}"
echo "  • Kernel panic"
echo "  • Kernel oops (serious kernel error)"
echo
echo -e "${GREEN}This setting persists across reboots.${NC}"
