# Redis Key Architecture for Separated Container Design

## Overview
This document defines the Redis keys used for inter-container communication in the separated architecture where each container has a single responsibility.

## Container Communication Flow

```
P2P Gateway ←→ Redis ←→ Dequeuer
                ↓
           Finalizer
                ↓
           Aggregator
                ↓
           P2P Gateway
```

## Key Definitions

### State Tracker Keys

#### Active Epoch Management
- `ActiveEpochs()` - SET: Currently active epoch IDs
  - Written by: Event Monitor
  - Read by: State Tracker (deterministic aggregation)
  - Purpose: Direct access to active epochs instead of SCAN operations
  - Format: Set of epoch IDs

- `EpochValidators({epochId})` - SET: Validator IDs participating in each epoch
  - Written by: Event Monitor/Aggregator
  - Read by: State Tracker (deterministic aggregation)
  - Purpose: Efficient validator detection per epoch
  - Format: Set of validator IDs

- `EpochProcessed({epochId})` - SET: Processed submission IDs per epoch
  - Written by: Dequeuer
  - Read by: State Tracker (deterministic aggregation)
  - Purpose: Fast submission counting without expensive operations
  - Format: Set of submission IDs

### P2P Gateway Keys

#### Incoming (from network to Redis) - ALL NAMESPACED
- `{protocol}:{market}:submissionQueue` - LIST: Raw P2P submissions from network
  - Written by: P2P Gateway
  - Read by: Dequeuer
  - Format: JSON encoded P2PSnapshotSubmission

- `{protocol}:{market}:incoming:batch:{epochId}:{validatorId}` - STRING: Received batch from validator
  - Written by: P2P Gateway
  - Read by: Aggregator
  - TTL: 30 minutes
  - Format: JSON encoded FinalizedBatch

- `{protocol}:{market}:aggregation:queue` - LIST: Epochs ready for Level 2 aggregation
  - Written by: P2P Gateway (when batch received)
  - Read by: Aggregator (Level 2)
  - Format: epochId as string

- `validator:active:{validatorId}` - STRING: Active validator tracking
  - Written by: P2P Gateway
  - Read by: Aggregator (for metrics)
  - TTL: 5 minutes
  - Format: Unix timestamp

#### Outgoing (from Redis to network) - NAMESPACED
- `{protocol}:{market}:outgoing:broadcast:batch` - LIST: Batches to broadcast
  - Written by: Aggregator (after Level 1 aggregation)
  - Read by: P2P Gateway
  - Format: JSON with type and data fields

### Dequeuer Keys

- `processingSubmission:{id}` - STRING: Submission being processed
  - Written by: Dequeuer
  - Read by: Dequeuer (for recovery)
  - TTL: 5 minutes

- `{protocol}:{market}:processed:{sequencerId}:{submissionId}` - HASH: Validated submission
  - Written by: Dequeuer
  - Read by: Finalizer
  - Format: Submission details as hash

- `{protocol}:{market}:epoch:{epochId}:processed` - SET: Submission IDs for epoch
  - Written by: Dequeuer
  - Read by: Finalizer

### Event Monitor Keys - NAMESPACED

- `{protocol}:{market}:epoch:{epochId}:window` - STRING: Submission window status
  - Written by: Event Monitor
  - Read by: Dequeuer, Finalizer
  - Values: "open" or "closed"
  - TTL: 1 hour after close

- `{protocol}:{market}:finalizationQueue` - LIST: Epochs ready for finalization
  - Written by: Event Monitor (on window close)
  - Read by: Finalizer

### Finalizer Keys

- `{protocol}:{market}:batch:part:{epochId}:{partId}` - STRING: Partial batch from worker
  - Written by: Finalizer workers
  - Read by: Aggregator (Level 1 aggregation)
  - Format: JSON with project results subset
  - TTL: 2 hours

- `{protocol}:{market}:epoch:{epochId}:parts:completed` - STRING: Count of completed parts
  - Written by: Finalizer workers
  - Read by: Workers monitoring
  - Format: Integer count

- `{protocol}:{market}:epoch:{epochId}:parts:total` - STRING: Total expected parts
  - Written by: Event Monitor/Finalizer
  - Read by: Workers monitoring
  - Format: Integer count

- `{protocol}:{market}:epoch:{epochId}:parts:ready` - STRING: Flag for ready status
  - Written by: Finalizer workers when all parts complete
  - Read by: Aggregator
  - Format: "true"

- `{protocol}:{market}:aggregationQueue` - LIST: Worker parts ready for Level 1 aggregation
  - Written by: Finalizer workers (via UpdateBatchPartsProgress)
  - Read by: Aggregator (Level 1)
  - Format: JSON with epoch_id, parts_completed

- `{protocol}:{market}:finalized:{epochId}` - STRING: Complete local finalized batch
  - Written by: Aggregator (after Level 1 aggregation)
  - Read by: Aggregator (for Level 2), Monitoring
  - Format: JSON encoded FinalizedBatch with IPFS CID

### Aggregator Keys - ALL NAMESPACED

- `{protocol}:{market}:batch:aggregated:{epochId}` - STRING: Network-wide consensus batch
  - Written by: Aggregator (Level 2 aggregation)
  - Read by: Monitoring/API
  - Format: JSON with all validator batches merged

### Monitoring Keys

- `pipeline:health:{component}` - STRING: Component health status
  - Written by: Each component
  - Read by: Monitoring
  - Format: JSON with status, last_update, metrics

- `submission_stats:{epochId}` - HASH: Epoch statistics
  - Written by: Finalizer
  - Read by: Monitoring/API
  - Fields: total_submissions, unique_projects, timestamp

## Data Flow Examples

### 1. Submission Flow
```
Network → P2P Gateway → submissionQueue → Dequeuer → processed:{id} → Event Monitor
```

### 2. Level 1 Aggregation (Worker Parts → Local Batch)
```
Finalizer Workers → batch:part:{epoch}:{0..N} → aggregationQueue → Aggregator
→ {protocol}:{market}:finalized:{epochId} + outgoing:broadcast:batch
```

### 3. Batch Broadcast Flow
```
Aggregator (Level 1) → outgoing:broadcast:batch → P2P Gateway → Network
```

### 4. Batch Reception Flow
```
Network → P2P Gateway → incoming:batch:{epochId}:{validatorId} + aggregation:queue → Aggregator
```

### 5. Level 2 Aggregation (Local + Remote → Consensus)
```
{protocol}:{market}:finalized:{epochId} + incoming:batch:{epochId}:*
→ Aggregator → batch:aggregated:{epochId}
```

## Key Naming Conventions

1. **Queues**: Simple names for lists (e.g., `submissionQueue`)
2. **Temporary**: Prefixed with action (e.g., `processingSubmission:{id}`)
3. **Persistent**: Namespaced by protocol/market (e.g., `{protocol}:{market}:processed:{id}`)
4. **Communication**: Direction prefix (e.g., `incoming:`, `outgoing:`)
5. **Status**: Component:metric format (e.g., `pipeline:health:{component}`)

## TTL Guidelines

- **Temporary processing**: 5 minutes
- **Window status**: 1 hour after close
- **Incoming batches**: 30 minutes
- **Finalized batches**: 24 hours minimum
- **Aggregated batches**: Persistent (no TTL)
- **Active validators**: 5 minutes