#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot Launcher — start_bot.sh
#  Auto-recovery and advanced management for TerraMaster F2-212
# ═══════════════════════════════════════════════════════════════════

# ─── CONFIG ─────────────────────────────────────────
BOT_DIR="/Volume1/public"
BOT_NAME="nasbot"
LOG_FILE="$BOT_DIR/nasbot.log"
PID_FILE="$BOT_DIR/nasbot.pid"
MAX_LOG_SIZE=$((10*1024*1024))  # 10MB
# ────────────────────────────────────────────────────

cd "$BOT_DIR" || exit 1

# Ensure execution permissions for the binary
chmod +x "$BOT_DIR/$BOT_NAME" 2>/dev/null

# Function to rotate logs if too large
rotate_logs() {
    if [ -f "$LOG_FILE" ] && [ $(stat -f%z "$LOG_FILE" 2>/dev/null || stat -c%s "$LOG_FILE" 2>/dev/null) -gt $MAX_LOG_SIZE ]; then
        mv "$LOG_FILE" "$LOG_FILE.old"
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Log rotated" > "$LOG_FILE"
    fi
}

# Function to check if bot is running
is_running() {
    if [ -f "$PID_FILE" ]; then
        pid=$(cat "$PID_FILE")
        if kill -0 "$pid" 2>/dev/null; then
            return 0
        fi
    fi
    # Fallback: search for process
    pgrep -x "$BOT_NAME" >/dev/null 2>&1
}

# Function to get PID
get_pid() {
    if [ -f "$PID_FILE" ]; then
        cat "$PID_FILE"
    else
        pgrep -x "$BOT_NAME"
    fi
}

# Function to start bot
start_bot() {
    if is_running; then
        echo "⚠️  Bot already running (PID: $(get_pid))"
        return 1
    fi
    
    rotate_logs
    
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting NASBot..." >> "$LOG_FILE"
    nohup ./$BOT_NAME >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    
    sleep 2
    if is_running; then
        echo "✅ Bot started (PID: $(get_pid))"
        return 0
    else
        echo "❌ Error starting bot. Check $LOG_FILE"
        return 1
    fi
}

# Function to stop bot
stop_bot() {
    if ! is_running; then
        echo "ℹ️  Bot not running"
        rm -f "$PID_FILE"
        return 0
    fi
    
    pid=$(get_pid)
    echo "⏳ Stopping bot (PID: $pid)..."
    
    # Graceful shutdown
    kill -TERM "$pid" 2>/dev/null
    
    # Wait up to 10 seconds
    for i in {1..10}; do
        if ! is_running; then
            echo "✅ Bot stopped"
            rm -f "$PID_FILE"
            return 0
        fi
        sleep 1
    done
    
    # Force kill if still active
    kill -9 "$pid" 2>/dev/null
    pkill -9 -x "$BOT_NAME" 2>/dev/null
    rm -f "$PID_FILE"
    echo "⚠️  Bot forcibly terminated"
}

# Function to restart
restart_bot() {
    echo "🔄 Restarting bot..."
    stop_bot
    sleep 2
    start_bot
}

# Watchdog function (for cron)
watchdog() {
    if ! is_running; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] WATCHDOG: Bot not running, restarting..." >> "$LOG_FILE"
        start_bot
    fi
}

# Detailed status function
status_bot() {
    echo "═══════════════════════════════════════"
    echo "  NASBot Status"
    echo "═══════════════════════════════════════"
    
    if is_running; then
        pid=$(get_pid)
        echo "🟢 Status: ACTIVE"
        echo "📋 PID: $pid"
        
        # Process uptime
        if [ -f "/proc/$pid/stat" ]; then
            start_time=$(stat -c %Y /proc/$pid 2>/dev/null)
            if [ -n "$start_time" ]; then
                now=$(date +%s)
                uptime=$((now - start_time))
                hours=$((uptime / 3600))
                mins=$(((uptime % 3600) / 60))
                echo "⏱️  Uptime: ${hours}h ${mins}m"
            fi
        fi
        
        # Memory usage
        if command -v ps >/dev/null; then
            mem=$(ps -o rss= -p "$pid" 2>/dev/null | awk '{print $1/1024 "MB"}')
            echo "💾 Memory: $mem"
        fi
    else
        echo "🔴 Status: INACTIVE"
    fi
    
    echo "───────────────────────────────────────"
    echo "📁 Directory: $BOT_DIR"
    echo "📝 Log: $LOG_FILE"
    
    if [ -f "$LOG_FILE" ]; then
        log_size=$(ls -lh "$LOG_FILE" | awk '{print $5}')
        echo "📊 Log size: $log_size"
    fi
    
    echo "═══════════════════════════════════════"
}

# Show last logs
show_logs() {
    lines=${1:-50}
    if [ -f "$LOG_FILE" ]; then
        echo "═══════════════════════════════════════"
        echo "  Last $lines logs"
        echo "═══════════════════════════════════════"
        tail -n "$lines" "$LOG_FILE"
    else
        echo "❌ Log file not found"
    fi
}

# ─── MAIN ───────────────────────────────────────────
case "${1:-}" in
    start)
        start_bot
        ;;
    stop)
        stop_bot
        ;;
    restart)
        restart_bot
        ;;
    status)
        status_bot
        ;;
    watchdog)
        watchdog
        ;;
    logs)
        show_logs "${2:-50}"
        ;;
    *)
        echo "NASBot Manager"
        echo ""
        echo "Usage: $0 {start|stop|restart|status|watchdog|logs [n]}"
        echo ""
        echo "  start     - Start the bot"
        echo "  stop      - Stop the bot"
        echo "  restart   - Restart the bot"
        echo "  status    - Show detailed status"
        echo "  watchdog  - Restart if inactive (for cron)"
        echo "  logs [n]  - Show last n logs (default: 50)"
        ;;
esac
