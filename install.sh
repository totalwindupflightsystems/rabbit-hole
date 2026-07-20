#!/usr/bin/env bash
set -euo pipefail

# Rabbit-Hole — One-command install
# curl -sSL https://gitlab.readydedis.com/rabbit-hole/rabbit-hole/-/raw/main/install.sh | bash

SCRIPT_VERSION="1.0.0"
BINARY="/usr/local/bin/rabbit-hole"
DATA_DIR="/var/lib/rabbit-hole"
LOG_DIR="/var/log/rabbit-hole"
CONFIG_DIR="/etc/rabbit-hole"
SERVICE_FILE="/etc/systemd/system/rabbit-hole.service"
MODEL_DIR="/var/lib/rabbit-hole/models"
MODEL_NAME="gemma3:4b"

# --- Flags ---
PREFIX="/usr/local"
SKIP_MODEL=false
SKIP_SYSTEMD=false
SKIP_BUILD=false
VERSION="latest"
OLLAMA_SERVE=false

# --- Colors ---
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
err()  { echo -e "${RED}[x]${NC} $*"; exit 1; }
step() { echo -e "\n${BOLD}==>${NC} $*"; }

# --- Usage ---
usage() {
    cat <<EOF
Rabbit-Hole installer v${SCRIPT_VERSION}

Usage: curl -sSL <raw-install-url> | bash -s -- [OPTIONS]
   or: bash install.sh [OPTIONS]

Options:
  --prefix PATH       Install prefix (default: /usr/local)
  --no-model          Skip Gemma model download
  --no-systemd        Skip systemd service setup
  --no-build          Skip building from source (use pre-existing binary)
  --ollama-serve      Start Ollama after install (for model download)
  --version VERSION   Install specific version (tag or 'latest')
  -h, --help          Show this help

EOF
    exit 0
}

# --- Parse args ---
while [[ $# -gt 0 ]]; do
    case "$1" in
        --prefix)       PREFIX="$2"; shift 2 ;;
        --no-model)     SKIP_MODEL=true; shift ;;
        --no-systemd)   SKIP_SYSTEMD=true; shift ;;
        --no-build)     SKIP_BUILD=true; shift ;;
        --ollama-serve) OLLAMA_SERVE=true; shift ;;
        --version)      VERSION="$2"; shift 2 ;;
        -h|--help)      usage ;;
        *)              err "Unknown flag: $1" ;;
    esac
done

BINARY="${PREFIX}/bin/rabbit-hole"

# --- Preflight ---
step "Rabbit-Hole installer v${SCRIPT_VERSION}"
log "Install prefix: ${PREFIX}"
log "Binary: ${BINARY}"

if [[ "$(id -u)" -ne 0 ]]; then
    err "This script must be run as root (or with sudo)."
fi

# --- Check dependencies ---
step "Checking dependencies"

if ! command -v go &>/dev/null && [[ "$SKIP_BUILD" != true ]]; then
    warn "Go not found. Install Go 1.25+ or use --no-build with a pre-built binary."
    warn "  https://go.dev/dl/"
    err "Go is required for building from source."
fi

if command -v go &>/dev/null; then
    GO_VERSION=$(go version | grep -oP 'go\K[0-9]+\.[0-9]+' || echo "0.0")
    log "Go version: $(go version)"
fi

# Check for Ollama (optional — for Gemma model)
if [[ "$SKIP_MODEL" != true ]]; then
    if command -v ollama &>/dev/null; then
        log "Ollama found: $(ollama --version 2>/dev/null || echo 'installed')"
    else
        warn "Ollama not found. Install it for local Gemma classification:"
        warn "  curl -fsSL https://ollama.com/install.sh | sh"
        if [[ "$OLLAMA_SERVE" != true ]]; then
            warn "Skipping model download. Re-run without --no-model after installing Ollama."
            SKIP_MODEL=true
        fi
    fi
fi

# --- Build ---
step "Building rabbit-hole"

if [[ "$SKIP_BUILD" == true ]]; then
    if [[ ! -x "$BINARY" ]]; then
        err "Binary not found at $BINARY and --no-build is set. Place rabbit-hole at $BINARY first."
    fi
    log "Skipping build, using existing binary at $BINARY"
else
    # Clone or use current directory
    BUILD_DIR=$(mktemp -d /tmp/rabbit-hole-build.XXXXXX)
    trap "rm -rf $BUILD_DIR" EXIT

    log "Cloning rabbit-hole..."
    if ! git clone https://gitlab.readydedis.com/rabbit-hole/rabbit-hole.git "$BUILD_DIR" 2>/dev/null; then
        warn "Git clone failed — building from current directory"
        BUILD_DIR="$PWD"
    fi

    cd "$BUILD_DIR"

    if [[ "$VERSION" != "latest" ]]; then
        log "Checking out $VERSION..."
        git checkout "$VERSION" 2>/dev/null || warn "Tag/branch $VERSION not found, using HEAD"
    fi

    log "Building (this may take a minute)..."
    CGO_ENABLED=0 go build -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown) -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o "$BINARY" ./cmd/rabbit-hole/

    log "Binary built: $BINARY ($(du -h "$BINARY" | cut -f1))"
    chmod 755 "$BINARY"
fi

# --- Verify binary ---
if ! "$BINARY" version &>/dev/null; then
    err "Binary verification failed. $BINARY does not run."
fi
log "Binary verified: $("$BINARY" version 2>&1 | head -1)"

# --- Directories ---
step "Setting up directories"

for dir in "$DATA_DIR" "$LOG_DIR" "$CONFIG_DIR" "$MODEL_DIR"; do
    mkdir -p "$dir"
    log "Created $dir"
done

# --- User ---
if ! id -u rabbit-hole &>/dev/null; then
    useradd --system --no-create-home --shell /usr/sbin/nologin \
        --home-dir "$DATA_DIR" rabbit-hole 2>/dev/null || \
    useradd --system --no-create-home --shell /sbin/nologin \
        --home-dir "$DATA_DIR" rabbit-hole 2>/dev/null || \
    warn "Could not create rabbit-hole user. Continuing..."
    log "Created rabbit-hole system user"
else
    log "rabbit-hole user already exists"
fi

chown -R rabbit-hole:rabbit-hole "$DATA_DIR" "$LOG_DIR" "$MODEL_DIR" 2>/dev/null || true

# --- Config file (env) ---
if [[ ! -f "$CONFIG_DIR/env" ]]; then
    cat > "$CONFIG_DIR/env" <<'ENVEOF'
# Rabbit-Hole configuration
# See: docs for all RABBITHOLE_* variables

RABBITHOLE_DATA_DIR=/var/lib/rabbit-hole
RABBITHOLE_LOG_DIR=/var/log/rabbit-hole
RABBITHOLE_LISTEN_ADDR=:8080
RABBITHOLE_MODEL_PATH=/var/lib/rabbit-hole/models

# Chat model (set to empty to disable NL chat, keep keyword search only)
RABBITHOLE_CHAT_ENABLED=true
RABBITHOLE_CHAT_PROVIDER=ollama
RABBITHOLE_CHAT_MODEL=gemma3:4b
ENVEOF
    log "Created $CONFIG_DIR/env"
else
    log "$CONFIG_DIR/env already exists, not overwriting"
fi

# --- Model download ---
if [[ "$SKIP_MODEL" != true ]] && command -v ollama &>/dev/null; then
    step "Downloading Gemma model"
    
    # Ensure Ollama is running
    if ! curl -s http://localhost:11434/api/tags &>/dev/null; then
        if [[ "$OLLAMA_SERVE" == true ]]; then
            log "Starting Ollama..."
            ollama serve &
            sleep 3
        else
            warn "Ollama is not running. Start it first: ollama serve"
            warn "Then pull the model: ollama pull $MODEL_NAME"
            warn "Skipping model download."
            SKIP_MODEL=true
        fi
    fi
    
    if [[ "$SKIP_MODEL" != true ]]; then
        if ollama list 2>/dev/null | grep -q "$MODEL_NAME"; then
            log "Model $MODEL_NAME already downloaded"
        else
            log "Pulling $MODEL_NAME (this downloads ~2.3GB)..."
            ollama pull "$MODEL_NAME" || warn "Model pull failed. Run manually: ollama pull $MODEL_NAME"
        fi
    fi
fi

# --- systemd ---
if [[ "$SKIP_SYSTEMD" != true ]]; then
    step "Setting up systemd service"

    if [[ ! -f "$SERVICE_FILE" ]]; then
        cat > "$SERVICE_FILE" <<SERVICEEOF
[Unit]
Description=Rabbit-Hole — Agent Legibility Daemon
Documentation=https://gitlab.readydedis.com/rabbit-hole/rabbit-hole
After=network-online.target ollama.service
Wants=network-online.target

[Service]
Type=simple
User=rabbit-hole
Group=rabbit-hole
WorkingDirectory=/var/lib/rabbit-hole
ExecStart=${BINARY} serve

Restart=always
RestartSec=5s
TimeoutStopSec=30s

NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/rabbit-hole /var/log/rabbit-hole

AmbientCapabilities=CAP_BPF CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_NET_ADMIN CAP_NET_RAW
CapabilityBoundingSet=CAP_BPF CAP_SYS_ADMIN CAP_SYS_RESOURCE CAP_NET_ADMIN CAP_NET_RAW

MemoryMax=2G
MemoryHigh=1.5G
LimitNOFILE=65536
LimitNPROC=256

StandardOutput=journal
StandardError=journal
SyslogIdentifier=rabbit-hole

EnvironmentFile=-/etc/rabbit-hole/env

[Install]
WantedBy=multi-user.target
SERVICEEOF
        log "Created $SERVICE_FILE"
    else
        log "$SERVICE_FILE already exists, not overwriting"
    fi

    systemctl daemon-reload
    log "systemd reloaded"

    # Enable but don't start — user decides
    systemctl enable rabbit-hole.service 2>/dev/null || warn "Could not enable rabbit-hole.service"
    log "Service enabled (not started). Start with: sudo systemctl start rabbit-hole"
fi

# --- Done ---
step "Installation complete"
echo ""
echo "  Binary:   ${BINARY}"
echo "  Data:     ${DATA_DIR}"
echo "  Config:   ${CONFIG_DIR}/env"
echo "  Logs:     journalctl -u rabbit-hole -f"
echo ""
if [[ "$SKIP_SYSTEMD" != true ]]; then
    echo "  Start:    sudo systemctl start rabbit-hole"
    echo "  Status:   sudo systemctl status rabbit-hole"
fi
echo ""
echo "  Try it:   rabbit-hole attach --pid \$(pgrep -n your-agent) --chat"
echo ""
log "Done! Rabbit-Hole is installed."
