#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot Launcher — start_bot.sh
#  Auto-recovery e gestione avanzata per TerraMaster F2-212
# ═══════════════════════════════════════════════════════════════════

# ─── CONFIG ─────────────────────────────────────────
export BOT_TOKEN=""
export BOT_USER_ID=""
BOT_DIR="/Volume1/public"
BOT_NAME="nasbot"
LOG_FILE="$BOT_DIR/nasbot.log"
PID_FILE="$BOT_DIR/nasbot.pid"
MAX_LOG_SIZE=$((10*1024*1024))  # 10MB
# ────────────────────────────────────────────────────

cd "$BOT_DIR" || exit 1

# Funzione per ruotare i log se troppo grandi
rotate_logs() {
    if [ -f "$LOG_FILE" ] && [ $(stat -f%z "$LOG_FILE" 2>/dev/null || stat -c%s "$LOG_FILE" 2>/dev/null) -gt $MAX_LOG_SIZE ]; then
        mv "$LOG_FILE" "$LOG_FILE.old"
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] Log ruotato" > "$LOG_FILE"
    fi
}

# Funzione per verificare se il bot è attivo
is_running() {
    if [ -f "$PID_FILE" ]; then
        pid=$(cat "$PID_FILE")
        if kill -0 "$pid" 2>/dev/null; then
            return 0
        fi
    fi
    # Fallback: cerca il processo
    pgrep -x "$BOT_NAME" >/dev/null 2>&1
}

# Funzione per ottenere il PID
get_pid() {
    if [ -f "$PID_FILE" ]; then
        cat "$PID_FILE"
    else
        pgrep -x "$BOT_NAME"
    fi
}

# Funzione per avviare il bot
start_bot() {
    if is_running; then
        echo "⚠️  Bot già attivo (PID: $(get_pid))"
        return 1
    fi
    
    rotate_logs
    
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] Avvio NASBot..." >> "$LOG_FILE"
    nohup ./$BOT_NAME >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"
    
    sleep 2
    if is_running; then
        echo "✅ Bot avviato (PID: $(get_pid))"
        return 0
    else
        echo "❌ Errore avvio bot. Controlla $LOG_FILE"
        return 1
    fi
}

# Funzione per fermare il bot
stop_bot() {
    if ! is_running; then
        echo "ℹ️  Bot non attivo"
        rm -f "$PID_FILE"
        return 0
    fi
    
    pid=$(get_pid)
    echo "⏳ Fermando bot (PID: $pid)..."
    
    # Graceful shutdown
    kill -TERM "$pid" 2>/dev/null
    
    # Aspetta fino a 10 secondi
    for i in {1..10}; do
        if ! is_running; then
            echo "✅ Bot fermato"
            rm -f "$PID_FILE"
            return 0
        fi
        sleep 1
    done
    
    # Force kill se ancora attivo
    kill -9 "$pid" 2>/dev/null
    pkill -9 -x "$BOT_NAME" 2>/dev/null
    rm -f "$PID_FILE"
    echo "⚠️  Bot terminato forzatamente"
}

# Funzione per riavviare
restart_bot() {
    echo "🔄 Riavvio bot..."
    stop_bot
    sleep 2
    start_bot
}

# Funzione watchdog (per cron)
watchdog() {
    if ! is_running; then
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] WATCHDOG: Bot non attivo, riavvio..." >> "$LOG_FILE"
        start_bot
    fi
}

# Funzione status dettagliato
status_bot() {
    echo "═══════════════════════════════════════"
    echo "  NASBot Status"
    echo "═══════════════════════════════════════"
    
    if is_running; then
        pid=$(get_pid)
        echo "🟢 Stato: ATTIVO"
        echo "📋 PID: $pid"
        
        # Uptime processo
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
        
        # Memoria usata
        if command -v ps >/dev/null; then
            mem=$(ps -o rss= -p "$pid" 2>/dev/null | awk '{print $1/1024 "MB"}')
            echo "💾 Memoria: $mem"
        fi
    else
        echo "🔴 Stato: NON ATTIVO"
    fi
    
    echo "───────────────────────────────────────"
    echo "📁 Directory: $BOT_DIR"
    echo "📝 Log: $LOG_FILE"
    
    if [ -f "$LOG_FILE" ]; then
        log_size=$(ls -lh "$LOG_FILE" | awk '{print $5}')
        echo "📊 Dimensione log: $log_size"
    fi
    
    echo "═══════════════════════════════════════"
}

# Mostra ultimi log
show_logs() {
    lines=${1:-50}
    if [ -f "$LOG_FILE" ]; then
        echo "═══════════════════════════════════════"
        echo "  Ultimi $lines log"
        echo "═══════════════════════════════════════"
        tail -n "$lines" "$LOG_FILE"
    else
        echo "❌ File log non trovato"
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
        echo "Uso: $0 {start|stop|restart|status|watchdog|logs [n]}"
        echo ""
        echo "  start     - Avvia il bot"
        echo "  stop      - Ferma il bot"
        echo "  restart   - Riavvia il bot"
        echo "  status    - Mostra stato dettagliato"
        echo "  watchdog  - Riavvia se non attivo (per cron)"
        echo "  logs [n]  - Mostra ultimi n log (default: 50)"
        ;;
esac
