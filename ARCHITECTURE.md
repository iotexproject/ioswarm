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

---

## IOSwarm vs Ethereum PBS — Gap Analysis

### What Phase 4 Achieves

| PBS Feature | Ethereum | IOSwarm (Phase 4) |
|-------------|----------|-------------------|
| Execution separation | Builder constructs block | Agent executes EVM, returns state diffs |
| Result verification | Relay validates block | Shadow mode compares agent vs delegate |
| Task dispatch | Builder marketplace via relay | Coordinator dispatches via gRPC |
| Reward distribution | Builder bids for block space | Epoch-based proportional rewards |

### What's Still Missing

| Feature | Ethereum PBS | IOSwarm Status | Priority |
|---------|-------------|----------------|----------|
| State root computation | Builder produces complete payload with state root | Agent returns diffs, delegate still does trie ops | HIGH — needs Verkle Tree (Phase 6) |
| MEV auction | Builders bid for block construction rights | Not needed — IoTeX ~10-30 TPS, minimal MEV | LOW |
| Commitment scheme | Proposer signs blinded block header | No cryptographic commitment | MEDIUM |
| Slashing / auto-kick | Equivocation → slash 32 ETH | Manual API key revocation only | HIGH — see below |
| Censorship resistance | Inclusion lists (ePBS) | Not needed — delegate self-selects txs | LOW |
| Multi-builder competition | Multiple builders compete per slot | Agents cooperate (split workload) | LOW |

### Conclusion

Phase 4 achieves the most valuable PBS feature: **execution separation**. Missing pieces are either IoTeX-irrelevant (MEV, inclusion lists) or future work (Verkle, slashing).

---

## Agent Accountability: Auto-Kick & Slashing Design

### Problem

Currently, if an agent misbehaves (returns wrong results, goes slow, acts selectively), the only recourse is manual API key revocation via the portal. We need automated detection and punishment.

### Reference: How Ethereum/Cosmos Handle This

**Ethereum slashing** (2 offenses only):
- Proposer equivocation: signing two blocks for same slot → lose 1/32 of stake immediately + correlation penalty up to 100% if coordinated
- Attester equivocation: conflicting votes → same penalties
- Whistleblower who submits proof gets 1/512 of slashed stake as reward

**Cosmos slashing** (simpler model):
- Double signing → 5% slash + permanent jail ("tombstoning")
- Downtime (missing >50% of blocks in a window) → 0.01% slash + temporary jail
- Sliding window (10,000 blocks) tracks liveness

**ePBS** (EIP-7732, not yet deployed):
- Builder fails to reveal payload → forfeits bid (unconditional payment to proposer)
- No traditional slashing — relies on economic incentives
- Payload Timeliness Committee (PTC) votes on whether payload was delivered on time

### IOSwarm Auto-Kick Design

#### Detection Mechanisms

1. **Shadow mode comparison** (already exists): Agent EVM results vs delegate execution. Wrong result = hard fault.
2. **Challenge tasks** (canary): 1-5% of tasks have pre-computed correct answers. Failed challenge = immediate flag. Low overhead, high confidence.
3. **Cross-validation**: Same task sent to N agents (e.g., 3). If 2/3 agree and 1 disagrees, outlier flagged. More expensive but robust.
4. **Latency monitoring**: Track P50/P95 per agent vs population median. Agent >3x slower = soft fault.
5. **Completion rate**: Accept-vs-complete ratio. Population average 95%, agent at 60% = selective execution.
6. **Heartbeat liveness**: 3 missed heartbeats = offline. Remove from active pool.

#### Progressive Punishment (4 Tiers)

```
Tier 0 — MONITORING (baseline)
  Track: accuracy, latency, completion rate, heartbeat
  Rolling windows: 1-hour and 24-hour
  No action, just data collection

Tier 1 — WARNING
  Trigger: 3 soft faults in 1h OR 1 failed challenge task
  Action: reduce task allocation priority, notify operator
  Auto-clear: 6 hours clean → back to Tier 0
  Escalation: 3 warnings in 24h → Tier 2

Tier 2 — REWARD REDUCTION
  Trigger: escalation from Tier 1 OR accuracy < 90% sustained 1h
  Action: 50% reward cut, max 1 concurrent task, 10% challenge rate
  Auto-clear: 24h clean → back to Tier 1
  Escalation: any hard fault OR 48h without improvement → Tier 3

Tier 3 — SUSPENSION (auto-kick)
  Trigger: escalation from Tier 2 OR 1 hard fault (provably wrong EVM result)
  Action: remove from active pool, stop tasks, freeze rewards
  Re-entry: after cooldown (1h), agent re-registers + passes health check
  Escalation: 3 suspensions in 7 days → Tier 4

Tier 4 — PERMANENT BAN
  Trigger: escalation from Tier 3 OR proven malicious behavior
  Action: revoke API key, ban agent identity, forfeit frozen rewards
  Appeal: manual review by delegate operator
```

#### Practical Thresholds

| Metric | Warning (Tier 1) | Suspension (Tier 3) |
|--------|-----------------|---------------------|
| EVM accuracy (shadow match) | <95% in 1h | <80% in 1h |
| Challenge task failure | 1 failure | 2 failures in 24h |
| Response latency vs median | >2x P95 | >5x P95 |
| Task completion rate | <85% | <60% |
| Heartbeat misses (consecutive) | 3 | 5 |

#### Implementation in Coordinator

New component: `AgentAccountability` in coordinator:
- Per-agent state machine tracking current tier + fault log (circular buffer)
- Runs alongside existing `RewardDistributor` and `ShadowComparator`
- Challenge task injection in `pollAndDispatch()` (1-5% of tasks)
- Tier transitions logged immutably for auditability
- Exposed via SwarmAPI: `GET /swarm/agents/{id}/accountability`

#### Correlation Penalty (from Polkadot)

If multiple agents from the same operator misbehave simultaneously:
```
penalty_multiplier = min(3 × (concurrent_offenders / total_agents), 1.0)
```
Individual bug = mild penalty. Coordinated attack = maximum penalty.

#### Future: On-Chain Staking

For full economic security (not needed for MVP):
- Agents stake IOTX to participate (e.g., 1000 IOTX)
- Tier 4 slashes stake (10-100% depending on severity)
- Slashed funds go to delegate or burn
- Staking contract: `IOSwarmStaking.sol` on IoTeX mainnet
