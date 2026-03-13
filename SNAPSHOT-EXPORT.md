# IOSWSNAP Snapshot Export Guide

## Tool: `l4baseline`

Source: `iotex2026/ioswarm/cmd/l4baseline/main.go`

Uses BoltDB directly (not iotex-core's DB layer), so it's a standalone binary with no iotex-core dependency at runtime.

## Build

```bash
cd /Users/raullenstudio/work/iotex2026
# For local Mac
go build -o l4baseline ./ioswarm/cmd/l4baseline

# For delegate (linux/amd64)
GOOS=linux GOARCH=amd64 go build -o l4baseline ./ioswarm/cmd/l4baseline
scp l4baseline root@delegate.goodwillclaw.com:/tmp/
```

## Usage

### Inspect trie.db (stats only, no export)

```bash
./l4baseline --source /var/data/trie.db --stats
```

### Export full baseline snapshot (Account + Code + Contract)

```bash
./l4baseline --source /var/data/trie.db --output baseline.snap.gz
```

Output: ~1.4 GB gzip compressed, takes ~5-10 min on delegate.

### Export Account + Code only (smaller, sufficient for L4)

```bash
./l4baseline --source /var/data/trie.db --output acctcode.snap.gz --namespaces Account,Code
```

Output: ~209 MB gzip compressed. Contract namespace gets filled in via state diffs at runtime.

## On the Delegate

trie.db location: `/root/iotex-var/data/trie.db` (~45 GB)

The tool opens BoltDB in read-only mode, safe to run while iotex-core is running.

### Existing snapshots (as of 2026-03-12)

| File | Size | Namespaces | Height |
|------|------|-----------|--------|
| `baseline.snap.gz` | 1.4G | Account+Code+Contract | 46,006,460 |
| `acctcode.snap.gz` | 209M | Account+Code | 46,006,460 |

Location on delegate: `/root/iotex-var/data/`
Local copy: `/Users/raullenstudio/work/ioswarm-agent/`

## IOSWSNAP Format

```
[gzip compressed]
  Header:  "IOSWSNAP" (8 bytes) + version (uint32) + height (uint64)
  Entries: [0x01 + ns_len(uint8) + namespace + key_len(uint32) + key + val_len(uint32) + value]*
  End:     0x00
  Trailer: entry_count (uint64) + sha256 (32 bytes) + "SNAPEND\0" (8 bytes)
```

SHA256 covers header + entries + end marker. Trailer is NOT part of the digest.

## Loading in Agent

```bash
./ioswarm-agent \
  --level=L4 \
  --snapshot=./acctcode.snap.gz \
  --datadir=/tmp/l4state \
  ...
```

Agent loads snapshot into BoltDB, then connects to coordinator's `StreamStateDiffs` gRPC to catch up from snapshot height + 1.

## Known Issues

- `baseline.snap.gz` (full) causes "read value: unexpected EOF" when loading — under investigation
- `acctcode.snap.gz` (Account+Code only) loads successfully; Contract namespace fills in via state diffs
- For now, use `acctcode.snap.gz` for L4 bootstrap
