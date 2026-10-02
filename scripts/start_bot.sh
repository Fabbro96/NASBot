#!/usr/bin/env bash
set -u -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
BOT_DIR="$(cd "$SCRIPT_DIR/.." >/dev/null 2>&1 && pwd)"
BIN_DIR="$BOT_DIR/bin"
VAR_DIR="$BOT_DIR/var"
BOT_NAME="nasbot"
BOT_BINARY="$BIN_DIR/$BOT_NAME"
UPDATE_FILE="$BIN_DIR/nasbot-update"
LOG_FILE="$VAR_DIR/nasbot.log"
PID_FILE="$VAR_DIR/nasbot.pid"
STATE_FILE="$VAR_DIR/nasbot_state.json"
MAX_LOG_SIZE=$((10 * 1024 * 1024))
DRY_RUN="${NASBOT_DRY_RUN:-false}"
ASSUME_YES="${NASBOT_ASSUME_YES:-false}"
COMMAND=""

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

cd "$BOT_DIR" || exit 1

init_dirs() {
	mkdir -p "$BIN_DIR" "$VAR_DIR"
}

# Architettura logica dell'host, con la stessa nomenclatura dei suffissi dei
# binari (amd64/arm64/arm/386). `uname -m` restituisce invece i nomi del kernel
# (x86_64, aarch64, armv7l, i686): senza questa normalizzazione il filtro sui
# candidati non abboccerebbe mai.
host_arch() {
	local machine
	machine=$(uname -m 2>/dev/null || echo unknown)
	case "$machine" in
	x86_64 | amd64) echo "amd64" ;;
	aarch64 | arm64) echo "arm64" ;;
	armv7l | armv7 | armv6l | arm) echo "arm" ;;
	i386 | i486 | i586 | i686) echo "386" ;;
	*) echo "$machine" ;;
	esac
}

# Un candidato senza suffisso di architettura (es. bin/nasbot-update) è
# accettato così com'è; un candidato con suffisso viene accettato solo se
# combacia con l'host. Senza questo filtro, dopo una build multi-architettura
# un `status` potrebbe sostituire il bot col binario dell'altra architettura,
# che sul NAS non gira.
candidate_matches_host() {
	local candidate="$1"
	local base suffix host
	base=$(basename "$candidate")
	host=$(host_arch)
	suffix=""

	case "$base" in
	*-arm64) suffix="arm64" ;;
	*-amd64) suffix="amd64" ;;
	*-armv7) suffix="armv7" ;;
	*-386) suffix="386" ;;
	*-arm) suffix="arm" ;;
	esac

	[[ -z "$suffix" ]] && return 0
	[[ "$suffix" == "$host" ]] && return 0
	# I binari a 32 bit si dichiarano con due suffissi equivalenti.
	[[ "$suffix" == "armv7" && "$host" == "arm" ]] && return 0
	return 1
}

# Conferma esplicita. Fuori da un terminale (cron, systemd, CI) la risposta è
# "no": un comando non interattivo non deve poter modificare l'host di nascosto.
confirm() {
	local prompt="$1"
	local reply=""

	if [[ "$ASSUME_YES" == "true" ]]; then
		echo "   [--yes: conferma automatica]"
		return 0
	fi
	if [[ ! -t 0 ]]; then
		echo -e "${YELLOW}   Nessun terminale: usa --yes (o NASBOT_ASSUME_YES=true) per procedere.${NC}"
		return 1
	fi

	read -r -p "   ${prompt} [y/N] " reply || true
	[[ "$reply" =~ ^[Yy]([Ee][Ss])?$ ]]
}

migrate_legacy_layout() {
	if [[ -x "$BOT_DIR/$BOT_NAME" && ! -e "$BOT_BINARY" ]]; then
		mv "$BOT_DIR/$BOT_NAME" "$BOT_BINARY"
	fi

	if [[ -f "$BOT_DIR/nasbot.log" && ! -f "$LOG_FILE" ]]; then
		mv "$BOT_DIR/nasbot.log" "$LOG_FILE"
	fi

	if [[ -f "$BOT_DIR/nasbot.pid" && ! -f "$PID_FILE" ]]; then
		mv "$BOT_DIR/nasbot.pid" "$PID_FILE"
	fi

	if [[ -f "$BOT_DIR/nasbot_state.json" && ! -f "$STATE_FILE" ]]; then
		mv "$BOT_DIR/nasbot_state.json" "$STATE_FILE"
	fi
}

print_header() {
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
	echo -e "${BLUE}  🤖 NASBot Manager${NC}"
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

ensure_binary_permissions() {
	if [[ -f "$BOT_BINARY" ]]; then
		chmod +x "$BOT_BINARY" 2>/dev/null || true
	fi
}

get_file_size() {
	local path="$1"
	local size
	if size=$(stat -c%s "$path" 2>/dev/null); then
		echo "$size"
		return
	fi
	if size=$(stat -f%z "$path" 2>/dev/null); then
		echo "$size"
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
	if [[ "$DRY_RUN" == "true" ]]; then
		echo -e "${BLUE}🧪 [dry-run] Avvierebbe ${BOT_BINARY}${NC}"
		if is_running; then
			echo -e "${BLUE}🧪 [dry-run] Il bot (PID: $(get_pid)) è già in esecuzione${NC}"
		else
			echo -e "${BLUE}🧪 [dry-run] Il bot non è in esecuzione${NC}"
		fi
		return 0
	fi

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
	
	export NASBOT_LOG_FILE="$LOG_FILE"
	export NASBOT_PID_FILE="$PID_FILE"
	export NASBOT_STATE_FILE="$STATE_FILE"
	
	nohup "$BOT_BINARY" >>"$LOG_FILE" 2>&1 &
	local bg_pid=$!
	disown 2>/dev/null || true
	echo "$bg_pid" >"$PID_FILE"

	sleep 2
	if is_running; then
		echo "✅ Bot started (PID: $(get_pid))"
		return 0
	fi

	echo "❌ Error starting bot. Check $LOG_FILE"
	return 1
}

stop_bot() {
	if [[ "$DRY_RUN" == "true" ]]; then
		if is_running; then
			echo -e "${BLUE}🧪 [dry-run] Fermerebbe il bot (PID: $(get_pid)) e rimuoverebbe ${PID_FILE}${NC}"
		else
			echo -e "${BLUE}🧪 [dry-run] Il bot non è in esecuzione: nessuna azione${NC}"
		fi
		return 0
	fi

	if ! is_running; then
		echo "ℹ️  Bot not running"
		rm -f "$PID_FILE"
		return 0
	fi

	local pid
	pid=$(get_pid)
	echo "⏳ Stopping bot (PID: $pid)..."
	kill -TERM "$pid" 2>/dev/null || true

	for ((i=1; i<=10; i++)); do
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
		echo "📊 Log size: $(du -sh "$LOG_FILE" | awk '{print $1}')"
	fi
	echo -e "${BLUE}═══════════════════════════════════════════════════════════════${NC}"
}

check_updates() {
	local update_file=""
	local candidates=(
		"$UPDATE_FILE"
		"$BIN_DIR/nasbot-update-arm64"
		"$BIN_DIR/nasbot-update-amd64"
		"$BIN_DIR/nasbot-arm64"
		"$BIN_DIR/nasbot-amd64"
		"$BOT_DIR/nasbot-update"
		"$BOT_DIR/nasbot-update-arm64"
		"$BOT_DIR/nasbot-update-amd64"
		"$BOT_DIR/nasbot-arm64"
		"$BOT_DIR/nasbot-amd64"
		"$SCRIPT_DIR/nasbot-update"
		"$SCRIPT_DIR/nasbot-update-arm64"
		"$SCRIPT_DIR/nasbot-update-amd64"
		"$SCRIPT_DIR/nasbot-arm64"
		"$SCRIPT_DIR/nasbot-amd64"
	)

	for candidate in "${candidates[@]}"; do
		if [[ ! -f "$candidate" || "$candidate" == "$BOT_BINARY" ]]; then
			continue
		fi
		# Non sostituire mai il binario con uno compilato per un'altra
		# architettura: sul NAS semplicemente non gira, e il bot si ferma.
		if ! candidate_matches_host "$candidate"; then
			echo -e "${YELLOW}⚠️  Candidato ignorato (architettura diversa dall'host $(host_arch)): $candidate${NC}"
			continue
		fi
		update_file="$candidate"
		break
	done

	if [[ -z "$update_file" ]]; then
		return
	fi

	echo -e "${YELLOW}🔄 Update detected: $update_file${NC}"
	if [[ "$DRY_RUN" == "true" ]]; then
		echo -e "${BLUE}🧪 [dry-run] Sostituirebbe ${BOT_BINARY} con ${update_file}${NC}"
		if is_running; then
			echo -e "${BLUE}🧪 [dry-run] Fermerebbe prima il bot (PID: $(get_pid)) e lo riavvierebbe${NC}"
		else
			echo -e "${BLUE}🧪 [dry-run] Il bot non è in esecuzione${NC}"
		fi
		return 0
	fi

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

# Modifica il comportamento di RIAVVIO AUTOMATICO della macchina:
#   /etc/sysctl.d/99-nasbot-panic.conf  kernel.panic = 10
#                                       kernel.panic_on_oops = 1
# Non è una modifica innocua e non è reversibile da qui: per questo si chiama
# solo da `install`, mai da status/logs/stop, e solo dietro conferma.
apply_system_tweaks() {
	if [[ "$EUID" -ne 0 ]]; then
		echo -e "${YELLOW}ℹ️  Kernel tweaks skipped: serve root (EUID 0).${NC}"
		return 0
	fi

	local panic panic_oops
	panic=$(cat /proc/sys/kernel/panic 2>/dev/null || true)
	panic_oops=$(cat /proc/sys/kernel/panic_on_oops 2>/dev/null || true)

	if [[ "$panic" == "10" && "$panic_oops" == "1" ]]; then
		echo -e "${GREEN}✅ Kernel panic auto-reboot già configurato.${NC}"
		return 0
	fi

	cat <<'TWEAKEOF'
⚠️  Questa operazione cambia il comportamento di riavvio automatico della macchina:
      kernel.panic = 10        un kernel panic riavvia dopo 10 secondi
      kernel.panic_on_oops = 1 un "oops" diventa panic, e quindi riavvio
    Scrive /etc/sysctl.d/99-nasbot-panic.conf (persistenza) e applica subito
    i valori con sysctl -w (persistono solo fino al prossimo riavvio se il file
    non è scrivibile).
TWEAKEOF

	if [[ "$DRY_RUN" == "true" ]]; then
		echo -e "${BLUE}🧪 [dry-run] Scriverebbe /etc/sysctl.d/99-nasbot-panic.conf (kernel.panic 10 -> kernel.panic_on_oops 1)${NC}"
		return 0
	fi

	if ! confirm "Applicare i kernel tweaks (scrive sotto /etc e usa sysctl -w)?"; then
		echo "↩️  Kernel tweaks annullati: nulla scritto sotto /etc."
		return 0
	fi

	echo -e "${YELLOW}⚙️  Applying Kernel Panic auto-reboot settings...${NC}"
	sysctl -w kernel.panic=10 >/dev/null 2>&1 || true
	sysctl -w kernel.panic_on_oops=1 >/dev/null 2>&1 || true

	if [[ -d "/etc/sysctl.d" && -w "/etc/sysctl.d" ]]; then
		{
			echo "kernel.panic = 10"
			echo "kernel.panic_on_oops = 1"
		} >/etc/sysctl.d/99-nasbot-panic.conf
	elif [[ -d "/etc/sysctl.d" ]]; then
		echo -e "${YELLOW}⚠️  /etc/sysctl.d non è scrivibile: i valori valgono solo fino al prossimo riavvio.${NC}"
	else
		echo -e "${YELLOW}⚠️  /etc/sysctl.d assente: i valori valgono solo fino al prossimo riavvio.${NC}"
	fi
}

install_persistence() {
	if ! command -v crontab >/dev/null 2>&1; then
		echo -e "${RED}❌ crontab command not available${NC}"
		return 1
	fi

	local script_path cron_job
	script_path="$BOT_DIR/scripts/start_bot.sh"
	cron_job="*/5 * * * * $script_path watchdog"

	if crontab -l 2>/dev/null | grep -Fq "$script_path watchdog"; then
		echo -e "${GREEN}✅ Autostart (Cron) already configured.${NC}"
	else
		if [[ "$DRY_RUN" == "true" ]]; then
			echo -e "${BLUE}🧪 [dry-run] Aggiungerebbe al crontab: ${cron_job}${NC}"
			return 0
		fi
		echo -e "${YELLOW}⚙️  Configuring Autostart (Cron)...${NC}"
		(crontab -l 2>/dev/null; echo "$cron_job") | crontab -
		echo -e "${GREEN}✅ Autostart enabled (runs every 5 mins).${NC}"
	fi
}

usage() {
	print_header
	echo "Usage: $0 [OPTIONS] {start|stop|restart|status|watchdog|logs [n]|install}"
	echo
	echo "  start     - Start the bot (checks for pending updates)"
	echo "  stop      - Stop the bot"
	echo "  restart   - Restart the bot (checks for pending updates)"
	echo "  status    - Show detailed status (read-only)"
	echo "  watchdog  - Restart if inactive (for cron)"
	echo "  logs [n]  - Show last n logs (default: 50)"
	echo "  install   - Setup persistence and kernel tweaks (asks for confirmation)"
	echo
	echo "Options:"
	echo "  --dry-run    Preview everything without changing binary, crontab or /etc"
	echo "  --yes        Answer yes to every confirmation (kernel tweaks)"
	echo "  --help, -h   Show this help message"
	echo
	echo "Note: the pending-update check only runs for 'start' and 'restart', and"
	echo "      skips candidates built for another architecture."
}

# Un solo insieme di comandi validi, e lo stesso insieme usato dal dispatch in
# fondo allo script: la validazione e l'esecuzione non possono divergere.
VALID_COMMANDS=(
	start
	stop
	restart
	status
	watchdog
	logs
	install
)

is_valid_command() {
	local candidate="$1" known
	for known in "${VALID_COMMANDS[@]}"; do
		[[ "$candidate" == "$known" ]] && return 0
	done
	return 1
}

# --- Opzioni globali, poi comando ---
while [[ $# -gt 0 ]]; do
	case "$1" in
	--dry-run)
		DRY_RUN="true"
		shift
		;;
	--yes | -y)
		ASSUME_YES="true"
		shift
		;;
	--help | -h)
		usage
		exit 0
		;;
	--*)
		# Il precedente `*)` accettava QUALSIASI token sconosciuto come
		# comando e poi stampava l'uso uscendo con 0: uno script di gestione che
		# risponde "successo" a `./start_bot.sh --forces` o `./start_bot.sh sar`
		# non distingue un refuso da un comando eseguito. Un'opzione sconosciuta
		# viene rifiutata qui, prima di qualunque effetto.
		echo -e "${RED}Unknown option: $1${NC}"
		usage
		exit 1
		;;
	*)
		COMMAND="$1"
		shift
		break
		;;
	esac
done

# Nessun comando, o un comando non in VALID_COMMANDS: stampa l'uso ed esci con
# codice non zero. La validazione avviene PRIMA di init_dirs/migrate_legacy_layout
# perché un argomento sbagliato non deve creare directory né spostare file.
if [[ -z "$COMMAND" ]]; then
	echo -e "${RED}Missing command.${NC}"
	usage
	exit 1
fi

if ! is_valid_command "$COMMAND"; then
	echo -e "${RED}Unknown command: ${COMMAND}${NC}"
	echo "   Valid commands: ${VALID_COMMANDS[*]}"
	usage
	exit 1
fi

# Da qui in avanti "$@" sono gli argomenti del comando. Solo `logs` ne accetta
# uno; tutto il resto deve rifiutarli invece di ignorarli in silenzio.
if [[ "$COMMAND" != "logs" && $# -gt 0 ]]; then
	echo -e "${RED}Unexpected argument for '${COMMAND}': $1${NC}"
	usage
	exit 1
fi

if [[ "$COMMAND" == "logs" && $# -gt 1 ]]; then
	echo -e "${RED}'logs' accepts at most one line count, got $#: $*${NC}"
	usage
	exit 1
fi

init_dirs
migrate_legacy_layout
ensure_binary_permissions

case "$COMMAND" in
start)
	check_updates
	start_bot
	;;
stop)
	stop_bot
	;;
restart)
	check_updates
	restart_bot
	;;
status)
	status_bot
	;;
watchdog)
	watchdog
	;;
logs)
	show_logs "${1:-50}"
	;;
install)
	install_persistence
	apply_system_tweaks
	# Idempotente per definizione: `install` deve poter essere rieseguito. Se il
	# bot è già attivo lo stato desiderato è già raggiunto, e riporterlo come
	# fallimento renderebbe il secondo `install` rosso mentre ha fatto tutto
	# il suo lavoro (crontab idempotente, tweaks idempotenti).
	if is_running; then
		echo -e "${GREEN}✅ Bot already running (PID: $(get_pid)).${NC}"
	else
		start_bot
	fi
	;;
*)
	# Irraggiungibile: il comando è già stato validato contro VALID_COMMANDS.
	# Resta per non trasformare un comando aggiunto a VALID_COMMANDS senza
	#implementazione in un'uscita 0 silenziosa.
	echo -e "${RED}Command not implemented: ${COMMAND}${NC}"
	exit 1
	;;
esac
