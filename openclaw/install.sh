#!/usr/bin/env bash
set -euo pipefail

# ioSwarm Agent Installer for OpenClaw
# Usage: curl -sSL https://raw.githubusercontent.com/iotexproject/ioswarm-agent/main/openclaw/install.sh | bash

REPO="iotexproject/ioswarm-agent"
BINARY="ioswarm-agent"
AGENT_DIR="$HOME/.ioswarm/agent"
SCRIPT_URL="https://raw.githubusercontent.com/${REPO}/main/openclaw/ioswarm.sh"
REGISTRY_URL="https://raw.githubusercontent.com/${REPO}/main/openclaw/delegates.json"

GREEN='\033[0;32m'
CYAN='\033[0;36m'
NC='\033[0m'

log() { echo -e "${GREEN}[ioswarm]${NC} $1"; }

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64)  ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
    esac
}

download_binary() {
    VERSION=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        echo "Failed to fetch latest version"; exit 1
    fi
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY}-${OS}-${ARCH}"
    log "Downloading ioswarm-agent ${VERSION} (${OS}/${ARCH})..."
    curl -sSL -o "${AGENT_DIR}/ioswarm-agent" "$DOWNLOAD_URL"
    chmod +x "${AGENT_DIR}/ioswarm-agent"
}

main() {
    echo ""
    echo -e "${CYAN}  ioSwarm — Earn IOTX with your idle compute${NC}"
    echo ""

    detect_platform
    mkdir -p "$AGENT_DIR"

    download_binary

    log "Downloading ioswarm.sh..."
    curl -sSL -o "${AGENT_DIR}/ioswarm.sh" "$SCRIPT_URL"
    chmod +x "${AGENT_DIR}/ioswarm.sh"

    # Download delegate registry
    curl -sSL -o "${AGENT_DIR}/delegates.json" "$REGISTRY_URL" 2>/dev/null || true

    echo ""
    log "Installed to ${AGENT_DIR}/"
    log ""
    log "Next: run setup to generate your wallet and connect"
    log ""
    log "  ${AGENT_DIR}/ioswarm.sh setup"
    echo ""
}

main "$@"
