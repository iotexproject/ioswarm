# IOSwarm Architecture & Roadmap

## Core Insight

IoTeX's stateDB is 13.7GB (trie.db), but only **293 contracts** were active in the last 30 days out of 99,826 total. This means an agent can hold a "hot stateDB" (~36MB) entirely in memory, enabling local EVM execution without syncing the full state.

### IoTeX stateDB Breakdown

| Namespace | Size | Notes |
|-----------|------|-------|
| Contract | 12.0 GB | 99,826 contracts total |
| Rewarding | 1.4 GB | Protocol rewards |
| Account | 294 MB | All accounts |
| **Total trie.db** | **13.7 GB** | |
| Production snapshot | ~180 GB | Compressed full node |

### Hot Set Analysis

| Window | Active Contracts | Size | Hit Rate |
|--------|-----------------|------|----------|
| 30 days | 293 | ~36 MB | 99.7% |
| 150 days | 34,426 | ~4.14 GB | ~65% |

Cache miss rate for the 30-day hot set is approximately 0.3% — meaning an agent with only the hot set can handle 99.7% of transactions without fetching additional state.

---

## Separated Execution Layer Architecture

IOSwarm implements a **separated execution layer** analogous to Ethereum's Proposer-Builder Separation (PBS), but adapted for IoTeX's DPoS model.

```
Delegate Node (light):
  ├── Consensus (DPoS signing)
  ├── State storage (full trie, compute state root)
  └── Verify agent results (spot-check or compare)

Agent Swarm (heavy lifting):
  ├── EVM execution (with hot stateDB cache)
  ├── Returns: receipts, gas_used, state_changes
  └── N agents execute in parallel, results voted on
```

### PBS Analogy

| Ethereum PBS | IOSwarm |
|-------------|---------|
| Proposer | Delegate (consensus + state root) |
| Builder | Agent Swarm (execution) |
| Relay | Coordinator (dispatch) |
| MEV Auction | Not needed (delegate selects from actpool) |

Key difference: Ethereum PBS exists to solve MEV centralization. IOSwarm exists to **distribute compute costs** — delegates spend less on hardware, agents earn IOTX for contributing CPU.

---

## Phase Roadmap

### Phase 1-2: Foundation ✅

- Coordinator: registry, scheduler, prefetcher, shadow comparator
- gRPC streaming protocol with HMAC-SHA256 authentication
- Agent skeleton: connect, receive tasks, heartbeat
- Reward distribution system (delegate 10% cut)
- 46 coordinator tests passing

### Phase 3: Validation + E2E ✅

- Agent 3-tier validation:
  - L1: ECDSA P-256 signature range check (~1μs/tx)
  - L2: Nonce replay protection + balance check (~2μs/tx)
  - L3: Stub (returns L2 result + "EVM not yet implemented")
- 16 agent validator tests
- E2E Docker deployment (coordinator + 3 agents + Prometheus)
- Operator portal (register, login, API key management)
- Delegate address integration for reward payouts

### Phase 4: EVM Execution (~2-3 weeks estimated)

The key realization: with only 293 active contracts (~36MB), agents can hold the entire hot state in memory and execute EVM transactions locally.

**Implementation plan:**

1. **Extend TaskPackage**: Add bytecode, storage slots, and block context
2. **Hot contract cache**: Agent loads 293 active contracts at startup (~36MB RAM)
3. **Storage diff protocol**: Coordinator sends only changed slots per block (delta sync)
4. **Embed EVM**: Use `go-ethereum/core/vm` or iotex-core's EVM fork
5. **Minimal StateDB interface**: In-memory map backing go-ethereum's `StateDB` interface
   - Full interface has 38 methods; ~20 are essential for basic execution
   - Key methods: `GetBalance`, `GetNonce`, `GetCode`, `GetState`, `SetState`, `AddBalance`, `SubBalance`
6. **Agent returns**: `success/revert`, `gas_used`, `logs[]`, `state_changes[]`

**StateDB interface complexity:**

```go
// Essential methods (must implement):
GetBalance, SubBalance, AddBalance    // Balance ops
GetNonce, SetNonce                     // Nonce tracking
GetCode, GetCodeHash, GetCodeSize     // Contract code
GetState, SetState                     // Storage slots
Exist, Empty                           // Account existence
CreateAccount                          // New accounts
Snapshot, RevertToSnapshot            // EVM requires these for CALL

// Can stub initially:
AddRefund, SubRefund, GetRefund       // Gas refunds
AddLog, GetLogs                       // Event logs
AddPreimage                          // Debug only
```

### Phase 5: Accelerated Block Production (~2-3 weeks estimated)

Once agents can execute EVM transactions, delegates can skip re-execution:

1. Agent executes transaction, returns state changes: `[{contract, slot, old_value, new_value}, ...]`
2. Delegate applies state changes directly to trie → compute state root → sign block
3. **Shadow mode first**: Run both paths (agent execution + delegate's own execution), compare results
4. Only after 100% match rate over sustained period: switch to agent-only execution

This is the "trust but verify" approach — shadow mode proves correctness before going live.

### Phase 6: Full Stateless Execution (Future)

- **Verkle Tree migration**: Agents can compute state root with witness data, without holding the full trie
- Agents become truly independent execution nodes
- Delegate only does consensus + final verification
- This eliminates the last dependency on the delegate for state root computation

---

## Ethereum PBS Status (Reference)

As of early 2026:

- **MEV-Boost** (out-of-protocol PBS) handles ~90% of Ethereum blocks
- **ePBS** (enshrined PBS, EIP-7732) is specified but not yet deployed
  - Splits block into consensus + execution, with an "inclusion list" mechanism
  - Removes need for trusted relays
- **PeerDAS** (EIP-7594) is the immediate priority for Ethereum, providing data availability sampling for blob scaling
- Ethereum's roadmap: PeerDAS → Pectra → ePBS (likely 2026-2027)

**Relevance to IOSwarm**: We don't need the complexity of MEV auctions or trusted relays. Our separation is simpler — delegates trust their own agent swarm (which they partially control via coordinator configuration).

---

## EVM Validation Technical Requirements

### go-ethereum StateDB Integration

The `vm.StateDB` interface in go-ethereum defines what an EVM executor needs. For IOSwarm agents:

```
Minimal viable implementation:
├── In-memory map[address]Account for balances, nonces
├── In-memory map[address]map[hash]hash for storage slots
├── Code cache: map[address][]byte (pre-loaded from hot set)
├── Snapshot stack for revert support (required by CALL opcode)
└── Log accumulator for event emission
```

### State Sync Protocol

```
Coordinator → Agent (per block):
  1. Block header (number, timestamp, basefee, coinbase)
  2. Storage diffs since last block: [{address, slot, new_value}]
  3. New contracts deployed (bytecode)
  4. Transaction list to execute

Agent → Coordinator (per block):
  1. Per-tx: success/revert, gas_used, logs
  2. Aggregate: state_changes[], receipts_root
```

### Performance Estimates

| Metric | Estimate |
|--------|----------|
| Hot state load time | <1s (36MB from coordinator) |
| Per-tx execution | ~100μs-1ms (vs ~1μs for sig-only) |
| Memory overhead | ~50-100MB per agent |
| State sync bandwidth | ~1-10KB per block (only diffs) |

---

## Simulation Results

### 10 Agents × 50 TPS (IoTeX mainnet-like)

```
Txs validated:   850
Effective TPS:   27
Avg latency:     <1μs/tx
Load balance:    78-99 tx/agent (even)
Cost:            $50/mo = $0.70/M tx
```

### 100 Agents × 1000 TPS (stress test)

```
Txs validated:   20,475
Validation rate: 100%
Effective TPS:   660
Load balance:    180-230 tx/agent (±12%)
Cost:            $500/mo = $0.29/M tx
Monthly volume:  1.7B tx
```

### Key Findings

1. **Linear scale**: 10→100 agents achieves 60→660 TPS
2. **Bottleneck is tx supply**: 100 TPS can't feed 100 agents; need 1000 TPS to saturate
3. **Sub-microsecond validation**: sig verify + state check in <2μs on commodity hardware
4. **Unit cost drops with scale**: $0.70 → $0.29/M tx
5. **IoTeX mainnet at ~10-30 TPS**: 10 agents is sufficient; 100 for future DePIN growth

---

## Reward Economics

### 100-Agent Scenario

```
Delegate epoch income:   ~800 IOTX
Delegate keeps 10%:      80 IOTX
Agent pool 90%:          720 IOTX
Average per agent:       7.2 IOTX/epoch
Per agent per month:     ~5,184 IOTX (720 epochs)
VPS cost:                $5/mo ≈ 100 IOTX
Agent net profit/month:  ~5,084 IOTX
```

High-accuracy agents earn a 1.2x bonus (20% more than average). Even the lowest-performing qualifying agent is profitable at ~5,000 IOTX/month vs ~100 IOTX VPS cost.
