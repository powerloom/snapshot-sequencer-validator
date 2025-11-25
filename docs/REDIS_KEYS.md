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

- `{protocol}:{market}:epoch:{epochId}:state` - HASH: Comprehensive epoch state tracking
  - Written by: Event Monitor, Aggregator, Finalizer, State-Tracker
  - Read by: Monitor API, State-Tracker
  - TTL: 7 days
  - Fields:
    - `window_status`: "open" | "closed"
    - `window_opened_at`: timestamp
    - `window_closes_at`: timestamp
    - `phase`: "submission" | "level1_finalization" | "level2_aggregation" | "onchain_submission" | "complete" | "failed"
    - `submissions_count`: count of processed submissions
    - `level1_status`: "pending" | "in_progress" | "completed" | "failed"
    - `level1_completed_at`: timestamp
    - `level2_status`: "pending" | "collecting" | "aggregating" | "completed" | "failed"
    - `level2_completed_at`: timestamp
    - `onchain_status`: "pending" | "queued" | "submitted" | "confirmed" | "failed"
    - `onchain_tx_hash`: transaction hash
    - `onchain_block_number`: block number
    - `onchain_submitted_at`: timestamp
    - `priority`: validator priority for this epoch
    - `vpa_submission_attempted`: boolean
    - `last_updated`: timestamp

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

### State-Tracker Monitoring Keys - ALL NAMESPACED

#### Dashboard Metrics
- `{protocol}:{market}:dashboard:summary` - STRING: Pre-aggregated system metrics for API
  - Written by: State-Tracker (aggregateCurrentMetrics)
  - Read by: Monitor API (metrics endpoint)
  - TTL: 60 seconds
  - Format: JSON with rates, counts, recent activity
  - Fields: active_validators, batch_rate, epochs_rate, epochs_1m, batches_1m, epochs_5m, batches_5m, submissions_1m, submissions_5m, etc.

- `{protocol}:{market}:stats:current` - HASH: Current operational stats (same data as dashboard:summary but as hash)
  - Written by: State-Tracker (aggregateCurrentMetrics)
  - Read by: Monitor API (current_stats endpoint)
  - TTL: 60 seconds
  - Purpose: Easy field access for API responses

#### Participation Metrics (24-hour)
- `{protocol}:{market}:metrics:participation` - STRING: Validator participation and inclusion statistics
  - Written by: State-Tracker (aggregateParticipationMetrics)
  - Read by: Monitor API (participation_stats endpoint)
  - TTL: 300 seconds (5 minutes)
  - Format: JSON with 24h aggregated data
  - Fields: epochs_participated_24h, level1_batches_24h, level2_inclusions_24h, participation_rate, inclusion_rate, epochs_total_24h

#### Current Epoch Status
- `{protocol}:{market}:metrics:current_epoch` - STRING: Current epoch timing and phase information
  - Written by: State-Tracker (aggregateCurrentEpochStatus)
  - Read by: Monitor API (current_status endpoint)
  - TTL: 30 seconds
  - Format: JSON with epoch status
  - Fields: epoch_id, phase, time_remaining_seconds, window_duration, submissions_received

#### Timeline Event Tracking
- `{protocol}:{market}:metrics:epochs:timeline` - ZSET: Epoch lifecycle events
  - Written by: Event Monitor (epoch open/close events)
  - Read by: State-Tracker (for epoch counting), Monitor API
  - No TTL (pruned daily by state-tracker)
  - Format: Sorted set by timestamp
  - Members: "open:{epochId}", "close:{epochId}"

- `{protocol}:{market}:metrics:batches:timeline` - ZSET: Batch creation events
  - Written by: Aggregator (local and aggregated batches)
  - Read by: State-Tracker (for batch counting), Monitor API
  - No TTL (pruned daily by state-tracker)
  - Format: Sorted set by timestamp
  - Members: "local:{epochId}", "aggregated:{epochId}"

- `{protocol}:{market}:metrics:submissions:timeline` - ZSET: Submission receipt events
  - Written by: Unified Sequencer (handleSubmissionMessages)
  - Read by: State-Tracker (for submission counting)
  - No TTL (pruned daily by state-tracker)
  - Format: Sorted set by timestamp
  - Members: submissionId

- `{protocol}:{market}:metrics:validations:timeline` - ZSET: Validation completion events
  - Written by: Dequeuer (ProcessSubmission)
  - Read by: State-Tracker (for validation metrics)
  - No TTL (pruned daily by state-tracker)
  - Format: Sorted set by timestamp
  - Members: submissionId

#### Validator-Specific Tracking
- `{protocol}:{market}:metrics:validator:{validatorId}:batches` - ZSET: Per-validator batch timeline
  - Written by: Aggregator (when batches created)
  - Read by: State-Tracker (participation metrics)
  - No TTL (pruned daily by state-tracker)
  - Format: Sorted set by timestamp
  - Members: epochId
  - Purpose: Track individual validator participation

- `{protocol}:{market}:metrics:batch:{epochId}:validators` - STRING: Validator list for specific batch
  - Written by: Aggregator (during batch creation)
  - Read by: State-Tracker (participation metrics)
  - TTL: 24 hours
  - Format: JSON array of validator IDs
  - Purpose: Track who participated in each batch

#### Deterministic Aggregation Keys
- `{protocol}:{market}:ActiveEpochs` - SET: Currently active epoch IDs
  - Written by: Event Monitor
  - Read by: State-Tracker (deterministic aggregation)
  - Purpose: Direct access to active epochs instead of SCAN operations
  - Format: Set of epoch IDs as strings

- `{protocol}:{market}:EpochValidators({epochId})` - SET: Validator IDs participating in each epoch
  - Written by: Event Monitor/Aggregator
  - Read by: State-Tracker (deterministic aggregation)
  - Purpose: Efficient validator detection per epoch
  - Format: Set of validator IDs

- `{protocol}:{market}:EpochProcessed({epochId})` - SET: Processed submission IDs per epoch
  - Written by: Dequeuer
  - Read by: State-Tracker (deterministic aggregation)
  - Purpose: Fast submission counting without expensive operations
  - Format: Set of submission IDs

- `{protocol}:{market}:epochs:gaps` - ZSET: Epoch gaps tracking
  - Written by: State-Tracker (detectEpochGaps)
  - Read by: Monitor API (epochs/gaps endpoint)
  - TTL: 24 hours (old gaps pruned after 1 hour)
  - Format: Sorted set by timestamp
  - Members: "{epochId}:{gapType}" where gapType is "missing_level1", "missing_level2", or "missing_onchain"
  - Purpose: Track epochs with missing finalizations for gap detection and alerting

#### Legacy Health Monitoring
- `pipeline:health:{component}` - STRING: Component health status
  - Written by: Each component
  - Read by: Monitoring
  - Format: JSON with status, last_update, metrics

- `submission_stats:{epochId}` - HASH: Epoch statistics
  - Written by: Finalizer
  - Read by: Monitoring/API
  - Fields: total_submissions, unique_projects, timestamp

## Data Flow Examples

### 1. Submission Flow with Monitoring
```
Network → P2P Gateway → submissionQueue → Dequeuer → processed:{id} → Event Monitor
                                           ↓                          ↓
                                   metrics:submissions:timeline   metrics:epochs:timeline
                                           ↓                          ↓
                                   metrics:validations:timeline   ActiveEpochs SET
```

### 2. Level 1 Aggregation with Monitoring (Worker Parts → Local Batch)
```
Finalizer Workers → batch:part:{epoch}:{0..N} → aggregationQueue → Aggregator
→ {protocol}:{market}:finalized:{epochId} + outgoing:broadcast:batch
        ↓                                              ↓
EpochProcessed({epochId}) SET                     metrics:batches:timeline
        ↓                                              ↓
  metrics:validator:{validatorId}:batches      dashboard:summary (rates)
```

### 3. Batch Broadcast Flow
```
Aggregator (Level 1) → outgoing:broadcast:batch → P2P Gateway → Network
```

### 4. Batch Reception Flow with Participation Tracking
```
Network → P2P Gateway → incoming:batch:{epochId}:{validatorId} + aggregation:queue → Aggregator
                                                                                   ↓
                                                                     metrics:batch:{epochId}:validators
```

### 5. Level 2 Aggregation with Participation Metrics
```
{protocol}:{market}:finalized:{epochId} + incoming:batch:{epochId}:*
→ Aggregator → batch:aggregated:{epochId}
                    ↓
            metrics:participation (24h stats)
```

### 6. State-Tracker Monitoring Flow
```
Timeline Events (epochs, batches, submissions, validations) → State-Tracker
                                                            ↓
                                        dashboard:summary + stats:current
                                                            ↓
                                        metrics:participation + current_epoch
                                                            ↓
                                                    Monitor API Response
```

### 7. Deterministic Aggregation Flow
```
Event Monitor → ActiveEpochs SET + EpochValidators({epochId}) SET + metrics:epochs:timeline
                     ↓                                          ↓
            State-Tracker (direct access)          State-Tracker (timeline counting)
                     ↓                                          ↓
           Eliminates SCAN operations                Accurate rate calculations
```

## Component Responsibilities

### P2P Gateway
**Writes:**
- `{protocol}:{market}:submissionQueue` - Raw submissions from network
- `{protocol}:{market}:incoming:batch:{epochId}:{validatorId}` - Received batches from validators
- `{protocol}:{market}:aggregation:queue` - Epochs ready for Level 2 aggregation
- `validator:active:{validatorId}` - Active validator tracking
- `metrics:submissions:timeline` - Submission receipt events (NEW)

**Reads:**
- `{protocol}:{market}:outgoing:broadcast:batch` - Batches to broadcast

### Dequeuer
**Writes:**
- `{protocol}:{market}:processed:{sequencerId}:{submissionId}` - Validated submissions
- `{protocol}:{market}:epoch:{epochId}:processed` - Set of processed submissions
- `{protocol}:{market}:EpochProcessed({epochId})` - Deterministic aggregation set
- `metrics:validations:timeline` - Validation completion events (NEW)

**Reads:**
- `{protocol}:{market}:submissionQueue` - Raw submissions to process

### Event Monitor
**Writes:**
- `{protocol}:{market}:epoch:{epochId}:window` - Submission window status
- `{protocol}:{market}:epoch:{epochId}:state` - Initial epoch state hash (window status, phase, timestamps)
- `{protocol}:{market}:finalizationQueue` - Epochs ready for finalization
- `{protocol}:{market}:metrics:epochs:timeline` - Epoch lifecycle events
- `{protocol}:{market}:ActiveEpochs` - SET of active epoch IDs
- `{protocol}:{market}:EpochValidators({epochId})` - SET of validators per epoch

### Finalizer
**Writes:**
- `{protocol}:{market}:batch:part:{epochId}:{partId}` - Partial batch results
- `{protocol}:{market}:epoch:{epochId}:parts:*` - Progress tracking
- `{protocol}:{market}:aggregationQueue` - Ready for Level 1 aggregation
- `submission_stats:{epochId}` - Epoch statistics
- `{protocol}:{market}:epoch:{epochId}:state` - Updates level1_status to "in_progress" when finalization starts

### Aggregator
**Writes:**
- `{protocol}:{market}:finalized:{epochId}` - Complete local batch
- `{protocol}:{market}:batch:aggregated:{epochId}` - Network consensus batch
- `{protocol}:{market}:metrics:batches:timeline` - Batch creation events
- `{protocol}:{market}:metrics:validator:{validatorId}:batches` - Per-validator timeline
- `{protocol}:{market}:metrics:batch:{epochId}:validators` - Validator list per batch
- `{protocol}:{market}:metrics:participation` - 24h participation metrics
- `{protocol}:{market}:epoch:{epochId}:state` - Updates level1_status, level2_status, onchain_status, priority

**Reads:**
- `{protocol}:{market}:aggregationQueue` - Worker parts to aggregate
- `{protocol}:{market}:incoming:batch:{epochId}:{validatorId}` - Remote batches
- `{protocol}:{market}:EpochValidators({epochId})` - Active validators

### State-Tracker (NEW)
**Writes:**
- `{protocol}:{market}:dashboard:summary` - Pre-aggregated system metrics
- `{protocol}:{market}:stats:current` - Current operational stats (hash)
- `{protocol}:{market}:metrics:current_epoch` - Current epoch status
- `{protocol}:{market}:epoch:{epochId}:state` - Updates submission_count field
- `{protocol}:{market}:epochs:gaps` - Epoch gaps tracking
- Pruning of old timeline data

**Reads:**
- `{protocol}:{market}:metrics:*:timeline` - All timeline events for counting
- `{protocol}:{market}:ActiveEpochs` - Direct epoch access
- `{protocol}:{market}:EpochValidators({epochId})` - Validator sets
- `{protocol}:{market}:EpochProcessed({epochId})` - Submission sets
- `{protocol}:{market}:epoch:{epochId}:state` - Epoch state for gap detection

### Monitor API
**Writes:**
- None (read-only component)

**Reads:**
- `{protocol}:{market}:dashboard:summary` - System metrics
- `{protocol}:{market}:stats:current` - Current stats
- `{protocol}:{market}:metrics:participation` - Participation data
- `{protocol}:{market}:metrics:current_epoch` - Epoch status
- All queue depths for real-time status

## Key Naming Conventions

1. **Queues**: Simple names for lists (e.g., `submissionQueue`)
2. **Temporary**: Prefixed with action (e.g., `processingSubmission:{id}`)
3. **Persistent**: Namespaced by protocol/market (e.g., `{protocol}:{market}:processed:{id}`)
4. **Communication**: Direction prefix (e.g., `incoming:`, `outgoing:`)
5. **Status**: Component:metric format (e.g., `pipeline:health:{component}`)
6. **Monitoring**: `metrics:{type}:timeline` for event tracking
7. **Deterministic**: `ActiveEpochs`, `Epoch*` for direct access patterns

## TTL Guidelines

### Core Data Flow
- **Temporary processing**: 5 minutes
- **Window status**: 1 hour after close
- **Incoming batches**: 30 minutes
- **Finalized batches**: 24 hours minimum
- **Aggregated batches**: Persistent (no TTL)
- **Active validators**: 5 minutes

### Monitoring & Metrics
- **Dashboard summary**: 60 seconds (real-time metrics)
- **Current stats**: 60 seconds (same as dashboard summary)
- **Participation metrics**: 5 minutes (24h calculations less frequent)
- **Current epoch status**: 30 seconds (frequent updates)
- **Timeline events**: No TTL (pruned daily by state-tracker)
- **Validator batch timelines**: No TTL (pruned daily by state-tracker)
- **Batch validator lists**: 24 hours (participation tracking)
- **Health status**: 5 minutes (component monitoring)

### Pruning Strategy
- **Timeline pruning**: Daily cleanup of events older than 24 hours
- **Deterministic sets**: Keep based on epoch activity (managed by ActiveEpochs)
- **Metrics aggregation**: State-tracker manages rolling windows