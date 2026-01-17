#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot Autostart Setup
#  Configura avvio automatico per TerraMaster e sistemi senza systemd
# ═══════════════════════════════════════════════════════════════════

set -e

# ─── CONFIG ─────────────────────────────────────────
BOT_DIR="/Volume1/public"
SCRIPT_PATH="$BOT_DIR/start_bot.sh"
CRON_SCHEDULE="*/5 * * * *"  # Ogni 5 minuti
# ────────────────────────────────────────────────────

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "═══════════════════════════════════════════════════════════"
echo "  NASBot Autostart Setup"
echo "═══════════════════════════════════════════════════════════"
echo ""

# Verifica root
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}❌ Questo script deve essere eseguito come root${NC}"
    echo "   Usa: sudo $0"
    exit 1
fi

# Verifica esistenza script
if [ ! -f "$SCRIPT_PATH" ]; then
    echo -e "${RED}❌ Script non trovato: $SCRIPT_PATH${NC}"
    echo "   Assicurati di aver copiato i file in $BOT_DIR"
    exit 1
fi

# Rendi eseguibile lo script
chmod +x "$SCRIPT_PATH"
chmod +x "$BOT_DIR/nasbot" 2>/dev/null || true

echo "📋 Configurazione:"
echo "   Directory: $BOT_DIR"
echo "   Script: $SCRIPT_PATH"
echo "   Cron: $CRON_SCHEDULE (watchdog ogni 5 min)"
echo ""

# ─── METODO 1: Cron job ─────────────────────────────
setup_cron() {
    echo "🔧 Configurazione cron job..."
    
    CRON_LINE="$CRON_SCHEDULE $SCRIPT_PATH watchdog >/dev/null 2>&1"
    CRON_COMMENT="# NASBot watchdog - riavvia automaticamente se non attivo"
    
    # Rimuovi eventuali entry esistenti
    crontab -l 2>/dev/null | grep -v "nasbot\|NASBot\|start_bot.sh" > /tmp/crontab_new || true
    
    # Aggiungi nuovo cron
    echo "" >> /tmp/crontab_new
    echo "$CRON_COMMENT" >> /tmp/crontab_new
    echo "$CRON_LINE" >> /tmp/crontab_new
    
    # Aggiungi anche avvio al reboot
    echo "# NASBot avvio al boot" >> /tmp/crontab_new
    echo "@reboot sleep 60 && $SCRIPT_PATH start >/dev/null 2>&1" >> /tmp/crontab_new
    
    crontab /tmp/crontab_new
    rm /tmp/crontab_new
    
    echo -e "${GREEN}✅ Cron job configurato${NC}"
}

# ─── METODO 2: rc.local (fallback) ──────────────────
setup_rclocal() {
    echo "🔧 Configurazione rc.local (fallback)..."
    
    RC_LOCAL="/etc/rc.local"
    RC_LINE="$SCRIPT_PATH start &"
    
    # Crea rc.local se non esiste
    if [ ! -f "$RC_LOCAL" ]; then
        echo "#!/bin/bash" > "$RC_LOCAL"
        echo "# rc.local - eseguito al boot" >> "$RC_LOCAL"
        echo "" >> "$RC_LOCAL"
        echo "exit 0" >> "$RC_LOCAL"
        chmod +x "$RC_LOCAL"
    fi
    
    # Verifica se già presente
    if grep -q "start_bot.sh\|nasbot" "$RC_LOCAL" 2>/dev/null; then
        echo "   ℹ️  Entry già presente in rc.local"
    else
        # Inserisci prima di "exit 0"
        sed -i "/^exit 0/i # NASBot autostart\nsleep 60\n$RC_LINE\n" "$RC_LOCAL" 2>/dev/null || {
            # Se sed non funziona, aggiungi in fondo
            echo "" >> "$RC_LOCAL"
            echo "# NASBot autostart" >> "$RC_LOCAL"
            echo "sleep 60" >> "$RC_LOCAL"
            echo "$RC_LINE" >> "$RC_LOCAL"
        }
        echo -e "${GREEN}✅ rc.local configurato${NC}"
    fi
}

# ─── METODO 3: init.d (TerraMaster) ─────────────────
setup_initd() {
    echo "🔧 Configurazione init.d (TerraMaster)..."
    
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
    
    # Abilita su sistemi con update-rc.d
    if command -v update-rc.d >/dev/null 2>&1; then
        update-rc.d nasbot defaults 2>/dev/null || true
    fi
    
    # Abilita su sistemi con chkconfig
    if command -v chkconfig >/dev/null 2>&1; then
        chkconfig --add nasbot 2>/dev/null || true
        chkconfig nasbot on 2>/dev/null || true
    fi
    
    echo -e "${GREEN}✅ init.d script creato${NC}"
}

# ─── ESEGUI SETUP ───────────────────────────────────
echo "Applicando configurazioni..."
echo ""

# Prova tutti i metodi per massima compatibilità
setup_cron
echo ""

# Se esiste /etc/init.d, usa anche quello
if [ -d "/etc/init.d" ]; then
    setup_initd
    echo ""
fi

# Se esiste rc.local, configuralo come backup
if [ -f "/etc/rc.local" ] || [ -d "/etc" ]; then
    setup_rclocal
    echo ""
fi

# ─── AVVIA SUBITO ───────────────────────────────────
echo "🚀 Avvio immediato del bot..."
$SCRIPT_PATH start

echo ""
echo "═══════════════════════════════════════════════════════════"
echo -e "${GREEN}✅ Setup completato!${NC}"
echo "═══════════════════════════════════════════════════════════"
echo ""
echo "Il bot ora:"
echo "  • Si avvia automaticamente al boot (dopo 60 secondi)"
echo "  • Viene controllato ogni 5 minuti dal watchdog"
echo "  • Si riavvia automaticamente se crasha"
echo ""
echo "Comandi utili:"
echo "  $SCRIPT_PATH status    - Verifica stato"
echo "  $SCRIPT_PATH logs      - Vedi log"
echo "  crontab -l              - Vedi cron jobs"
echo ""
