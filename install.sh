#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  NASBot - One-Click Installer
#  Installa e configura tutto automaticamente
# ═══════════════════════════════════════════════════════════════════
#
#  Cosa fa questo script:
#  1. Verifica i file necessari
#  2. Rende eseguibili i binari
#  3. Configura il kernel panic auto-reboot
#  4. Configura l'autostart al boot
#  5. Avvia il bot
#
#  Uso: sudo ./install.sh
#
# ═══════════════════════════════════════════════════════════════════

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Detect directory
BOT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${BLUE}  🤖 NASBot - One-Click Installer${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo

# ─── Step 0: Check root ─────────────────────────────
if [ "$EUID" -ne 0 ]; then
    echo -e "${RED}❌ Questo script deve essere eseguito come root${NC}"
    echo "   Usa: sudo $0"
    exit 1
fi

# ─── Step 1: Verify files ───────────────────────────
echo -e "${YELLOW}[1/5]${NC} Verifica file..."

REQUIRED_FILES=("config.json")
BINARY_NAME=""

# Find the binary (nasbot or nasbot-arm64)
if [ -f "$BOT_DIR/nasbot-arm64" ]; then
    BINARY_NAME="nasbot-arm64"
elif [ -f "$BOT_DIR/nasbot" ]; then
    BINARY_NAME="nasbot"
else
    echo -e "${RED}❌ Binary non trovato (nasbot o nasbot-arm64)${NC}"
    exit 1
fi

for file in "${REQUIRED_FILES[@]}"; do
    if [ ! -f "$BOT_DIR/$file" ]; then
        echo -e "${RED}❌ File mancante: $file${NC}"
        exit 1
    fi
done

echo -e "${GREEN}   ✓ Binary: $BINARY_NAME${NC}"
echo -e "${GREEN}   ✓ Config: config.json${NC}"

# ─── Step 2: Make executables ───────────────────────
echo -e "${YELLOW}[2/5]${NC} Imposto permessi..."

chmod +x "$BOT_DIR/$BINARY_NAME"
chmod +x "$BOT_DIR/start_bot.sh" 2>/dev/null || true
chmod +x "$BOT_DIR/setup_autostart.sh" 2>/dev/null || true
chmod +x "$BOT_DIR/setup_kernel_panic.sh" 2>/dev/null || true

# Create symlink if needed (nasbot-arm64 -> nasbot)
if [ "$BINARY_NAME" = "nasbot-arm64" ] && [ ! -f "$BOT_DIR/nasbot" ]; then
    ln -sf "$BOT_DIR/nasbot-arm64" "$BOT_DIR/nasbot"
    echo -e "${GREEN}   ✓ Creato link: nasbot -> nasbot-arm64${NC}"
fi

echo -e "${GREEN}   ✓ Permessi impostati${NC}"

# ─── Step 3: Kernel panic auto-reboot ───────────────
echo -e "${YELLOW}[3/5]${NC} Configuro kernel panic auto-reboot..."

PANIC_TIMEOUT=10
CURRENT_PANIC=$(cat /proc/sys/kernel/panic 2>/dev/null || echo "0")

if [ "$CURRENT_PANIC" -gt 0 ]; then
    echo -e "${GREEN}   ✓ Già configurato (timeout: ${CURRENT_PANIC}s)${NC}"
else
    # Apply immediately
    echo $PANIC_TIMEOUT > /proc/sys/kernel/panic
    echo 1 > /proc/sys/kernel/panic_on_oops
    
    # Make persistent
    SYSCTL_FILE="/etc/sysctl.d/99-kernel-panic.conf"
    cat > "$SYSCTL_FILE" << EOF
# NASBot - Auto-reboot after kernel panic
kernel.panic = $PANIC_TIMEOUT
kernel.panic_on_oops = 1
EOF
    echo -e "${GREEN}   ✓ Kernel panic: reboot automatico dopo ${PANIC_TIMEOUT}s${NC}"
fi

# ─── Step 4: Setup autostart ────────────────────────
echo -e "${YELLOW}[4/5]${NC} Configuro autostart..."

# Create start_bot.sh if not exists or update it
SCRIPT_PATH="$BOT_DIR/start_bot.sh"
CRON_ENTRY="*/5 * * * * $SCRIPT_PATH watchdog >> /dev/null 2>&1"

# Check if cron entry exists
if crontab -l 2>/dev/null | grep -q "$SCRIPT_PATH"; then
    echo -e "${GREEN}   ✓ Cron già configurato${NC}"
else
    # Add cron entry
    (crontab -l 2>/dev/null || true; echo "$CRON_ENTRY") | crontab -
    echo -e "${GREEN}   ✓ Cron aggiunto (watchdog ogni 5 min)${NC}"
fi

# Add to rc.local if exists
if [ -f /etc/rc.local ]; then
    if ! grep -q "$SCRIPT_PATH" /etc/rc.local; then
        sed -i "/^exit 0/i $SCRIPT_PATH start &" /etc/rc.local 2>/dev/null || true
        echo -e "${GREEN}   ✓ Aggiunto a /etc/rc.local${NC}"
    fi
fi

# ─── Step 5: Start bot ──────────────────────────────
echo -e "${YELLOW}[5/5]${NC} Avvio NASBot..."

# Stop if running
pkill -x nasbot 2>/dev/null || true
pkill -x nasbot-arm64 2>/dev/null || true
sleep 1

# Start
cd "$BOT_DIR"
nohup "./$BINARY_NAME" >> "$BOT_DIR/nasbot.log" 2>&1 &
NEW_PID=$!
echo $NEW_PID > "$BOT_DIR/nasbot.pid"

sleep 2

if kill -0 $NEW_PID 2>/dev/null; then
    echo -e "${GREEN}   ✓ NASBot avviato (PID: $NEW_PID)${NC}"
else
    echo -e "${RED}   ❌ Errore avvio - controlla nasbot.log${NC}"
    exit 1
fi

# ─── Done ───────────────────────────────────────────
echo
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo -e "${GREEN}  ✅ Installazione completata!${NC}"
echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
echo
echo "  📁 Directory: $BOT_DIR"
echo "  🤖 Binary:    $BINARY_NAME"
echo "  📋 Log:       $BOT_DIR/nasbot.log"
echo "  🔄 Autostart: Cron ogni 5 minuti"
echo "  💥 Kernel:    Auto-reboot dopo panic"
echo
echo -e "${YELLOW}Comandi utili:${NC}"
echo "  ./start_bot.sh status   - Stato del bot"
echo "  ./start_bot.sh restart  - Riavvia"
echo "  ./start_bot.sh logs     - Mostra log"
echo "  ./start_bot.sh stop     - Ferma"
echo
