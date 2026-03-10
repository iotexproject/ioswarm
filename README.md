# IOSwarm — Delegate AI Swarm for IoTeX

IOSwarm offloads transaction validation from IoTeX delegates to a swarm of lightweight agents. Agents perform signature verification, state validation, and (future) EVM execution — enabling delegates to process blocks faster while distributing compute costs.

## Architecture

```
Delegate's iotex-core (fork)             $5 VPS (anywhere)
┌────────────────────────┐              ┌──────────────────┐
│  iotex-core             │    gRPC      │  ioswarm-agent   │
│  ├── actpool            │◄────────────│  - connect        │
│  ├── statedb            │   :14689    │  - verify sigs    │
│  └── ioswarm/           │             │  - verify state   │
│       coordinator ──────┼────────────►│  - return results │
│       prefetcher        │  streaming  │  - heartbeat 10s  │
│       scheduler         │             │  - LRU cache      │
│       shadow comparator │             │  - Prometheus     │
│       reward distributor│             └──────────────────┘
└────────────────────────┘                     × N agents
```

## Components

| Directory | Description |
|-----------|-------------|
| `coordinator/` | Embeds into iotex-core. Polls actpool, prefetches state, dispatches tasks via gRPC, compares results in shadow mode, distributes epoch rewards. |
| `agent/` | Standalone binary. Connects to coordinator, validates transactions (L1: signature, L2: nonce/balance, L3: EVM stub), reports via Prometheus. |
| `portal/` | Web UI for agent operators. Register, manage API keys, monitor dashboard. Dark-themed, pure Go + SQLite. |

## Quick Start

```bash
# Run coordinator (test mode with mock data)
cd coordinator && go run ./cmd/testcoord

# Run agent (connects to coordinator)
cd agent && go build . && ./ioswarm-agent \
  --coordinator=localhost:14689 \
  --level=L2 \
  --agent-id=agent-001

# Run portal
cd portal && go run . --master-secret=your-secret
```

## Task Levels

- **L1**: Signature verification only (ECDSA P-256 range check, ~1μs/tx)
- **L2**: + Nonce replay protection + balance check (~2μs/tx)
- **L3**: + Full EVM execution (in development)

## Reward Distribution

```
Every epoch (~1 hour, 360 blocks):

Delegate epoch reward (e.g. 800 IOTX)
    ├── 10% → Delegate (operating costs)     = 80 IOTX
    └── 90% → Agent pool                     = 720 IOTX
              ├── weight = tasks × (accuracy ≥ 99.5% ? 1.2 : 1.0)
              ├── payout = pool × (my_weight / total_weight)
              └── min 10 tasks/epoch to qualify
```

## Testing

```bash
cd coordinator && go test ./...   # 46+ tests
cd agent && go test ./...         # 16+ tests
```

## Docker

```bash
# Build coordinator (test mode)
cd coordinator && docker build -f Dockerfile.testcoord -t ioswarm-coordinator .

# Build agent
cd agent && docker build -t ioswarm-agent .

# Build portal
cd portal && docker build -t ioswarm-portal .
```

## Coordinator Swarm API

| Endpoint | Purpose |
|----------|---------|
| `GET /swarm/status` | Overall swarm status |
| `GET /swarm/agents` | Connected agents with stats |
| `GET /swarm/leaderboard` | Agents ranked by tasks |
| `GET /swarm/epoch` | Current epoch stats |
| `GET /swarm/shadow` | Shadow mode accuracy |
| `GET /healthz` | Health check |

## Documentation

See [`coordinator/doc.md`](coordinator/doc.md) for detailed architecture, simulation results, and integration guide.

## License

Apache-2.0
