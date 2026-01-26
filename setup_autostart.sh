#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot Autostart Setup
#  Configures autostart for TerraMaster and systems without systemd
# ═══════════════════════════════════════════════════════════════════

set -e

# ─── CONFIG ─────────────────────────────────────────
BOT_DIR="/Volume1/public"
SCRIPT_PATH="$BOT_DIR/start_bot.sh"
CRON_SCHEDULE="*/5 * * * *"  # Every 5 minutes
# ────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "═══════════════════════════════════════════════════════════"
echo "  NASBot Autostart Setup"
echo "═══════════════════════════════════════════════════════════"
echo ""

# Verify root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}❌ This script runs as root${NC}"
    echo "   Use: sudo $0"
    exit 1
fi

# Verify script existence
if [ ! -f "$SCRIPT_PATH" ]; then
    echo -e "${RED}❌ Script not found: $SCRIPT_PATH${NC}"
    echo "   Ensure files are copied to $BOT_DIR"
    exit 1
fi

# Make script executable
chmod +x "$SCRIPT_PATH"
chmod +x "$BOT_DIR/nasbot" 2>/dev/null || true

echo "📋 Configuration:"
echo "   Directory: $BOT_DIR"
echo "   Script: $SCRIPT_PATH"
echo "   Cron: $CRON_SCHEDULE (watchdog every 5 min)"
echo ""

# ─── METHOD 1: Cron job ─────────────────────────────
setup_cron() {
    echo "🔧 Configuring cron job..."
    
    CRON_LINE="$CRON_SCHEDULE $SCRIPT_PATH watchdog >/dev/null 2>&1"
    CRON_COMMENT="# NASBot watchdog - automatically restarts if inactive"
    
    # Remove existing entries
    crontab -l 2>/dev/null | grep -v "nasbot\|NASBot\|start_bot.sh" > /tmp/crontab_new || true
    
    # Add new cron
    echo "" >> /tmp/crontab_new
    echo "$CRON_COMMENT" >> /tmp/crontab_new
    echo "$CRON_LINE" >> /tmp/crontab_new
    
    # Add boot start
    echo "# NASBot boot start" >> /tmp/crontab_new
    echo "@reboot sleep 60 && $SCRIPT_PATH start >/dev/null 2>&1" >> /tmp/crontab_new
    
    crontab /tmp/crontab_new
    rm /tmp/crontab_new
    
    echo -e "${GREEN}✅ Cron job configured${NC}"
}

# ─── METHOD 2: rc.local (fallback) ──────────────────
setup_rclocal() {
    echo "🔧 Configuring rc.local (fallback)..."
    
    RC_LOCAL="/etc/rc.local"
    RC_LINE="$SCRIPT_PATH start &"
    
    # Create rc.local if missing
    if [ ! -f "$RC_LOCAL" ]; then
        echo "#!/bin/bash" > "$RC_LOCAL"
        echo "# rc.local - executed at boot" >> "$RC_LOCAL"
        echo "" >> "$RC_LOCAL"
        echo "exit 0" >> "$RC_LOCAL"
        chmod +x "$RC_LOCAL"
    fi
    
    # Check if already present
    if grep -q "start_bot.sh\|nasbot" "$RC_LOCAL" 2>/dev/null; then
        echo "   ℹ️  Entry already present in rc.local"
    else
        # Insert before "exit 0"
        sed -i "/^exit 0/i # NASBot autostart\nsleep 60\n$RC_LINE\n" "$RC_LOCAL" 2>/dev/null || {
            # If sed fails, append to end
            echo "" >> "$RC_LOCAL"
            echo "# NASBot autostart" >> "$RC_LOCAL"
            echo "sleep 60" >> "$RC_LOCAL"
            echo "$RC_LINE" >> "$RC_LOCAL"
        }
        echo -e "${GREEN}✅ rc.local configured${NC}"
    fi
}

# ─── METHOD 3: init.d (TerraMaster) ─────────────────
setup_initd() {
    echo "🔧 Configuring init.d (TerraMaster)..."
    
    INIT_SCRIPT="/etc/init.d/nasbot"
    
    cat > "$INIT_SCRIPT" << 'INITEOF'
#!/bin/sh
### BEGIN INIT INFO
# Provides:          nasbot
# Required-Start:    $local_fs $network
# Required-Stop:     $local_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Description:       NASBot Telegram Bot
### END INIT INFO

BOT_DIR="/Volume1/public"
SCRIPT="$BOT_DIR/start_bot.sh"

case "$1" in
    start)
        sleep 30
        $SCRIPT start
        ;;
    stop)
        $SCRIPT stop
        ;;
    restart)
        $SCRIPT restart
        ;;
    status)
        $SCRIPT status
        ;;
    *)
        echo "Usage: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac

exit 0
INITEOF

    chmod +x "$INIT_SCRIPT"
    
    # Enable on systems with update-rc.d
    if command -v update-rc.d >/dev/null 2>&1; then
        update-rc.d nasbot defaults 2>/dev/null || true
    fi
    
    # Enable on systems with chkconfig
    if command -v chkconfig >/dev/null 2>&1; then
        chkconfig --add nasbot 2>/dev/null || true
        chkconfig nasbot on 2>/dev/null || true
    fi
    
    echo -e "${GREEN}✅ init.d script created${NC}"
}

# ─── EXECUTE SETUP ───────────────────────────────────
echo "Applying configurations..."
echo ""

# Try all methods for maximum compatibility
setup_cron
echo ""

# If /etc/init.d exists, use it too
if [ -d "/etc/init.d" ]; then
    setup_initd
    echo ""
fi

# If rc.local exists, configure it as backup
if [ -f "/etc/rc.local" ] || [ -d "/etc" ]; then
    setup_rclocal
    echo ""
fi

# ─── START IMMEDIATELY ───────────────────────────────────
echo "🚀 Starting bot immediately..."
$SCRIPT_PATH start

echo ""
echo "═══════════════════════════════════════════════════════════"
echo -e "${GREEN}✅ Setup completed!${NC}"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "The bot now:"
echo "  • Starts automatically at boot (after 60 seconds)"
echo "  • Is checked every 5 minutes by the watchdog"
echo "  • Restarts automatically if it crashes"
echo ""
echo "Useful commands:"
echo "  $SCRIPT_PATH status    - Check status"
echo "  $SCRIPT_PATH logs      - View logs"
echo "  crontab -l              - View cron jobs"
echo ""
