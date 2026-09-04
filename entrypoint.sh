#!/bin/bash
# BAAS Container Entrypoint - Secure startup script for BAAS Chrome automation container
# Implements Chainguard-inspired security practices with structured JSON logging

set -euo pipefail  # Fail fast on errors
set +h             # Disable hash table for security
umask 077          # Restrictive file permissions by default

# Configuration with sensible defaults
SCREEN_RESOLUTION=${SCREEN_RESOLUTION:-"1920x1080x24"}
DISPLAY_NUM=${DISPLAY_NUM:-99}

# Structured JSON logging function using heredoc for reliable output
log() {
    local level="$1"
    local message="$2"
    local context="$3"
    local timestamp
    timestamp=$(date +'%Y-%m-%d %H:%M:%S')
    
    # If no context provided, use empty object
    if [ -z "$context" ]; then
        context="{}"
    fi
    
    # Use heredoc to ensure exact JSON output without shell interference
    cat >&2 << EOF
{"date":"${timestamp}","level":"${level}","message":"${message}","context":${context}}
EOF
}

# Convenience functions for different log levels
log_info() {
    log "INFO" "$1" "$2"
}

log_warn() {
    log "WARN" "$1" "$2"
}

log_error() {
    log "ERROR" "$1" "$2"
}

log_debug() {
    log "DEBUG" "$1" "$2"
}

# Input validation (performed after logging functions are available)
if [[ ! "$SCREEN_RESOLUTION" =~ ^[0-9]+x[0-9]+x[0-9]+$ ]]; then
    log_error "Invalid screen resolution format" "{\"resolution\":\"$SCREEN_RESOLUTION\",\"expected_format\":\"NxNxN\"}"
    exit 1
fi

if [[ ! "$DISPLAY_NUM" =~ ^[0-9]+$ ]] || [ "$DISPLAY_NUM" -lt 1 ] || [ "$DISPLAY_NUM" -gt 999 ]; then
    log_error "Invalid display number" "{\"display_num\":\"$DISPLAY_NUM\",\"valid_range\":\"1-999\"}"
    exit 1
fi

export DISPLAY=":$DISPLAY_NUM"

uid_value=$(id -u)
gid_value=$(id -g)
log_info "Starting BAAS container" "{\"pid\":$$,\"display\":\"$DISPLAY\",\"resolution\":\"$SCREEN_RESOLUTION\",\"uid\":$uid_value,\"gid\":$gid_value}"

# Graceful shutdown with process tracking and secure cleanup
declare -a CHILD_PIDS=()

cleanup() {
    local exit_code=${1:-0}
    log_info "Initiating graceful shutdown" "{\"exit_code\":$exit_code,\"child_processes\":${#CHILD_PIDS[@]}}"
    
    # Kill child processes gracefully first
    for pid in "${CHILD_PIDS[@]}"; do
        if kill -0 "$pid" 2>/dev/null; then
            log_info "Terminating process" "{\"pid\":$pid,\"signal\":\"SIGTERM\"}"
            kill -TERM "$pid" 2>/dev/null || true
        fi
    done
    
    # Wait for graceful shutdown
    sleep 2
    
    # Force kill if necessary
    local force_killed=0
    for process in baas chromedriver chrome Xvfb fluxbox; do
        if pkill -f "$process" 2>/dev/null; then
            ((force_killed++))
            log_warn "Force killed process" "{\"process\":\"$process\",\"signal\":\"SIGKILL\"}"
        fi
    done

    # Clean up X11 lock files before exit
    log_info "Cleaning up X11 lock files" "{\"lock_files\":\"/tmp/.X*-lock\",\"socket_files\":\"/tmp/.X11-unix/X*\"}"
    rm -f /tmp/.X*-lock /tmp/.X11-unix/X* 2>/dev/null || true

    # Clean temporary files securely
    if [ -d "/app/tmp" ]; then
        local files_cleaned=$(find /app/tmp -type f | wc -l)
        find /app/tmp -type f -exec shred -vfz -n 3 {} \; 2>/dev/null || true
        rm -rf /app/tmp/* 2>/dev/null || true
        log_info "Cleaned temporary files" "{\"files_count\":$files_cleaned}"
    fi
    
    log_info "Shutdown complete" "{\"exit_code\":$exit_code,\"force_killed\":$force_killed}"
    exit "$exit_code"
}

# Configure signal handlers for proper container lifecycle management
trap 'cleanup 130' SIGINT   # Ctrl+C
trap 'cleanup 143' SIGTERM  # Docker stop  
trap 'cleanup 1' ERR        # Error handling
trap 'cleanup 129' SIGHUP   # Terminal hangup

# Configure secure runtime environment
export HOME=/app
export XDG_RUNTIME_DIR=/app/tmp
export TMPDIR=/app/tmp
# Set container-specific environment variables
export CONTAINER_ENV=true
export BAAS_CONTAINER=true
export X11_AVAILABLE=true

# Ensure temp directory exists and is writable
if [ ! -d "/app/tmp" ]; then
    mkdir -p /app/tmp 2>/dev/null || {
        log_error "Cannot create temp directory" "{\"directory\":\"/app/tmp\",\"reason\":\"permission_denied\"}"
        cleanup 1
    }
fi

# Remove potentially dangerous environment variables
unset HISTFILE HISTSIZE HISTFILESIZE
export SHELL=/bin/false
export PATH="/usr/local/bin:/usr/bin:/bin"

# Disable D-Bus to reduce attack surface
export DBUS_SESSION_BUS_ADDRESS="disabled:"
log_info "D-Bus disabled for security" "{\"reason\":\"security_hardening\",\"impact\":\"chrome_warnings_expected\"}"

# Verify X11 socket directory exists (should be provided by base image)
if [ ! -d "/tmp/.X11-unix" ]; then
    log_warn "X11 socket directory missing - this may cause display issues" "{\"directory\":\"/tmp/.X11-unix\",\"base_image\":\"selenium/standalone-chrome\"}"
fi

# Clean up stale X11 lock files from previous container runs
log_info "Cleaning up stale X11 lock files" "{\"lock_files\":\"/tmp/.X*-lock\",\"socket_files\":\"/tmp/.X11-unix/X*\"}"
rm -f /tmp/.X*-lock /tmp/.X11-unix/X* 2>/dev/null || true

# Process custom root certificates with strict validation
setup_certificates() {
    local cert_count=0
    
    if ! env | grep -q '^ROOT_CA_'; then
        return 0
    fi
    
    log_info "Setting up custom certificates" "{\"nss_db_path\":\"$HOME/.pki/nssdb\"}"
    mkdir -p "$HOME/.pki/nssdb"
    
    # Initialize NSS database
    if ! certutil -N --empty-password -d "sql:$HOME/.pki/nssdb" 2>/dev/null; then
        log_warn "Could not initialize NSS database" "{\"database_path\":\"sql:$HOME/.pki/nssdb\"}"
        return 1
    fi
    
    # Process certificates with strict validation
    while IFS= read -r -d '' env_var; do
        if [[ "$env_var" =~ ^ROOT_CA_([A-Za-z0-9_]+)=(.+)$ ]]; then
            local cert_name="${BASH_REMATCH[1]}"
            local cert_data="${BASH_REMATCH[2]}"
            
            # Validate certificate name (alphanumeric + underscore only)
            if [[ ! "$cert_name" =~ ^[A-Za-z0-9_]+$ ]]; then
                log_warn "Invalid certificate name" "{\"cert_name\":\"$cert_name\",\"allowed_pattern\":\"[A-Za-z0-9_]+\"}"
                continue
            fi
            
            local cert_file="/app/tmp/cert_${cert_name}.pem"
            
            # Decode and validate certificate
            if echo "$cert_data" | base64 -d > "$cert_file" 2>/dev/null; then
                # Verify it's actually a certificate
                if openssl x509 -in "$cert_file" -noout 2>/dev/null; then
                    if certutil -A -n "$cert_name" -t "TC,C,T" -i "$cert_file" -d "sql:$HOME/.pki/nssdb" 2>/dev/null; then
                        ((cert_count++))
                        log_info "Installed certificate" "{\"cert_name\":\"$cert_name\",\"trust_flags\":\"TC,C,T\"}"
                    else
                        log_warn "Failed to install certificate" "{\"cert_name\":\"$cert_name\",\"reason\":\"certutil_failed\"}"
                    fi
                else
                    log_warn "Invalid certificate format" "{\"cert_name\":\"$cert_name\",\"validation\":\"openssl_x509_failed\"}"
                fi
                # Secure file deletion
                shred -vfz -n 3 "$cert_file" 2>/dev/null || rm -f "$cert_file"
            else
                log_warn "Invalid base64 data for certificate" "{\"cert_name\":\"$cert_name\",\"validation\":\"base64_decode_failed\"}"
            fi
        fi
    done < <(env -0)
    
    if [ $cert_count -gt 0 ]; then
        log_info "Custom certificates installation completed" "{\"certificates_installed\":$cert_count}"
    fi
}

setup_certificates

# Initialize virtual X server with security hardening
log_info "Starting X server" "{\"display\":\"$DISPLAY\",\"resolution\":\"$SCREEN_RESOLUTION\",\"security_flags\":[\"nolisten_tcp\",\"nolisten_unix\",\"noreset\"]}"
Xvfb "$DISPLAY" \
    -ac \
    -screen 0 "$SCREEN_RESOLUTION" \
    -nolisten tcp \
    -nolisten unix \
    -noreset \
    +extension GLX \
    +extension RANDR \
    +extension RENDER &

xvfb_pid=$!
CHILD_PIDS+=("$xvfb_pid")

# Wait for X server to become available
timeout=10
while [ $timeout -gt 0 ]; do
    if xdpyinfo -display "$DISPLAY" >/dev/null 2>&1; then
        log_info "X server started successfully" "{\"pid\":$xvfb_pid,\"display\":\"$DISPLAY\",\"timeout_remaining\":$timeout}"
        break
    fi
    sleep 1
    ((timeout--))
done

if [ $timeout -eq 0 ]; then
    log_error "X server failed to start within timeout" "{\"pid\":$xvfb_pid,\"display\":\"$DISPLAY\",\"timeout_seconds\":10}"
    cleanup 1
fi

# Launch lightweight window manager
log_info "Starting window manager" "{\"wm\":\"fluxbox\",\"display\":\"$DISPLAY\"}"
fluxbox -display "$DISPLAY" 2>/dev/null &
fluxbox_pid=$!
CHILD_PIDS+=("$fluxbox_pid")
sleep 2
log_info "Window manager started" "{\"pid\":$fluxbox_pid,\"wm\":\"fluxbox\"}"

# Start VNC server if enabled
if [ "${ENABLE_VNC:-false}" = "true" ]; then
    log_info "Starting VNC server" "{\"display\":\"$DISPLAY\",\"port\":5900,\"no_password\":${SE_VNC_NO_PASSWORD:-0}}"
    
    # Configure VNC password
    if [ "${SE_VNC_NO_PASSWORD:-0}" = "1" ]; then
        # Unauthenticated. Only safe when port 5900 is not reachable off-host.
        x11vnc -display "$DISPLAY" -forever -shared -nopw -rfbport 5900 2>&1 | logger &
        vnc_pid=$!
    elif [ -n "${VNC_PASSWORD:-}" ]; then
        mkdir -p "$HOME/.vnc"
        x11vnc -storepasswd "$VNC_PASSWORD" "$HOME/.vnc/passwd" 2>/dev/null
        x11vnc -display "$DISPLAY" -forever -shared -rfbauth "$HOME/.vnc/passwd" -rfbport 5900 2>&1 | logger &
        vnc_pid=$!
    else
        # Refuse rather than fall back to a default password.
        log_error "ENABLE_VNC is set but no password was configured" \
            "{\"fix\":\"set VNC_PASSWORD, or SE_VNC_NO_PASSWORD=1 to run unauthenticated\"}"
        exit 1
    fi

    CHILD_PIDS+=("$vnc_pid")
    
    # Wait for VNC to start
    sleep 2
    if ps -p $vnc_pid > /dev/null 2>&1; then
        log_info "VNC server started successfully" "{\"pid\":$vnc_pid,\"port\":5900}"
    else
        log_warn "VNC server may have failed to start" "{\"port\":5900}"
    fi
fi

# Initialize ChromeDriver with network restrictions
allowed_ips="${ALLOWED_IPS:-127.0.0.1}"
allowed_origins="${ALLOWED_ORIGINS:-http://localhost:8080,https://localhost:8080}"
driver_args="${DRIVER_ARGS:-}"

log_info "Starting ChromeDriver" "{\"port\":4444,\"allowed_ips\":\"$allowed_ips\",\"allowed_origins\":\"$allowed_origins\",\"security_flags\":[\"disable-dev-shm-usage\",\"disable-extensions\",\"no-sandbox\",\"disable-gpu\"]}"

# Security check: validate IP allowlist configuration
if [[ ! "$allowed_ips" =~ ^[0-9.,\s]+$ ]] && [[ "$allowed_ips" != "127.0.0.1" ]]; then
    log_warn "Potentially unsafe ALLOWED_IPS configuration" "{\"allowed_ips\":\"$allowed_ips\",\"recommended\":\"127.0.0.1\"}"
fi

chromedriver \
    --port=4444 \
    --allowed-ips="$allowed_ips" \
    --allowed-origins="$allowed_origins" \
    --disable-dev-shm-usage \
    --disable-extensions \
    --no-sandbox \
    --disable-gpu \
    --disable-background-timer-throttling \
    --disable-backgrounding-occluded-windows \
    --disable-renderer-backgrounding \
    $driver_args &

chromedriver_pid=$!
CHILD_PIDS+=("$chromedriver_pid")

# Wait for ChromeDriver to become ready
timeout=15
while [ $timeout -gt 0 ]; do
    if curl -sf --max-time 2 http://localhost:4444/status >/dev/null 2>&1; then
        log_info "ChromeDriver started successfully" "{\"pid\":$chromedriver_pid,\"port\":4444,\"timeout_remaining\":$timeout}"
        break
    fi
    sleep 1
    ((timeout--))
done

if [ $timeout -eq 0 ]; then
    log_warn "ChromeDriver may not be fully ready, continuing anyway" "{\"pid\":$chromedriver_pid,\"port\":4444,\"timeout_seconds\":15}"
fi

# Launch BAAS application with security validation
log_info "Preparing to start BAAS application" "{\"working_directory\":\"/app\"}"
cd /app

# Verify application binary integrity
if [ ! -f "/app/baas" ] || [ ! -x "/app/baas" ]; then
    file_exists=$([ -f "/app/baas" ] && echo true || echo false)
    is_executable=$([ -x "/app/baas" ] && echo true || echo false)
    log_error "BAAS binary not found or not executable" "{\"binary_path\":\"/app/baas\",\"file_exists\":$file_exists,\"is_executable\":$is_executable}"
    cleanup 1
fi

# Security check: ensure we're running as non-root
if [ "$(id -u)" -eq 0 ]; then
    current_uid_check=$(id -u)
    log_error "Refusing to run as root for security reasons" "{\"current_uid\":$current_uid_check,\"required\":\"non-root\"}"
    cleanup 1
fi

# Production startup - debug features disabled for security
current_uid=$(id -u)
current_gid=$(id -g)
log_info "Starting BAAS application in production mode" "{\"uid\":$current_uid,\"gid\":$current_gid,\"pwd\":\"$PWD\",\"mode\":\"production\"}"

# Execute with minimal environment
log_info "Executing BAAS binary" "{\"binary_path\":\"/app/baas\",\"exec_method\":\"exec\"}"
exec /app/baas
