# Powerloom Decentralized Sequencer Validator (DSV) Protocol - Meta Reference

**Document Status**: Authoritative Reference (November 2025)  
**Purpose**: Single entry point for understanding the complete DSV protocol workflow, architecture, and design decisions  
**Audience**: Developers, AI assistants, system architects

---

## Table of Contents

1. [Protocol Overview](#protocol-overview)
2. [End-to-End Workflow](#end-to-end-workflow)
3. [Component Architecture](#component-architecture)
4. [Key Design Decisions](#key-design-decisions)
5. [Deployment Configurations](#deployment-configurations)
6. [Common Issues & Solutions](#common-issues--solutions)
7. [Related Documentation](#related-documentation)

---

## Protocol Overview

### What is DSV?

The Decentralized Sequencer Validator (DSV) protocol replaces centralized sequencer infrastructure with a decentralized P2P network where:

- **Snapshotters** compute blockchain state snapshots (e.g., Uniswap V3 pool states)
- **Validators** collect, validate, and reach consensus on snapshots
- **Consensus** is achieved through two-level aggregation across validator network
- **Finalization** occurs on-chain via protocol state smart contracts

### Core Value Proposition

1. **Decentralization**: No single point of failure
2. **Byzantine Fault Tolerance**: Majority consensus across validators
3. **Data Integrity**: Complete audit trail from submission to on-chain finalization
4. **Scalability**: Parallel processing with Redis-based coordination

---

## End-to-End Workflow

### Complete Data Flow

```
┌─────────────────────────────────────────────────────────────────────────┐
│ PHASE 1: Snapshot Submission                                           │
└─────────────────────────────────────────────────────────────────────────┘

Snapshotter Node (Computes State)
    ↓
Local Collector (Signs with EIP-712)
    ↓
P2P Gossipsub: /powerloom/snapshot-submissions/all
    ↓
┌─────────────────────────────────────────────────────────────────────────┐
│ PHASE 2: Collection & Validation                                        │
└─────────────────────────────────────────────────────────────────────────┘

P2P Gateway (Receives submissions)
    ↓
Redis: submissionQueue (LIST)
    ↓
Dequeuer (Validates, deduplicates)
    ↓
Redis: {protocol}:{market}:epoch:{epochId}:submissions:ids (ZSET - deterministic)
Redis: {protocol}:{market}:epoch:{epochId}:submissions:data (HASH - deterministic)
    ↓
┌─────────────────────────────────────────────────────────────────────────┐
│ PHASE 3: Local Finalization (Level 1 Aggregation)                        │
└─────────────────────────────────────────────────────────────────────────┘

Event Monitor detects EpochReleased event
  ↓
Event Monitor queries contract for submission window duration:
  - Legacy: snapshotSubmissionWindow(dataMarket) → 30 seconds (originally for centralized sequencer, now repurposed for snapshot CID collection in decentralized architecture when commit/reveal is disabled)
  - New: getDataMarketSubmissionWindowConfig(dataMarket) → dynamic config (P1=45s, PN=45s)
  ↓
Submission window opens → Snapshotters submit snapshot CIDs via P2P
  ↓
Window closes (after duration expires) → Level 1 finalization begins
  ↓
Finalizer Workers (Process in parallel - aggregating collected snapshot CIDs)
    ↓
Redis: {protocol}:{market}:batch:part:{epoch}:{partID} (HASH)
    ↓
Aggregator Level 1 (Combines worker parts)
    ↓
Redis: {protocol}:{market}:finalized:{epochId} (STRING)
IPFS: /ipfs/{cid} (Finalized batch JSON)
    ↓
┌─────────────────────────────────────────────────────────────────────────┐
│ PHASE 4: Network Consensus (Level 2 Aggregation)                       │
└─────────────────────────────────────────────────────────────────────────┘

Aggregator Level 1 completes → Writes to aggregation stream
    ↓
Redis Stream: {protocol}:{market}:stream:aggregation:notifications
    ↓
P2P Gateway (Broadcasts local finalized batch)
    ↓
P2P Gossipsub: /powerloom/finalized-batches/all
    ↓
Other Validators Receive → Write to aggregation stream
    ↓
Redis: {protocol}:{market}:incoming:batch:{epochId}:{validatorId} (STRING)
    ↓
Stream Consumer (Aggregator) receives messages
    ↓
Aggregator Level 2 starts aggregation window (30s default)
    ↓
Collects batches during window (local + remote)
    ↓
Window expires → Level 2 aggregation computes consensus
    ↓
Redis: {protocol}:{market}:batch:aggregated:{epochId} (STRING)
IPFS: /ipfs/{cid} (Aggregated consensus batch)
    ↓
┌─────────────────────────────────────────────────────────────────────────┐
│ PHASE 5: On-Chain Submission                                            │
└─────────────────────────────────────────────────────────────────────────┘

Level 2 Aggregation Completes
    ↓
VPA Client checks priority assignment
    ↓
Waits for submission window (if needed)
    ↓
Checks if higher priority validators submitted
    ↓
Submits via relayer-py service
    ↓
relayer-py queues and submits transaction
    ↓
On-Chain: Protocol State Contract (via VPA)
    ↓
Epoch Finalized ✅
```

### State Transitions

See [`PROTOCOL_STATE_TRANSITIONS.md`](./PROTOCOL_STATE_TRANSITIONS.md) for detailed state machine documentation.

**Key States:**
- `SUBMITTED` → Snapshot published to P2P network
- `COLLECTED` → Received and queued by validator
- `PROCESSED` → Validated and deduplicated
- `FINALIZED` → Level 1 aggregation complete (local validator view)
- `AGGREGATED` → Level 2 aggregation complete (network consensus)
- `ON_CHAIN` → Committed to protocol state contract

---

## Component Architecture

### Core Components

#### 1. **P2P Gateway** (`cmd/p2p-gateway/main.go`)
- **Role**: Libp2p network interface
- **Responsibilities**:
  - Subscribes to `/powerloom/snapshot-submissions/all` (receives snapshots)
  - Publishes to `/powerloom/finalized-batches/all` (broadcasts local batches)
  - Receives validator batches from network
- **Communication**: Redis queues (decouples P2P from processing)

#### 2. **Dequeuer** (`cmd/dequeuer/main.go` or `cmd/unified/main.go`)
- **Role**: Validates and processes submissions
- **Responsibilities**:
  - Reads from `submissionQueue`
  - Validates EIP-712 signatures
  - Deduplicates submissions
  - Tracks epoch progress
- **Output**: Processed submissions stored in Redis

#### 3. **Finalizer** (`cmd/finalizer/main.go` or `cmd/unified/main.go`)
- **Role**: Creates finalized batches from submissions
- **Responsibilities**:
  - Monitors epoch windows
  - Creates batch parts (parallel workers)
  - Generates Merkle roots
  - Stores batches in IPFS
- **Output**: `{protocol}:{market}:finalized:{epochId}` + IPFS CID

#### 4. **Aggregator** (`cmd/aggregator/main.go`)
- **Role**: Two-level batch aggregation
- **Responsibilities**:
  - **Level 1**: Combines worker parts into local finalized batch
    - Reads from `{protocol}:{market}:aggregationQueue:level1` (Level 1 queue)
    - Writes local batch to stream after completion
  - **Level 2**: Aggregates validator batches into network consensus
    - Consumes from `{protocol}:{market}:stream:aggregation:notifications` (unified stream path)
    - Manages configurable aggregation windows (default 30s)
    - Collects batches during window (local + remote validators)
    - Computes consensus after window expires
  - Handles validator participation tracking
- **Output**: `{protocol}:{market}:batch:aggregated:{epochId}` + IPFS CID

**Aggregation Window Behavior:**
- **Current (Commit/Reveal Disabled)**: Fixed duration (`AGGREGATION_WINDOW_SECONDS`, default 30s)
  - First batch arrival (local or remote) starts timer
  - Additional batches collected during window
  - Window expiration triggers Level 2 aggregation
  - Acts as "sink" for collecting validator batches before consensus computation
- **Future (Commit/Reveal Enabled)**: Dynamic timing tied to Validator Voting Window
  - Level 2 aggregation triggers after Validator Vote Reveal Window closes
  - Window duration calculated from contract configuration
  - Ensures validators have revealed votes before consensus computation

#### 5. **Event Monitor** (`cmd/event-monitor/main.go` or `cmd/unified/main.go`)
- **Role**: Epoch window management
- **Responsibilities**:
  - Opens/closes epoch submission windows
  - Tracks epoch lifecycle
  - Publishes state changes
- **Output**: Epoch status in Redis

#### 6. **VPA Client** (`pkgs/vpa/client.go`)
- **Role**: On-chain proposer selection
- **Responsibilities**:
  - Checks if current validator is proposer for epoch
  - Submits aggregated batches to protocol state contract
  - Handles transaction signing and submission

### Supporting Infrastructure

#### **Redis** (State Coordination)
- **Purpose**: Decoupled communication between components
- **Key Patterns**:
  - Queues: `submissionQueue`, `{protocol}:{market}:aggregationQueue:level1` (Level 1 only)
  - Streams: `{protocol}:{market}:stream:aggregation:notifications` (Level 2 unified path)
  - State: `{protocol}:{market}:finalized:{epochId}`, `{protocol}:{market}:batch:aggregated:{epochId}`
  - Incoming Batches: `{protocol}:{market}:incoming:batch:{epochId}:{validatorId}`
  - Metrics: `{protocol}:{market}:metrics:batches:timeline`, `{protocol}:{market}:metrics:epoch:{id}:info`
- **See**: [`REDIS_KEYS.md`](./REDIS_KEYS.md)

**Unified Aggregation Path:**
- Both local batches (after Level 1) and remote batches (from P2P Gateway) write to the same aggregation stream
- Stream consumer (Aggregator) processes all messages uniformly
- Eliminates dual-path complexity and ensures consistent behavior

#### **IPFS** (Data Availability)
- **Purpose**: Immutable storage for finalized and aggregated batches
- **Configurations**:
  - **Local IPFS**: Docker service with `/data/ipfs` mount (`./dsv.sh start --with-ipfs`)
  - **External IPFS**: Remote IPFS node via HTTP API (`./dsv.sh start --with-vpa`)
- **See**: [`IPFS_CLIENT_ARCHITECTURE.md`](./IPFS_CLIENT_ARCHITECTURE.md)

#### **P2P Network** (Gossipsub)
- **Topics**:
  - `/powerloom/snapshot-submissions/all`: Snapshot submissions
  - `/powerloom/finalized-batches/all`: Validator batch broadcasts
- **Bootstrap Nodes**: Peer discovery and mesh formation

---

## Key Design Decisions

### 1. Two-Level Aggregation

**Why**: Separates local processing from network consensus

- **Level 1 (Internal)**: Combines parallel worker parts into single validator view
  - Enables horizontal scaling of finalizer workers
  - Creates consistent local batch structure
- **Level 2 (Network)**: Aggregates validator views into consensus
  - Handles Byzantine fault tolerance
  - Creates network-wide agreement

**See**: [`AGGREGATOR_WORKFLOW.md`](./AGGREGATOR_WORKFLOW.md)

### 2. Redis-Based Decoupling

**Why**: Prevents P2P networking from blocking processing

- P2P Gateway only handles network I/O
- All processing happens via Redis queues
- Enables independent scaling of components
- Simplifies error handling and retries

### 3. Conditional IPFS Upload Strategy

**Why**: Optimizes for different deployment scenarios

- **Local IPFS**: Uses `Unixfs().Add()` - IPFS node handles storage efficiently
- **External IPFS**: Uses direct HTTP POST - avoids temp files in aggregator container
- **Detection**: Based on hostname (`ipfs:5001` vs `/dns/hostname/tcp/5001`)

**See**: [`IPFS_CLIENT_ARCHITECTURE.md`](./IPFS_CLIENT_ARCHITECTURE.md)

### 4. Namespaced Redis Keys

**Why**: Multi-market support and isolation

- Format: `{protocol}:{market}:{key}`
- Prevents cross-contamination between markets
- Enables parallel processing of multiple markets
- Centralized key generation in `pkgs/redis/keys.go`

### 5. Epoch-Based Windows

**Why**: Deterministic batch boundaries

- Each epoch has open/close windows
- Submissions collected during open window
- Finalization occurs after window closes
- Enables predictable consensus timing

---

## Deployment Configurations

### Local IPFS Setup

**Command**: `./dsv.sh start --with-ipfs --with-vpa`

**Configuration**:
- IPFS runs as Docker service alongside DSV components
- Data mounted at `/data/ipfs` in IPFS container
- Aggregator connects to `ipfs:5001` (local service)
- Uses `Unixfs().Add()` for efficient local storage

**Use Case**: Development, single-node deployments, testing

### External IPFS Setup

**Command**: `./dsv.sh start --with-vpa`

**Configuration**:
- IPFS runs on separate infrastructure (load-balanced, multiple nodes)
- Aggregator connects via HTTP API (`/dns/hostname/tcp/5001` or `http://hostname:5001`)
- Uses direct HTTP POST to avoid local temp files
- Requires external IPFS node to be accessible

**Use Case**: Production deployments, shared IPFS infrastructure

### Environment Variables

**Key Variables**:
- `IPFS_HOST`: IPFS API endpoint (default: `127.0.0.1:5001`)
  - Local: `ipfs:5001` or `127.0.0.1:5001`
  - External: `/dns/hostname/tcp/5001` or `http://hostname:5001`
- `IPFS_PATH`: **Only for IPFS service container** - repository location
- `SEQUENCER_ID`: Unique validator identifier
- `PROTOCOL_STATE`: Protocol namespace (e.g., `powerloom`)
- `DATA_MARKET`: Market namespace (e.g., `uniswap-v3`)

**See**: `localenvs/env.validator2.devnet` for example configuration

---

## Common Issues & Solutions

### Issue 1: "no space left on device" during IPFS upload

**Symptoms**:
- Aggregator logs show: `open /data/ipfs/blocks/.temp/temp-XXXXX: no space left on device`
- Occurs during Level 1 or Level 2 aggregation

**Root Causes**:
1. **External IPFS with local temp files**: Kubo library creating temp files in aggregator container
   - **Solution**: Ensure `IPFS_HOST` points to external host (not `127.0.0.1` or `ipfs:5001`)
   - **Verification**: Check logs show `local=false` in IPFS client initialization

2. **IPFS node StorageMax exceeded**: IPFS daemon configured with low storage limit
   - **Solution**: Update `Datastore.StorageMax` in IPFS config (see `docker-compose.separated.yml`)
   - **Verification**: `docker exec ipfs-ipfs-1 ipfs config Datastore.StorageMax`

3. **Inode exhaustion**: Filesystem out of inodes (even with free disk space)
   - **Symptoms**: `df -i` shows 100% inode usage
   - **Solution**: Run `ipfs repo gc` to free unpinned blocks
   - **Prevention**: Add `ipfs repo gc` to cron job in IPFS container

**See**: [`IPFS_CLIENT_ARCHITECTURE.md`](./IPFS_CLIENT_ARCHITECTURE.md) - Troubleshooting section

### Issue 2: Level 1 succeeds but Level 2 fails

**Symptoms**:
- Level 1 aggregation completes successfully
- Level 2 aggregation fails with IPFS errors

**Root Cause**: IPFS node-side issues (StorageMax, inodes) affect larger aggregated batches

**Solution**: Address IPFS node configuration (see Issue 1)

### Issue 3: Unclear log messages

**Symptoms**: Logs don't indicate which aggregation level failed or which epoch ID

**Solution**: Enhanced logging added in `main.go`:
- Level 1: `✅ LEVEL 1: Stored finalized batch in IPFS` / `❌ LEVEL 1: Failed to store...`
- Level 2: `✅ LEVEL 2: Stored aggregated batch in IPFS` / `❌ LEVEL 2: Failed to store...`
- Both include `epoch` and `level` fields

### Issue 4: Aggregated batches not appearing

**Symptoms**: Level 2 aggregation completes but `batch:aggregated:{epochId}` not found

**Root Causes**:
1. **Wrong key pattern**: Using non-namespaced keys
   - **Solution**: Use `{protocol}:{market}:batch:aggregated:{epochId}`
2. **TTL expired**: Aggregated batches have 24-hour TTL
   - **Solution**: Check Redis TTL: `TTL {protocol}:{market}:batch:aggregated:{epochId}`
3. **Aggregation window timing**: Level 2 requires 30-second collection window
   - **Solution**: Check aggregator logs for window expiration timing

### Issue 5: Validator batches not received

**Symptoms**: Level 2 aggregation shows only local batch

**Root Causes**:
1. **P2P network connectivity**: Validators not connected
   - **Solution**: Check P2P Gateway logs for peer connections
   - **Verification**: `./dsv.sh monitor` shows connected peers
2. **Bootstrap node misconfiguration**: Validators can't discover each other
   - **Solution**: Ensure all validators use same bootstrap nodes
3. **Topic subscription**: P2P Gateway not subscribed to finalized-batches topic
   - **Solution**: Check P2P Gateway logs for topic subscription

---

## Related Documentation

### Detailed Specifications

- **[AGGREGATOR_WORKFLOW.md](./AGGREGATOR_WORKFLOW.md)**: Two-level aggregation system details
- **[PROTOCOL_STATE_TRANSITIONS.md](./PROTOCOL_STATE_TRANSITIONS.md)**: Complete state machine documentation
- **[IPFS_CLIENT_ARCHITECTURE.md](./IPFS_CLIENT_ARCHITECTURE.md)**: IPFS client design and troubleshooting
- **[DSV_CONSENSUS_FLOW.md](./DSV_CONSENSUS_FLOW.md)**: Consensus mechanism implementation
- **[PHASE_3_DECENTRALIZED_CONSENSUS_ARCHITECTURE.md](./PHASE_3_DECENTRALIZED_CONSENSUS_ARCHITECTURE.md)**: Phase 3 architecture overview

### Code References

- **Redis Keys**: [`REDIS_KEYS.md`](./REDIS_KEYS.md)
- **Monitoring Guide**: [`MONITORING_GUIDE.md`](./MONITORING_GUIDE.md)
- **Visual Timeline**: [`imgs/sequencing-timeline.png`](./imgs/sequencing-timeline.png)
- **Main Components**:
  - Aggregator: `cmd/aggregator/main.go`
  - IPFS Client: `pkgs/ipfs/client.go`
  - Redis Keys: `pkgs/redis/keys.go`
  - Docker Compose: `docker-compose.separated.yml`

### High-Level Meta Documents

- **[phase_overall_meta_decentralized_sequencers.md](./phase_overall_meta_decentralized_sequencers.md)**: Project background and research
- **[TECHNICAL_REFERENCE.md](./TECHNICAL_REFERENCE.md)**: Technical implementation details

---

## Quick Reference: Component Responsibilities

| Component | Input | Output | Key Redis Keys |
|-----------|-------|--------|----------------|
| **P2P Gateway** | P2P messages | Redis queues | `submissionQueue`, `outgoing:broadcast:batch` |
| **Dequeuer** | `submissionQueue` | Processed submissions | `{protocol}:{market}:epoch:{id}:processed` |
| **Finalizer** | Processed submissions | Finalized batches | `{protocol}:{market}:finalized:{epochId}` |
| **Aggregator L1** | Worker parts | Local finalized batch | `{protocol}:{market}:finalized:{epochId}` |
| **Aggregator L2** | Validator batches | Network consensus | `{protocol}:{market}:batch:aggregated:{epochId}` |
| **Event Monitor** | Epoch lifecycle | Window status | `{protocol}:{market}:epoch:{id}:window` |
| **VPA Client** | Aggregated batches | On-chain transactions | Protocol state contract |

---

## Quick Reference: Deployment Commands

```bash
# Local IPFS setup (development)
./dsv.sh start --with-ipfs --with-vpa

# External IPFS setup (production)
./dsv.sh start --with-vpa

# Monitoring
./dsv.sh monitor

# Stop
./dsv.sh stop
```

---

**Last Updated**: November 2025  
**Maintainer**: DSV Development Team  
**Status**: Production System

