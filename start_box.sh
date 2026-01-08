#!/bin/bash
# ════════════════════════════════════════════════════════════════════
#  🤖 NASBot Launcher
#  Uso: ./start_box.sh [start|stop|status|restart]
# ════════════════════════════════════════════════════════════════════

# ─── CONFIGURAZIONE (modifica questi valori) ────────────────────────
export BOT_TOKEN="IL_TUO_TOKEN"
export BOT_USER_ID="IL_TUO_USER_ID"
BOT_DIR="/Volume1/public"
LOG_FILE="$BOT_DIR/nasbot.log"
# ────────────────────────────────────────────────────────────────────

# Colori
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# Funzioni
get_pid() {
    pgrep -x "nasbot" 2>/dev/null
}

do_start() {
    local pid=$(get_pid)
    if [[ -n "$pid" ]]; then
        echo -e "${YELLOW}⚡ NASBot già attivo (PID $pid)${NC}"
        return 0
    fi

    # Validazione
    if [[ -z "$BOT_TOKEN" || "$BOT_TOKEN" == "IL_TUO_TOKEN" ]]; then
        echo -e "${RED}✗ BOT_TOKEN non configurato!${NC}"
        return 1
    fi
    if [[ -z "$BOT_USER_ID" || "$BOT_USER_ID" == "IL_TUO_USER_ID" ]]; then
        echo -e "${RED}✗ BOT_USER_ID non configurato!${NC}"
        return 1
    fi

    cd "$BOT_DIR" || { echo -e "${RED}✗ Directory $BOT_DIR non trovata${NC}"; return 1; }

    if [[ ! -x "./nasbot" ]]; then
        echo -e "${RED}✗ Binario nasbot non trovato o non eseguibile${NC}"
        return 1
    fi

    # Avvio
    echo -e "${CYAN}▶ Avvio NASBot...${NC}"
    nohup ./nasbot >> "$LOG_FILE" 2>&1 &
    sleep 2

    pid=$(get_pid)
    if [[ -n "$pid" ]]; then
        echo -e "${GREEN}✔ NASBot avviato (PID $pid)${NC}"
        echo -e "  Log: $LOG_FILE"
    else
        echo -e "${RED}✗ Avvio fallito — controlla $LOG_FILE${NC}"
        tail -5 "$LOG_FILE" 2>/dev/null
        return 1
    fi
}

do_stop() {
    local pid=$(get_pid)
    if [[ -z "$pid" ]]; then
        echo -e "${YELLOW}⚠ NASBot non in esecuzione${NC}"
        return 0
    fi

    echo -e "${CYAN}▶ Arresto NASBot (PID $pid)...${NC}"
    kill "$pid" 2>/dev/null
    sleep 1

    if [[ -z "$(get_pid)" ]]; then
        echo -e "${GREEN}✔ NASBot fermato${NC}"
    else
        echo -e "${YELLOW}⚠ Invio SIGKILL...${NC}"
        kill -9 "$pid" 2>/dev/null
        echo -e "${GREEN}✔ NASBot terminato${NC}"
    fi
}

do_status() {
    local pid=$(get_pid)
    if [[ -n "$pid" ]]; then
        local uptime=$(ps -o etime= -p "$pid" 2>/dev/null | tr -d ' ')
        echo -e "${GREEN}● NASBot attivo${NC}"
        echo -e "  PID: $pid"
        echo -e "  Uptime: $uptime"
        echo -e "  Log: $LOG_FILE"
    else
        echo -e "${RED}○ NASBot non attivo${NC}"
    fi
}

do_logs() {
    if [[ -f "$LOG_FILE" ]]; then
        echo -e "${CYAN}═══ Ultimi 20 log ═══${NC}"
        tail -20 "$LOG_FILE"
    else
        echo -e "${YELLOW}Nessun log trovato${NC}"
    fi
}

# ─── MAIN ───────────────────────────────────────────────────────────
case "${1:-start}" in
    start)   do_start ;;
    stop)    do_stop ;;
    restart) do_stop && sleep 1 && do_start ;;
    status)  do_status ;;
    logs)    do_logs ;;
    *)
        echo "NASBot Launcher"
        echo ""
        echo "Uso: $0 {start|stop|restart|status|logs}"
        echo ""
        echo "  start   - Avvia il bot"
        echo "  stop    - Ferma il bot"
        echo "  restart - Riavvia il bot"
        echo "  status  - Mostra stato"
        echo "  logs    - Mostra ultimi log"
        echo ""
        echo "Per avvio automatico al boot, aggiungi al crontab:"
        echo "  @reboot $BOT_DIR/start_box.sh start"
        exit 1
        ;;
esac
