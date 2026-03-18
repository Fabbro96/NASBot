#!/usr/bin/env bash
set -u -o pipefail

# Source common utilities (if available in same directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
BOT_DIR="${SCRIPT_DIR}"
if [[ -f "${SCRIPT_DIR}/common.sh" ]]; then
	NASBOT_NO_AUTO_LOAD=1 source "${SCRIPT_DIR}/common.sh"
fi

# Configuration (with environment variable overrides)
BOT_NAME="${NASBOT_APP_NAME:-nasbot}"
BOT_BINARY="${NASBOT_BINARY_PATH:-${BOT_DIR}/${BOT_NAME}}"
LOG_FILE="${NASBOT_LOG_FILE:-${BOT_DIR}/nasbot.log}"
PID_FILE="${NASBOT_PID_FILE:-${BOT_DIR}/nasbot.pid}"
STATE_FILE="${NASBOT_STATE_FILE:-${BOT_DIR}/nasbot_state.json}"
UPDATE_FILE_PATTERN="${NASBOT_UPDATE_FILE_PATTERN:-nasbot-update*}"
MAX_LOG_SIZE_MB="${NASBOT_LOG_MAX_SIZE_MB:-10}"
MAX_LOG_SIZE=$((MAX_LOG_SIZE_MB * 1024 * 1024))
AUTO_RESTART_ON_UPDATE="${NASBOT_AUTO_RESTART_ON_UPDATE:-true}"
ULIMIT_N="${NASBOT_ULIMIT_N:-}"
VERBOSE="${NASBOT_VERBOSE:-false}"
DEBUG_MODE="${NASBOT_DEBUG:-false}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

cd "$BOT_DIR" || exit 1

print_header() {
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
	echo -e "${BLUE}  🤖 NASBot Runtime Manager${NC}"
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

ensure_binary_permissions() {
	if [[ -f "$BOT_BINARY" ]]; then
		chmod +x "$BOT_BINARY" 2>/dev/null || true
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

	if [[ ! -x "$BOT_BINARY" ]]; then
		echo -e "${RED}❌ Binary '$BOT_BINARY' not found or not executable${NC}"
		return 1
	fi

	rotate_logs
	echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting NASBot..." >>"$LOG_FILE"
	NASBOT_LOG_FILE="$LOG_FILE" \
		NASBOT_PID_FILE="$PID_FILE" \
		NASBOT_STATE_FILE="$STATE_FILE" \
		nohup "$BOT_BINARY" >>"$LOG_FILE" 2>&1 &
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
	echo "⚙️  Binary: $BOT_BINARY"
	echo "📝 Log: $LOG_FILE"
	if [[ -f "$LOG_FILE" ]]; then
		echo "📊 Log size: $(ls -lh "$LOG_FILE" | awk '{print $5}')"
	fi
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

check_updates() {
	local update_file=""
	# Search for update files matching the pattern
	for candidate in ${BOT_DIR}/${UPDATE_FILE_PATTERN}; do
		if [[ -f "${candidate}" ]]; then
			update_file="${candidate}"
			break
		fi
	done
	if [[ -z "${update_file}" ]]; then
		return
	fi

	echo -e "${YELLOW}🔄 Update detected: $update_file${NC}"
	if is_running; then
		echo "   Stopping running instance for update..."
		stop_bot
	fi

	if [[ -f "$BOT_BINARY" ]]; then
		mv "$BOT_BINARY" "${BOT_BINARY}.bak"
	fi

	mv "$update_file" "$BOT_BINARY"
	chmod +x "$BOT_BINARY"

	if [[ ! -x "$BOT_BINARY" ]]; then
		echo -e "${RED}❌ Update failed: binary is not executable${NC}"
		if [[ -f "${BOT_BINARY}.bak" ]]; then
			mv "${BOT_BINARY}.bak" "$BOT_BINARY"
		fi
		return
	fi

	rm -f "${BOT_BINARY}.bak"
	echo -e "${GREEN}✅ Binary updated.${NC}"
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
	cat <<EOF

COMMANDS:
  start              Start the bot daemon
  stop               Stop the bot gracefully  
  restart            Restart the bot
  status             Show detailed status and metrics
  watchdog           Restart if not running (for cron)
  logs [N]           Show last N lines of logs (default: 50)
  watch              Watch logs in real-time (tail -f)
  config [init]      Initialize or show configuration
  install            Setup auto-restart via cron

OPTIONS (global):
  --help, -h         Show this help message
  --verbose          Enable verbose output

EXAMPLES:
  \${0##*/} start              # Start bot
  \${0##*/} watch              # Monitor logs live
  \${0##*/} config init        # Create config from template
  \${0##*/} logs 100           # Show last 100 lines
  \${0##*/} --verbose restart  # Restart with verbose output

CONFIGURATION (via environment variables):
  NASBOT_LOG_FILE        Log file path (default: $LOG_FILE)
  NASBOT_PID_FILE        PID file path (default: $PID_FILE)
  NASBOT_STATE_FILE      State file path (default: $STATE_FILE)
  NASBOT_APP_NAME        Application name (default: $BOT_NAME)
  NASBOT_DEBUG           Enable debug mode (default: false)
  NASBOT_VERBOSE         Enable verbose logging (default: false)

For custom configuration, copy nasbot.config.template to nasbot.config.local
and modify values as needed.

EOF
}

watch_logs() {
	if [[ ! -f "$LOG_FILE" ]]; then
		echo -e "${RED}❌ Log file not found${NC}"
		return 1
	fi
	
	echo -e "${BLUE}Watching logs (press Ctrl+C to exit)...${NC}"
	tail -f "$LOG_FILE"
}

init_config() {
	local config_template="config.example.json"
	local config_file="config.json"
	
	if [[ ! -f "$config_template" ]]; then
		echo -e "${RED}❌ Config template not found: $config_template${NC}"
		return 1
	fi
	
	if [[ -f "$config_file" ]]; then
		echo -e "${YELLOW}⚠️  Config already exists: $config_file${NC}"
		read -p "Overwrite? (y/N) " -n 1 -r; echo
		if [[ ! "$REPLY" =~ ^[Yy]$ ]]; then
			echo "Cancelled."
			return 0
		fi
	fi
	
	cp "$config_template" "$config_file"
	chmod 600 "$config_file"
	echo -e "${GREEN}✅ Config created: $config_file${NC}"
	echo "📝 Edit with your settings before starting the bot."
	
	if command -v nano >/dev/null 2>&1; then
		read -p "Edit now? (y/N) " -n 1 -r; echo
		if [[ "$REPLY" =~ ^[Yy]$ ]]; then
			nano "$config_file"
		fi
	fi
}

show_config() {
	print_header
	echo
	echo "📋 Configuration:"
	echo "  Bot Name:       $BOT_NAME"
	echo "  Binary:         $BOT_BINARY"
	echo "  Log File:       $LOG_FILE"
	echo "  PID File:       $PID_FILE"
	echo "  State File:     $STATE_FILE"
	echo "  Max Log Size:   $MAX_LOG_SIZE_MB MB"
	echo "  Verbose:        $VERBOSE"
	echo "  Debug Mode:     $DEBUG_MODE"
	echo
}

ensure_binary_permissions
check_updates

# Parse global options
while [[ $# -gt 0 ]]; do
	case "$1" in
	--verbose)
		VERBOSE="true"
		shift
		;;
	--help|-h)
		usage
		exit 0
		;;
	*)
		break
		;;
	esac
done

case "${1:-status}" in
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
watch)
	watch_logs
	;;
config)
	case "${2:-show}" in
	init)
		init_config
		;;
	*)
		show_config
		;;
	esac
	;;
install)
	install_persistence
	start_bot
	;;
*)
	usage
	exit 1
	;;
*)
	usage
	;;
esac
