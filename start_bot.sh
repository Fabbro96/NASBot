#!/usr/bin/env bash
set -u -o pipefail

BOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
BOT_NAME="nasbot"
UPDATE_FILE="nasbot-arm64"
LOG_FILE="$BOT_DIR/nasbot.log"
PID_FILE="$BOT_DIR/nasbot.pid"
MAX_LOG_SIZE=$((10 * 1024 * 1024))

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

cd "$BOT_DIR" || exit 1

print_header() {
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
	echo -e "${BLUE}  🤖 NASBot Manager${NC}"
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

ensure_binary_permissions() {
	if [[ -f "$BOT_NAME" ]]; then
		chmod +x "$BOT_NAME" 2>/dev/null || true
	fi
}

get_file_size() {
	local path="$1"
	if stat -c%s "$path" >/dev/null 2>&1; then
		stat -c%s "$path"
		return
	fi
	if stat -f%z "$path" >/dev/null 2>&1; then
		stat -f%z "$path"
		return
	fi
	echo 0
}

rotate_logs() {
	if [[ -f "$LOG_FILE" ]]; then
		local size
		size=$(get_file_size "$LOG_FILE")
		if [[ "$size" -gt "$MAX_LOG_SIZE" ]]; then
			mv "$LOG_FILE" "$LOG_FILE.old"
			echo "[$(date '+%Y-%m-%d %H:%M:%S')] Log rotated" >"$LOG_FILE"
		fi
	fi
}

is_running() {
	if [[ -f "$PID_FILE" ]]; then
		local pid
		pid=$(cat "$PID_FILE" 2>/dev/null || true)
		if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
			return 0
		fi
	fi
	pgrep -x "$BOT_NAME" >/dev/null 2>&1
}

get_pid() {
	if [[ -f "$PID_FILE" ]]; then
		cat "$PID_FILE" 2>/dev/null || true
	else
		pgrep -x "$BOT_NAME" 2>/dev/null || true
	fi
}

start_bot() {
	if is_running; then
		echo "⚠️  Bot already running (PID: $(get_pid))"
		return 1
	fi

	if [[ ! -x "$BOT_NAME" ]]; then
		echo -e "${RED}❌ Binary '$BOT_NAME' not found or not executable${NC}"
		return 1
	fi

	rotate_logs
	echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting NASBot..." >>"$LOG_FILE"
	nohup "./$BOT_NAME" >>"$LOG_FILE" 2>&1 &
	echo $! >"$PID_FILE"

	sleep 2
	if is_running; then
		echo "✅ Bot started (PID: $(get_pid))"
		return 0
	fi

	echo "❌ Error starting bot. Check $LOG_FILE"
	return 1
}

stop_bot() {
	if ! is_running; then
		echo "ℹ️  Bot not running"
		rm -f "$PID_FILE"
		return 0
	fi

	local pid
	pid=$(get_pid)
	echo "⏳ Stopping bot (PID: $pid)..."
	kill -TERM "$pid" 2>/dev/null || true

	for _ in $(seq 1 10); do
		if ! is_running; then
			echo "✅ Bot stopped"
			rm -f "$PID_FILE"
			return 0
		fi
		sleep 1
	done

	kill -9 "$pid" 2>/dev/null || true
	pkill -9 -x "$BOT_NAME" 2>/dev/null || true
	rm -f "$PID_FILE"
	echo "⚠️  Bot forcibly terminated"
}

restart_bot() {
	echo "🔄 Restarting bot..."
	stop_bot
	sleep 2
	start_bot
}

watchdog() {
	if ! is_running; then
		echo "[$(date '+%Y-%m-%d %H:%M:%S')] WATCHDOG: Bot not running, restarting..." >>"$LOG_FILE"
		start_bot
	fi
}

show_logs() {
	local lines="${1:-50}"
	if [[ ! -f "$LOG_FILE" ]]; then
		echo -e "${RED}❌ Log file not found${NC}"
		return 1
	fi

	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
	echo -e "${BLUE}  Last $lines logs${NC}"
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
	tail -n "$lines" "$LOG_FILE"
}

status_bot() {
	print_header
	if is_running; then
		local pid
		pid=$(get_pid)
		echo -e "${GREEN}🟢 Status: ACTIVE${NC}"
		echo "📋 PID: $pid"

		if command -v ps >/dev/null 2>&1; then
			local etime rss
			etime=$(ps -o etime= -p "$pid" 2>/dev/null | xargs || true)
			rss=$(ps -o rss= -p "$pid" 2>/dev/null | awk '{printf "%.1fMB", $1/1024}' || true)
			[[ -n "$etime" ]] && echo "⏱️  Uptime: $etime"
			[[ -n "$rss" ]] && echo "💾 Memory: $rss"
		fi
	else
		echo -e "${RED}🔴 Status: INACTIVE${NC}"
	fi

	echo -e "${BLUE}───────────────────────────────────────${NC}"
	echo "📁 Directory: $BOT_DIR"
	echo "📝 Log: $LOG_FILE"
	if [[ -f "$LOG_FILE" ]]; then
		echo "📊 Log size: $(ls -lh "$LOG_FILE" | awk '{print $5}')"
	fi
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

check_updates() {
	if [[ ! -f "$UPDATE_FILE" ]]; then
		return
	fi

	echo -e "${YELLOW}🔄 Update detected: $UPDATE_FILE${NC}"
	if is_running; then
		echo "   Stopping running instance for update..."
		stop_bot
	fi

	if [[ -f "$BOT_NAME" ]]; then
		mv "$BOT_NAME" "${BOT_NAME}.bak"
	fi

	mv "$UPDATE_FILE" "$BOT_NAME"
	chmod +x "$BOT_NAME"

	if [[ ! -x "$BOT_NAME" ]]; then
		echo -e "${RED}❌ Update failed: binary is not executable${NC}"
		if [[ -f "${BOT_NAME}.bak" ]]; then
			mv "${BOT_NAME}.bak" "$BOT_NAME"
		fi
		return 1
	fi

	rm -f "${BOT_NAME}.bak"
	echo -e "${GREEN}✅ Binary updated.${NC}"
}

apply_system_tweaks() {
	if [[ "$EUID" -ne 0 ]]; then
		return
	fi

	local panic panic_oops
	panic=$(cat /proc/sys/kernel/panic 2>/dev/null || true)
	panic_oops=$(cat /proc/sys/kernel/panic_on_oops 2>/dev/null || true)

	if [[ "$panic" == "10" && "$panic_oops" == "1" ]]; then
		return
	fi

	echo -e "${YELLOW}⚙️  Applying Kernel Panic auto-reboot settings...${NC}"
	sysctl -w kernel.panic=10 >/dev/null 2>&1 || true
	sysctl -w kernel.panic_on_oops=1 >/dev/null 2>&1 || true

	if [[ -d "/etc/sysctl.d" ]]; then
		{
			echo "kernel.panic = 10"
			echo "kernel.panic_on_oops = 1"
		} >/etc/sysctl.d/99-nasbot-panic.conf
	fi
}

install_persistence() {
	if ! command -v crontab >/dev/null 2>&1; then
		echo -e "${RED}❌ crontab command not available${NC}"
		return 1
	fi

	local script_path cron_job
	script_path="$BOT_DIR/start_bot.sh"
	cron_job="*/5 * * * * $script_path watchdog"

	if crontab -l 2>/dev/null | grep -Fq "$script_path watchdog"; then
		echo -e "${GREEN}✅ Autostart (Cron) already configured.${NC}"
	else
		echo -e "${YELLOW}⚙️  Configuring Autostart (Cron)...${NC}"
		(crontab -l 2>/dev/null; echo "$cron_job") | crontab -
		echo -e "${GREEN}✅ Autostart enabled (runs every 5 mins).${NC}"
	fi
}

usage() {
	print_header
	echo "Usage: $0 {start|stop|restart|status|watchdog|logs [n]|install}"
	echo
	echo "  start     - Start the bot"
	echo "  stop      - Stop the bot"
	echo "  restart   - Restart the bot"
	echo "  status    - Show detailed status"
	echo "  watchdog  - Restart if inactive (for cron)"
	echo "  logs [n]  - Show last n logs (default: 50)"
	echo "  install   - Setup persistence and kernel tweaks"
}

ensure_binary_permissions
check_updates
apply_system_tweaks

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
install)
	install_persistence
	apply_system_tweaks
	start_bot
	;;
*)
	usage
	;;
esac
