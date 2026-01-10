#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot Auto-Recovery Setup — Per sistemi senza systemd
#  Alternativa usando cron per TerraMaster e altri NAS
# ═══════════════════════════════════════════════════════════════════
#
#  Questo script configura un watchdog via cron che:
#  - Controlla ogni minuto se il bot è attivo
#  - Lo riavvia automaticamente se crashato
#  - Avvia il bot automaticamente al boot del NAS
#
#  USO: sudo ./setup_autostart.sh
#
# ═══════════════════════════════════════════════════════════════════

set -e

BOT_DIR="/Volume1/public"
SCRIPT_PATH="$BOT_DIR/start_bot.sh"
CRON_FILE="/etc/cron.d/nasbot"

echo "═══════════════════════════════════════"
echo "  NASBot Auto-Recovery Setup"
echo "═══════════════════════════════════════"
echo ""

# Verifica esistenza script
if [ ! -f "$SCRIPT_PATH" ]; then
    echo "❌ Errore: $SCRIPT_PATH non trovato!"
    exit 1
fi

# Rendi eseguibile
chmod +x "$SCRIPT_PATH"
chmod +x "$BOT_DIR/nasbot" 2>/dev/null || true

echo "📝 Configurazione cron watchdog..."

# Crea cron job
cat > "$CRON_FILE" << 'EOF'
# NASBot Watchdog - Controlla ogni minuto e riavvia se necessario
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin

# Watchdog: controlla ogni minuto
* * * * * root /Volume1/public/start_bot.sh watchdog >/dev/null 2>&1

# Avvio al boot (dopo 60 secondi per permettere ai servizi di avviarsi)
@reboot root sleep 60 && /Volume1/public/start_bot.sh start >/dev/null 2>&1
EOF

# Permessi corretti per cron
chmod 644 "$CRON_FILE"

echo "✅ Cron job creato: $CRON_FILE"
echo ""

# Verifica se cron è attivo
if command -v systemctl >/dev/null 2>&1; then
    systemctl restart cron 2>/dev/null || systemctl restart crond 2>/dev/null || true
elif command -v service >/dev/null 2>&1; then
    service cron restart 2>/dev/null || service crond restart 2>/dev/null || true
fi

echo "📋 Configurazione completata!"
echo ""
echo "Il bot verrà:"
echo "  ✓ Avviato automaticamente al boot (dopo 60 sec)"
echo "  ✓ Riavviato automaticamente se crasha"
echo "  ✓ Controllato ogni minuto dal watchdog"
echo ""
echo "═══════════════════════════════════════"
echo "  Comandi utili:"
echo "═══════════════════════════════════════"
echo "  $SCRIPT_PATH start    - Avvia"
echo "  $SCRIPT_PATH stop     - Ferma"
echo "  $SCRIPT_PATH status   - Stato"
echo "  $SCRIPT_PATH logs     - Vedi log"
echo ""

# Avvia subito il bot
echo "🚀 Avvio bot..."
"$SCRIPT_PATH" start
