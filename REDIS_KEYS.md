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

### P2P Gateway Keys

#### Incoming (from network to Redis)
- `submissionQueue` - LIST: Raw P2P submissions from network
  - Written by: P2P Gateway
  - Read by: Dequeuer
  - Format: JSON encoded P2PSnapshotSubmission

- `incoming:batch:{epochId}` - STRING: Received batch from another validator
  - Written by: P2P Gateway
  - Read by: Aggregator
  - TTL: 30 minutes
  - Format: JSON encoded FinalizedBatch

- `aggregation:queue` - LIST: Epochs ready for aggregation
  - Written by: P2P Gateway (when batch received)
  - Read by: Aggregator
  - Format: epochId as string

- `validator:active:{validatorId}` - STRING: Active validator tracking
  - Written by: P2P Gateway
  - Read by: Aggregator (for metrics)
  - TTL: 5 minutes
  - Format: Unix timestamp

#### Outgoing (from Redis to network)
- `outgoing:broadcast:batch` - LIST: Batches to broadcast
  - Written by: Aggregator
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

### Event Monitor Keys

- `epoch:{market}:{epochId}:window` - STRING: Submission window status
  - Written by: Event Monitor
  - Read by: Dequeuer, Finalizer
  - Values: "open" or "closed"
  - TTL: 1 hour after close

- `{protocol}:{market}:finalizationQueue` - LIST: Epochs ready for finalization
  - Written by: Event Monitor (on window close)
  - Read by: Finalizer

### Finalizer Keys

- `batch:finalized:{epochId}` - STRING: Finalized batch with IPFS CID
  - Written by: Finalizer
  - Read by: Aggregator
  - Format: JSON encoded FinalizedBatch with BatchCID

- `batch:merkle:{epochId}` - STRING: Merkle tree data
  - Written by: Finalizer
  - Read by: Aggregator (optional)
  - TTL: 24 hours

### Aggregator Keys

- `batch:aggregated:{epochId}` - STRING: Aggregated consensus batch
  - Written by: Aggregator
  - Read by: Monitoring/API
  - Format: JSON with all validator batches merged

- `incoming:batch:{epochId}:*` - Pattern for all incoming batches
  - Written by: P2P Gateway
  - Read by: Aggregator
  - Used to find all validator batches for an epoch

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
Network → P2P Gateway → submissionQueue → Dequeuer → processed:{id} → Finalizer
```

### 2. Batch Broadcast Flow
```
Finalizer → batch:finalized:{epochId} → Aggregator → outgoing:broadcast:batch → P2P Gateway → Network
```

### 3. Batch Reception Flow
```
Network → P2P Gateway → incoming:batch:{epochId} + aggregation:queue → Aggregator
```

### 4. Aggregation Flow
```
batch:finalized:{epochId} + incoming:batch:{epochId}:* → Aggregator → batch:aggregated:{epochId}
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