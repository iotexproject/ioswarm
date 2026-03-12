#!/usr/bin/env bash
set -euo pipefail

# ioswarm.sh — ioSwarm agent management script for OpenClaw
# All state lives in ~/.ioswarm/agent/

AGENT_DIR="$HOME/.ioswarm/agent"
AGENT_BIN="${AGENT_DIR}/ioswarm-agent"
CONFIG_FILE="${AGENT_DIR}/config.env"
WALLET_KEY="${AGENT_DIR}/wallet.key"
WALLET_ADDR="${AGENT_DIR}/wallet.addr"
PID_FILE="${AGENT_DIR}/agent.pid"
LOG_FILE="${AGENT_DIR}/agent.log"
DELEGATES_FILE="${AGENT_DIR}/delegates.json"

# Default reward contract on IoTeX mainnet
DEFAULT_CONTRACT="0x96F475F87911615dD710f9cB425Af8ed0e167C89"
DEFAULT_RPC="https://babel-api.mainnet.iotex.io"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
CYAN='\033[0;36m'
NC='\033[0m'

log()   { echo -e "${GREEN}[ioswarm]${NC} $1"; }
warn()  { echo -e "${YELLOW}[ioswarm]${NC} $1"; }
error() { echo -e "${RED}[ioswarm]${NC} $1" >&2; }

# --- Wallet ---

generate_wallet() {
    if [ -f "$WALLET_KEY" ]; then
        log "Wallet already exists: $(cat "$WALLET_ADDR")"
        return 0
    fi

    log "Generating IOTX wallet..."

    # Use the agent binary's keygen subcommand if available
    if [ -x "$AGENT_BIN" ] && "$AGENT_BIN" keygen --help &>/dev/null; then
        "$AGENT_BIN" keygen --out "$WALLET_KEY" --addr-out "$WALLET_ADDR"
    else
        # Fallback: generate secp256k1 private key via openssl
        local privkey
        privkey=$(openssl ecparam -name secp256k1 -genkey -noout 2>/dev/null \
            | openssl ec -text -noout 2>/dev/null \
            | grep -A3 'priv:' | tail -3 | tr -d ' :\n')

        if [ -z "$privkey" ] || [ ${#privkey} -lt 64 ]; then
            # Simpler fallback: random 32 bytes (valid for secp256k1 with overwhelming probability)
            privkey=$(openssl rand -hex 32)
        fi

        echo "$privkey" > "$WALLET_KEY"
        chmod 600 "$WALLET_KEY"

        # Derive address: keccak256(pubkey) → last 20 bytes
        # This requires the agent binary. If unavailable, store a placeholder.
        if [ -x "$AGENT_BIN" ] && "$AGENT_BIN" addr --key "$WALLET_KEY" &>/dev/null; then
            "$AGENT_BIN" addr --key "$WALLET_KEY" > "$WALLET_ADDR"
        else
            # Placeholder — will be resolved on first agent start
            echo "pending-derivation" > "$WALLET_ADDR"
            warn "Wallet address will be derived on first agent start"
        fi
    fi

    chmod 600 "$WALLET_KEY"
    log "Wallet created"
    log "Private key: ${WALLET_KEY} (mode 600)"
    warn "Back up ~/.ioswarm/agent/wallet.key — if you lose it, you lose your earnings."
}

# --- Delegate discovery ---

load_delegates() {
    if [ ! -f "$DELEGATES_FILE" ]; then
        # Try to download
        curl -sSL --connect-timeout 5 -o "$DELEGATES_FILE" \
            "https://raw.githubusercontent.com/iotexproject/ioswarm-agent/main/openclaw/delegates.json" 2>/dev/null || true
    fi

    if [ ! -f "$DELEGATES_FILE" ]; then
        echo "[]"
        return
    fi
    cat "$DELEGATES_FILE"
}

discover_delegates() {
    log "Discovering delegates with ioSwarm enabled..."
    echo ""

    local delegates best_rate=0 best_name="" best_grpc=""

    # Parse delegates.json (jq-free: extract name, grpc, api fields)
    local i=0
    while true; do
        local name grpc api
        name=$(load_delegates | grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -n "$((i+1))p" | sed 's/.*"\([^"]*\)"/\1/')
        [ -z "$name" ] && break

        grpc=$(load_delegates | grep -o '"grpc"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -n "$((i+1))p" | sed 's/.*"\([^"]*\)"/\1/')
        api=$(load_delegates | grep -o '"api"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -n "$((i+1))p" | sed 's/.*"\([^"]*\)"/\1/')

        # Query swarm status
        local status
        status=$(curl -sSL --connect-timeout 3 --max-time 5 "${api}/swarm/status" 2>/dev/null || echo "")

        if [ -z "$status" ]; then
            echo "  ${name}: offline"
            i=$((i + 1))
            continue
        fi

        local agents reward_iotx delegate_cut
        agents=$(echo "$status" | grep -o '"agent_count":[0-9]*' | head -1 | cut -d: -f2 || echo "0")
        reward_iotx=$(echo "$status" | grep -o '"epoch_reward_iotx":[0-9.]*' | head -1 | cut -d: -f2 || echo "0")
        delegate_cut=$(echo "$status" | grep -o '"delegate_cut_pct":[0-9.]*' | head -1 | cut -d: -f2 || echo "10")

        agents=${agents:-1}
        [ "$agents" = "0" ] && agents=1

        local rate
        rate=$(awk "BEGIN {printf \"%.4f\", ${reward_iotx:-0} * (1 - ${delegate_cut:-10}/100) / $agents}")

        echo "  ${name}: ${agents} agents, ${reward_iotx} IOTX/epoch, ~${rate} IOTX/agent/epoch"

        local rate_int best_int
        rate_int=$(awk "BEGIN {printf \"%d\", $rate * 10000}")
        best_int=$(awk "BEGIN {printf \"%d\", $best_rate * 10000}")

        if [ "$rate_int" -gt "$best_int" ]; then
            best_rate="$rate"
            best_name="$name"
            best_grpc="$grpc"
        fi

        i=$((i + 1))
    done

    echo ""
    if [ -n "$best_name" ]; then
        log "Best delegate: ${best_name} (~${best_rate} IOTX/agent/epoch)"
        echo "$best_name" > "${AGENT_DIR}/delegate.name"
        echo "$best_grpc" > "${AGENT_DIR}/delegate.addr"
    else
        warn "No delegates responding. Configure manually with: $0 switch <delegate>"
    fi
}

# --- Setup ---

cmd_setup() {
    echo ""
    echo -e "${CYAN}  ioSwarm — Setup${NC}"
    echo ""

    mkdir -p "$AGENT_DIR"

    generate_wallet
    discover_delegates

    local agent_id="ioswarm-$(openssl rand -hex 4)"
    echo "$agent_id" > "${AGENT_DIR}/agent.id"

    local delegate_addr
    if [ -f "${AGENT_DIR}/delegate.addr" ]; then
        delegate_addr=$(cat "${AGENT_DIR}/delegate.addr")
    else
        delegate_addr="127.0.0.1:14689"
    fi

    local wallet_addr
    if [ -f "$WALLET_ADDR" ]; then
        wallet_addr=$(cat "$WALLET_ADDR")
    else
        wallet_addr=""
    fi

    cat > "$CONFIG_FILE" <<EOF
IOSWARM_COORDINATOR=${delegate_addr}
IOSWARM_AGENT_ID=${agent_id}
IOSWARM_WALLET=${wallet_addr}
IOSWARM_LEVEL=L2
IOSWARM_REGION=$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)
IOSWARM_REWARD_CONTRACT=${DEFAULT_CONTRACT}
IOSWARM_RPC=${DEFAULT_RPC}
EOF
    chmod 600 "$CONFIG_FILE"

    echo ""
    log "Setup complete!"
    log "  Agent ID:  ${agent_id}"
    log "  Wallet:    ${wallet_addr}"
    log "  Delegate:  ${delegate_addr}"
    log "  Level:     L2 (signature + nonce verification)"
    echo ""
    log "Start earning: $0 start"
    echo ""
}

# --- Start ---

cmd_start() {
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        log "ioSwarm agent is already running (PID $(cat "$PID_FILE"))"
        return 0
    fi

    if [ ! -f "$CONFIG_FILE" ]; then
        error "Not set up yet. Run: $0 setup"
        exit 1
    fi

    source "$CONFIG_FILE"

    local cmd=("$AGENT_BIN"
        "--coordinator=$IOSWARM_COORDINATOR"
        "--agent-id=$IOSWARM_AGENT_ID"
        "--level=${IOSWARM_LEVEL:-L2}"
        "--region=${IOSWARM_REGION:-default}"
    )

    [ -n "${IOSWARM_WALLET:-}" ] && cmd+=("--wallet=$IOSWARM_WALLET")
    [ -n "${IOSWARM_API_KEY:-}" ] && cmd+=("--api-key=$IOSWARM_API_KEY")

    log "Starting ioSwarm agent..."
    nohup "${cmd[@]}" > "$LOG_FILE" 2>&1 &

    local pid=$!
    echo "$pid" > "$PID_FILE"

    sleep 1
    if kill -0 "$pid" 2>/dev/null; then
        log "Agent running (PID ${pid})"
        log "Earning IOTX in the background..."
    else
        error "Failed to start. Check: $LOG_FILE"
        rm -f "$PID_FILE"
        exit 1
    fi
}

# --- Stop ---

cmd_stop() {
    if [ ! -f "$PID_FILE" ]; then
        log "Agent is not running"
        return 0
    fi

    local pid
    pid=$(cat "$PID_FILE")

    if kill -0 "$pid" 2>/dev/null; then
        kill "$pid"
        log "Agent stopped (PID ${pid})"
        log "Unclaimed rewards are safe on-chain."
    else
        log "Agent was not running (stale PID file)"
    fi
    rm -f "$PID_FILE"
}

# --- Status ---

query_claimable() {
    # Query on-chain claimable balance via eth_call
    local wallet="$1"
    local contract="${2:-$DEFAULT_CONTRACT}"
    local rpc="${3:-$DEFAULT_RPC}"

    if [ "$wallet" = "pending-derivation" ] || [ -z "$wallet" ]; then
        echo "0"
        return
    fi

    # claimable(address) selector = 0x402914f5
    # ABI-encode the address (left-pad to 32 bytes)
    local addr_clean
    addr_clean=$(echo "$wallet" | sed 's/^0x//' | tr '[:upper:]' '[:lower:]')
    local calldata="0x402914f5000000000000000000000000${addr_clean}"

    local result
    result=$(curl -sSL --connect-timeout 5 --max-time 10 "$rpc" \
        -H "Content-Type: application/json" \
        -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_call\",\"params\":[{\"to\":\"${contract}\",\"data\":\"${calldata}\"},\"latest\"],\"id\":1}" \
        2>/dev/null || echo "")

    if [ -z "$result" ]; then
        echo "0"
        return
    fi

    # Parse hex result → decimal wei → IOTX
    local hex_val
    hex_val=$(echo "$result" | grep -o '"result":"0x[0-9a-fA-F]*"' | sed 's/"result":"//;s/"//' || echo "0x0")

    if [ "$hex_val" = "0x" ] || [ -z "$hex_val" ]; then
        echo "0"
        return
    fi

    # Convert hex wei to IOTX (divide by 1e18)
    local dec_wei
    dec_wei=$(printf "%d" "$hex_val" 2>/dev/null || echo "0")
    awk "BEGIN {printf \"%.4f\", $dec_wei / 1000000000000000000}"
}

cmd_status() {
    local running="false"
    local pid=""

    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        running="true"
        pid=$(cat "$PID_FILE")
    fi

    local agent_id="" wallet="" delegate="" delegate_addr=""
    [ -f "${AGENT_DIR}/agent.id" ] && agent_id=$(cat "${AGENT_DIR}/agent.id")
    [ -f "$WALLET_ADDR" ] && wallet=$(cat "$WALLET_ADDR")
    [ -f "${AGENT_DIR}/delegate.name" ] && delegate=$(cat "${AGENT_DIR}/delegate.name")
    [ -f "${AGENT_DIR}/delegate.addr" ] && delegate_addr=$(cat "${AGENT_DIR}/delegate.addr")

    # Query on-chain claimable balance
    local claimable="0"
    local contract="${DEFAULT_CONTRACT}"
    local rpc="${DEFAULT_RPC}"
    if [ -f "$CONFIG_FILE" ]; then
        source "$CONFIG_FILE" 2>/dev/null || true
        contract="${IOSWARM_REWARD_CONTRACT:-$DEFAULT_CONTRACT}"
        rpc="${IOSWARM_RPC:-$DEFAULT_RPC}"
    fi
    if [ -n "$wallet" ] && [ "$wallet" != "pending-derivation" ]; then
        claimable=$(query_claimable "$wallet" "$contract" "$rpc")
    fi

    # Count tasks from log (rough estimate)
    local tasks_processed=0
    if [ -f "$LOG_FILE" ]; then
        tasks_processed=$(grep -c '"msg":"batch submitted"' "$LOG_FILE" 2>/dev/null || echo "0")
    fi

    cat <<EOF
{
  "running": ${running},
  "pid": "${pid}",
  "agent_id": "${agent_id}",
  "wallet": "${wallet}",
  "delegate": "${delegate}",
  "delegate_addr": "${delegate_addr}",
  "level": "L2",
  "claimable_iotx": "${claimable}",
  "tasks_processed": ${tasks_processed},
  "log_file": "${LOG_FILE}"
}
EOF
}

# --- Claim ---

cmd_claim() {
    if [ ! -f "$WALLET_KEY" ]; then
        error "No wallet found. Run: $0 setup"
        exit 1
    fi

    source "$CONFIG_FILE" 2>/dev/null || true
    local contract="${IOSWARM_REWARD_CONTRACT:-$DEFAULT_CONTRACT}"

    # Check claimable first
    local wallet
    wallet=$(cat "$WALLET_ADDR" 2>/dev/null || echo "")
    local claimable
    claimable=$(query_claimable "$wallet" "$contract")

    if [ "$claimable" = "0" ] || [ "$claimable" = "0.0000" ]; then
        log "Nothing to claim yet. Keep earning!"
        return 0
    fi

    log "Claimable: ${claimable} IOTX"
    log "Sending claim transaction..."

    # Pass private key via environment variable (not command line — avoids ps leak)
    IOSWARM_PRIVATE_KEY=$(cat "$WALLET_KEY") "$AGENT_BIN" claim \
        --contract="$contract"
}

# --- Switch delegate ---

cmd_switch() {
    local target="${1:-}"
    if [ -z "$target" ]; then
        error "Usage: $0 switch <delegate-name>"
        echo ""
        log "Available delegates:"
        discover_delegates
        return 1
    fi

    # Find delegate in registry
    local grpc=""
    local i=0
    while true; do
        local name
        name=$(load_delegates | grep -o '"name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -n "$((i+1))p" | sed 's/.*"\([^"]*\)"/\1/')
        [ -z "$name" ] && break

        if [ "$name" = "$target" ]; then
            grpc=$(load_delegates | grep -o '"grpc"[[:space:]]*:[[:space:]]*"[^"]*"' | sed -n "$((i+1))p" | sed 's/.*"\([^"]*\)"/\1/')
            break
        fi
        i=$((i + 1))
    done

    if [ -z "$grpc" ]; then
        error "Delegate '${target}' not found in registry"
        return 1
    fi

    echo "$target" > "${AGENT_DIR}/delegate.name"
    echo "$grpc" > "${AGENT_DIR}/delegate.addr"

    # Update config
    if [ -f "$CONFIG_FILE" ]; then
        sed -i.bak "s|^IOSWARM_COORDINATOR=.*|IOSWARM_COORDINATOR=${grpc}|" "$CONFIG_FILE"
        rm -f "${CONFIG_FILE}.bak"
    fi

    log "Switched to delegate: ${target} (${grpc})"

    # Restart if running
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        log "Restarting agent..."
        cmd_stop
        sleep 1
        cmd_start
    fi
}

# --- Upgrade ---

cmd_upgrade() {
    log "Upgrading ioswarm-agent..."

    local was_running="false"
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        was_running="true"
        cmd_stop
    fi

    local OS ARCH
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
    esac

    local VERSION
    VERSION=$(curl -sSL "https://api.github.com/repos/iotexproject/ioswarm-agent/releases/latest" \
        | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')

    curl -sSL -o "${AGENT_BIN}.new" \
        "https://github.com/iotexproject/ioswarm-agent/releases/download/${VERSION}/ioswarm-agent-${OS}-${ARCH}"
    chmod +x "${AGENT_BIN}.new"
    mv "${AGENT_BIN}.new" "$AGENT_BIN"

    # Also update delegate registry
    curl -sSL -o "$DELEGATES_FILE" \
        "https://raw.githubusercontent.com/iotexproject/ioswarm-agent/main/openclaw/delegates.json" 2>/dev/null || true

    log "Upgraded to ${VERSION}"

    if [ "$was_running" = "true" ]; then
        cmd_start
    fi
}

# --- Logs ---

cmd_logs() {
    if [ ! -f "$LOG_FILE" ]; then
        log "No logs yet. Start the agent first: $0 start"
        return 0
    fi
    tail -50 "$LOG_FILE"
}

# --- Main ---

case "${1:-help}" in
    setup)    cmd_setup ;;
    start)    cmd_start ;;
    stop)     cmd_stop ;;
    status)   cmd_status ;;
    claim)    cmd_claim ;;
    switch)   cmd_switch "${2:-}" ;;
    discover) discover_delegates ;;
    upgrade)  cmd_upgrade ;;
    logs)     cmd_logs ;;
    help|*)
        echo ""
        echo "  ioSwarm — Earn IOTX with idle compute"
        echo ""
        echo "  Usage: $0 <command>"
        echo ""
        echo "  Commands:"
        echo "    setup      Generate wallet and connect to best delegate"
        echo "    start      Start earning in the background"
        echo "    stop       Stop the agent"
        echo "    status     Show status and claimable balance (JSON)"
        echo "    claim      Claim accumulated IOTX rewards"
        echo "    switch     Switch to a different delegate"
        echo "    discover   Find the best-paying delegate"
        echo "    upgrade    Upgrade agent binary and delegate registry"
        echo "    logs       Show recent agent logs"
        echo ""
        ;;
esac
