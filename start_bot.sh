#!/bin/bash
# NASBot Launcher — start_bot.sh

# ─── CONFIG ─────────────────────────────────────────
export BOT_TOKEN=""
export BOT_USER_ID=""
BOT_DIR="/Volume1/public"
# ────────────────────────────────────────────────────

cd "$BOT_DIR" || exit 1

case "${1:-}" in
  stop)
    pkill -x nasbot 2>/dev/null && echo "Bot fermato." || echo "Bot non attivo."
    ;;
  status)
    pgrep -x nasbot >/dev/null && echo "Bot attivo (PID $(pgrep -x nasbot))" || echo "Bot non attivo"
    ;;
  restart)
    pkill -x nasbot 2>/dev/null
    sleep 1
    nohup ./nasbot >> nasbot.log 2>&1 &
    echo "Bot riavviato."
    ;;
  *)
    # Default: start
    if pgrep -x nasbot >/dev/null; then
      echo "Bot già attivo."
    else
      nohup ./nasbot >> nasbot.log 2>&1 &
      echo "Bot avviato."
    fi
    ;;
esac
