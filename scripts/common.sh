#!/bin/bash
# Common utilities for NASBot deployment and runtime scripts
# This script provides:
# - Configuration loading (env > local config > defaults)
# - Logging functions
# - Error handling
# - Path resolution
# - Validation helpers

set -euo pipefail

# ============================================================================
# SCRIPT METADATA
# ============================================================================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# ============================================================================
# DEFAULT CONFIGURATION
# ============================================================================
declare -A DEFAULTS=(
    [APP_NAME]="nasbot"
    [BUILD_ARCH]="native"
    [CONFIG_FILE]="config.json"
    [CONFIG_EXAMPLE]="config.example.json"
    [LOG_FILE]="nasbot.log"
    [PID_FILE]="nasbot.pid"
    [STATE_FILE]="nasbot_state.json"
    [PACKAGE_OUT_DIR]="./dist"
    [INCLUDE_EXAMPLE_CONFIG]="true"
    [INCLUDE_README_RUNTIME]="true"
    [DEPLOY_PATH]="/Volume1/public"
    [USE_RSYNC]="true"
    [CHECK_UPDATES]="true"
    [UPDATE_FILE_PATTERN]="nasbot-update*"
    [VERBOSE]="false"
    [DEBUG]="false"
    [TZ]="UTC"
    [UMASK]="0022"
)

# ============================================================================
# LOGGING & OUTPUT
# ============================================================================

log_debug() {
    [[ "${VERBOSE:-false}" == "true" ]] && echo "[DEBUG] $*" >&2 || true
}

log_info() {
    echo "[INFO] $*" >&2
}

log_warn() {
    echo "[WARN] $*" >&2
}

log_error() {
    echo "[ERROR] $*" >&2
}

die() {
    log_error "$@"
    exit 1
}

# ============================================================================
# CONFIGURATION LOADING
# ============================================================================

# Load configuration hierarchically:
# 1. Default values (from DEFAULTS array)
# 2. nasbot.config.template (if exists)
# 3. Local config file nasbot.config.local (if exists)
# 4. Environment variables (CONFIG_PREFIX_VAR=value)
load_config() {
    local config_file="${1:-nasbot.config.local}"
    
    log_debug "Loading configuration from hierarchy..."
    
    # Load defaults
    for key in "${!DEFAULTS[@]}"; do
        eval "${key}='${DEFAULTS[$key]}'"
        log_debug "Default: ${key}=${DEFAULTS[$key]}"
    done
    
    # Load template if it exists
    if [[ -f "${REPO_ROOT}/nasbot.config.template" ]]; then
        log_debug "Loading template from ${REPO_ROOT}/nasbot.config.template"
        # Source template but only extract variables (skip functions/comments)
        set +u
        # shellcheck disable=SC1090
        source <(grep -E '^\s*[A-Z_]+=' "${REPO_ROOT}/nasbot.config.template" | grep -v '^#')
        set -u
    fi
    
    # Load local config if it exists
    if [[ -f "${config_file}" ]]; then
        log_debug "Loading local config from ${config_file}"
        set +u
        # shellcheck disable=SC1090
        source "${config_file}"
        set -u
    fi
    
    # Override with environment variables (NASBOT_VAR=value pattern)
    log_debug "Checking environment variable overrides..."
    for key in "${!DEFAULTS[@]}"; do
        env_var="NASBOT_${key}"
        if [[ -v "${env_var}" ]]; then
            eval "${key}=\${${env_var}}"
            log_debug "Env override: ${key}=${!key}"
        fi
    done
}

# Get configuration value with fallback
get_config() {
    local key="$1"
    local fallback="${2:-}"
    
    if [[ -v "${key}" ]]; then
        eval "echo \$${key}"
    elif [[ -n "${fallback}" ]]; then
        echo "${fallback}"
    else
        return 1
    fi
}

# ============================================================================
# PATH RESOLUTION
# ============================================================================

# Resolve path: convert relative paths to absolute based on runtime context
# Usage: resolve_path "config.json" "/Volume1/public" -> /Volume1/public/config.json
resolve_path() {
    local path="$1"
    local base_dir="${2:-.}"
    
    if [[ "$path" == /* ]]; then
        # Absolute path
        echo "$path"
    else
        # Relative path
        echo "${base_dir}/${path}"
    fi
}

# Resolve paths with environment variable fallback
# Usage: resolve_runtime_path "LOG_FILE" "/var/log" -> uses NASBOT_LOG_FILE if set, else config value
resolve_runtime_path() {
    local var_name="$1"
    local base_dir="${2:-.}"
    local env_var="NASBOT_${var_name}"
    
    local path
    if [[ -v "${env_var}" ]]; then
        path="${!env_var}"
    else
        path=$(get_config "${var_name}" "")
    fi
    
    [[ -z "${path}" ]] && return 1
    resolve_path "${path}" "${base_dir}"
}

# ============================================================================
# VALIDATION
# ============================================================================

# Validate directory exists and is writable
require_dir() {
    local dir="$1"
    local description="${2:-Directory}"
    
    if [[ ! -d "${dir}" ]]; then
        die "${description} does not exist: ${dir}"
    fi
    
    if [[ ! -w "${dir}" ]]; then
        die "${description} is not writable: ${dir}"
    fi
}

# Validate file is readable
require_file() {
    local file="$1"
    local description="${2:-File}"
    
    if [[ ! -f "${file}" ]]; then
        die "${description} not found: ${file}"
    fi
    
    if [[ ! -r "${file}" ]]; then
        die "${description} is not readable: ${file}"
    fi
}

# Validate command exists
require_cmd() {
    local cmd="$1"
    
    if ! command -v "${cmd}" &>/dev/null; then
        die "Required command not found: ${cmd}"
    fi
}

# Validate architecture is supported
validate_arch() {
    local arch="$1"
    
    case "${arch}" in
        native|arm64|amd64|armv7|386)
            return 0
            ;;
        *)
            die "Unsupported architecture: ${arch} (valid: native, arm64, amd64, armv7, 386)"
            ;;
    esac
}

# ============================================================================
# ARCHITECTURE & BUILD
# ============================================================================

# Get GOOS and GOARCH for a given architecture string
get_go_arch() {
    local arch="$1"
    
    case "${arch}" in
        native)
            echo "$(go env GOOS):$(go env GOARCH)" ;;
        arm64)
            echo "linux:arm64" ;;
        amd64)
            echo "linux:amd64" ;;
        armv7)
            echo "linux:armv7l" ;;
        386)
            echo "linux:386" ;;
        *)
            die "Unsupported architecture: ${arch}" ;;
    esac
}

# Get binary suffix for architecture
get_arch_suffix() {
    local arch="$1"
    
    case "${arch}" in
        native)
            echo "" ;;
        *)
            echo "-${arch}" ;;
    esac
}

# ============================================================================
# FILE OPERATIONS
# ============================================================================

# Safe copy with backup
safe_copy() {
    local src="$1"
    local dest="$2"
    
    require_file "${src}" "Source file"
    
    if [[ -f "${dest}" ]]; then
        local backup
        backup="${dest}.backup.$(date +%s)"
        log_info "Backing up existing file: ${backup}"
        cp "${dest}" "${backup}"
    fi
    
    cp "${src}" "${dest}"
    log_debug "Copied: ${src} -> ${dest}"
}

# Find and validate config file
find_config() {
    local runtime_dir="${1:-.}"
    
    local candidates=(
        "${runtime_dir}/config.json"
        "${runtime_dir}/../config.json"
        "${REPO_ROOT}/config.json"
        "${REPO_ROOT}/config.example.json"
    )
    
    for candidate in "${candidates[@]}"; do
        if [[ -f "${candidate}" ]]; then
            echo "${candidate}"
            return 0
        fi
    done
    
    return 1
}

# ============================================================================
# STATE MANAGEMENT
# ============================================================================

# Read PID from file
read_pid() {
    local pid_file="$1"
    
    if [[ -f "${pid_file}" ]]; then
        cat "${pid_file}"
    fi
}

# Check if process is running
is_process_running() {
    local pid="$1"
    
    if [[ -z "${pid}" ]]; then
        return 1
    fi
    
    kill -0 "${pid}" 2>/dev/null || return 1
}

# ============================================================================
# FORMATTING & UTILS
# ============================================================================

# Format bytes as human-readable
format_bytes() {
    local bytes="$1"
    
    if ((bytes < 1024)); then
        echo "${bytes}B"
    elif ((bytes < 1024 * 1024)); then
        echo "$((bytes / 1024))KB"
    elif ((bytes < 1024 * 1024 * 1024)); then
        echo "$((bytes / (1024 * 1024)))MB"
    else
        echo "$((bytes / (1024 * 1024 * 1024)))GB"
    fi
}

# Get script size in readable format
get_script_size() {
    local script="$1"
    local size
    size=$(wc -c < "${script}")
    format_bytes "${size}"
}

# Export common variables for subshells
export_config() {
    # Export all variables that start with uppercase letters
    for var in "${!DEFAULTS[@]}"; do
        if [[ -v "${var}" ]]; then
            export "${var?}"
        fi
    done
}

# Print current config
print_config() {
    log_info "Current configuration:"
    for key in $(printf '%s\n' "${!DEFAULTS[@]}" | sort); do
        if [[ -v "${key}" ]]; then
            log_info "  ${key}=${!key}"
        fi
    done
}

# ============================================================================
# EXPORT FOR USE IN OTHER SCRIPTS
# ============================================================================

export SCRIPT_DIR REPO_ROOT
export -f log_debug log_info log_warn log_error die
export -f load_config get_config resolve_path resolve_runtime_path
export -f require_dir require_file require_cmd validate_arch
export -f get_go_arch get_arch_suffix safe_copy find_config
export -f read_pid is_process_running format_bytes get_script_size
export -f print_config export_config validate_arch

# Auto-load config on source (can be disabled with NASBOT_NO_AUTO_LOAD=1)
if [[ "${NASBOT_NO_AUTO_LOAD:-0}" != "1" ]]; then
    load_config
fi
