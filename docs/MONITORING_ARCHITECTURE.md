# DSV Monitoring Architecture - Complete Guide

**Last Updated**: January 25, 2025  
**Audience**: Developers, AI Agents, Operators  
**Purpose**: Complete understanding of how monitoring data flows through the system

## Overview

The DSV monitoring system provides **epoch-centered observability** with complete visibility into:
- Submission collection (which snapshotter sent what)
- Finalization phases (Level 1 and Level 2)
- On-chain submission status (transaction tracking)
- Gap detection (missing finalizations)

## Architecture Principles

### 1. Three-Layer Architecture
```
Pipeline Components → Redis (State Writes) → State-Tracker (Aggregation) → Monitor-API (Read-Only)
```

**Key Rules**:
- **Pipeline Components**: Write state to Redis with TTLs
- **State-Tracker**: Background worker aggregates every 30s (NO API)
- **Monitor-API**: Read-only, reads pre-aggregated data (NO SCAN operations)

### 2. Data Flow Pattern

#### Writing State (Pipeline Components)
```go
// Example: Event Monitor writes epoch state
redis.HSet(ctx, epochStateKey, map[string]interface{}{
    "window_status": "open",
    "phase": "submission",
    "window_opened_at": timestamp,
})
redis.Expire(ctx, epochStateKey, 7*24*time.Hour) // 7 day TTL
```

#### Aggregating State (State-Tracker)
```go
// Example: State-Tracker aggregates every 30s
func aggregateCurrentMetrics() {
    // Read from timelines
    epochs := redis.ZRangeByScore("metrics:epochs:timeline", ...)
    
    // Aggregate and write prepared dataset
    redis.SetEx("dashboard:summary", jsonData, 60*time.Second)
}
```

#### Reading State (Monitor-API)
```go
// Example: Monitor-API reads prepared data
func DashboardSummary() {
    data := redis.Get("dashboard:summary") // Single key read
    return json.Unmarshal(data)
}
```

## Core Redis Keys

### Epoch State Tracking (Authoritative Source)

**Key**: `{protocol}:{market}:epoch:{epochId}:state` (HASH)  
**TTL**: 7 days  
**Written by**: Event Monitor, Aggregator, Finalizer, State-Tracker, **Relayer-Py**  
**Read by**: Monitor-API, State-Tracker

**Fields**:
- `window_status`: "open" | "closed"
- `window_opened_at`: Unix timestamp
- `window_closes_at`: Unix timestamp
- `phase`: "submission" | "level1_finalization" | "level2_aggregation" | "onchain_submission" | "complete" | "failed"
- `submissions_count`: Number of processed submissions
- `level1_status`: "pending" | "in_progress" | "completed" | "failed"
- `level1_completed_at`: Unix timestamp
- `level2_status`: "pending" | "collecting" | "aggregating" | "completed" | "failed"
- `level2_completed_at`: Unix timestamp
- `onchain_status`: "pending" | "queued" | "submitted" | "confirmed" | "failed"
- `onchain_tx_hash`: Transaction hash (when submitted)
- `onchain_block_number`: Block number (when confirmed)
- `onchain_submitted_at`: Unix timestamp
- `priority`: Validator priority (1-N)
- `vpa_submission_attempted`: Boolean
- `last_updated`: Unix timestamp

**Why This Key Exists**:
- Single source of truth for epoch state
- Enables epoch-centered queries
- Tracks complete workflow from submission to on-chain confirmation
- Relayer-py writes directly to this key (no log parsing needed)

### Submission Tracking

**Timeline Key**: `{protocol}:{market}:metrics:submissions:timeline` (ZSET)  
**TTL**: None (pruned daily)  
**Written by**: P2P Gateway, Dequeuer  
**Read by**: State-Tracker, Monitor-API

**Entity ID Formats**:
- **Enhanced**: `received:{epochId}:{slotId}:{projectId}:{timestamp}:{peerId}`
- **Legacy**: `{epochId}-{projectId}-{timestamp}`

**Metadata Key**: `{protocol}:{market}:metrics:submissions:metadata:{entityId}` (STRING)  
**TTL**: 24 hours  
**Written by**: P2P Gateway, Dequeuer  
**Read by**: Monitor-API

**Metadata Fields**:
- `epoch_id`: Epoch ID
- `slot_id`: Snapshotter slot ID
- `project_id`: Project ID
- `cid` or `snapshot_cid`: IPFS CID
- `peer_id`: Peer ID that sent submission
- `validator_id`: Validator ID (if available)
- `timestamp`: Unix timestamp
- `entity_id`: Entity ID from timeline

**Why These Keys Exist**:
- Enable epoch-centered submission queries
- Track which snapshotter sent what
- Debug submission patterns
- Validate peer participation

### Timeline Keys (Sorted Sets)

All timeline keys are **ZSETs** with:
- **Score**: Unix timestamp
- **Member**: Entity identifier
- **TTL**: None (pruned daily by State-Tracker)

**Keys**:
- `{protocol}:{market}:metrics:epochs:timeline` - Epoch lifecycle events
  - Members: `"open:{epochId}"`, `"close:{epochId}"`
- `{protocol}:{market}:metrics:batches:timeline` - Batch creation events
  - Members: `"local:{epochId}"`, `"aggregated:{epochId}"`
- `{protocol}:{market}:metrics:submissions:timeline` - Submission receipt events
  - Members: Enhanced entity IDs or legacy format
- `{protocol}:{market}:metrics:validations:timeline` - Validation completion events
  - Members: Submission IDs

## API Endpoints

### Epoch-Centered Endpoints

#### `GET /api/v1/epochs/{epochId}/status`
**Purpose**: Get complete epoch state  
**Data Source**: `{protocol}:{market}:epoch:{epochId}:state` (HASH)  
**Returns**: All fields from epoch state hash

**Example**:
```bash
curl "http://localhost:9091/api/v1/epochs/23875280/status?protocol=0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401&market=0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d"
```

#### `GET /api/v1/epochs/{epochId}/submissions`
**Purpose**: Get all submissions for an epoch with detailed metadata  
**Data Sources**:
1. `{protocol}:{market}:metrics:submissions:timeline` (ZSET) - Query last 24h
2. `{protocol}:{market}:metrics:submissions:metadata:{entityId}` (STRING) - Fetch metadata

**How It Works**:
1. Query timeline ZSET for last 24 hours
2. Filter entity IDs matching epoch ID
3. For each matching submission:
   - Fetch metadata from metadata key
   - Fallback to parsing entity ID if metadata missing
4. Return array sorted by timestamp (most recent first)

**Returns**:
- `entity_id`: Entity ID from timeline
- `epoch_id`: Epoch ID
- `slot_id`: Snapshotter slot ID
- `project_id`: Project ID
- `snapshot_cid`: IPFS CID
- `peer_id`: Peer ID
- `validator_id`: Validator ID (if available)
- `timestamp`: Unix timestamp
- `time`: RFC3339 formatted time

**Example**:
```bash
curl "http://localhost:9091/api/v1/epochs/23875280/submissions?protocol=0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401&market=0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d"
```

#### `GET /api/v1/epochs/active`
**Purpose**: Get epochs currently in progress  
**Data Sources**:
1. `{protocol}:{market}:metrics:epochs:timeline` (ZSET) - Query last 100 epochs
2. `{protocol}:{market}:epoch:{epochId}:state` (HASH) - Check state for each epoch

**How It Works** (Fixed Implementation):
1. Query timeline ZSET for last 100 epochs
2. For each epoch ID:
   - Query epoch state hash
   - Check `window_status`, `level1_status`, `level2_status`
   - Include if: window="open" OR level1="in_progress" OR level2="collecting"/"aggregating"
3. Return filtered epochs sorted by epoch ID (descending)

**Why Fixed**: Previous implementation relied on `epochs:active` SET which could be stale, causing inconsistent results.

**Example**:
```bash
curl "http://localhost:9091/api/v1/epochs/active?protocol=0x3B5A0FB70ef68B5dd677C7d614dFB89961f97401&market=0xb5cE2F9B71e785e3eC0C45EDE06Ad95c3bb71a4d"
```

#### `GET /api/v1/epochs/timeline`
**Purpose**: Get epoch progression timeline  
**Data Sources**:
1. `{protocol}:{market}:metrics:epochs:timeline` (ZSET)
2. `{protocol}:{market}:epoch:{epochId}:state` (HASH) - For phase information
3. `{protocol}:{market}:metrics:epoch:{epochId}:info` (HASH) - For timing info

**Returns**: Sorted by epoch ID (descending)

#### `GET /api/v1/epochs/gaps`
**Purpose**: Identify epochs with missing finalizations  
**Data Sources**:
1. `{protocol}:{market}:epochs:gaps` (ZSET) - Pre-detected gaps
2. `{protocol}:{market}:epoch:{epochId}:state` (HASH) - For diagnostic info

**Gap Types**:
- `missing_level1`: Window closed but Level 1 not started
- `missing_level2`: Level 1 completed but Level 2 not started
- `missing_onchain`: Level 2 completed but on-chain submission not attempted

## Component Responsibilities

### Event Monitor
**Writes**:
- `{protocol}:{market}:epoch:{epochId}:state` - Initial epoch state (window open)
- `{protocol}:{market}:metrics:epochs:timeline` - Epoch lifecycle events
- `{protocol}:{market}:epochs:active` - Active epochs SET (legacy, may be stale)

**When**:
- On `EpochReleased` event: Initialize epoch state hash
- On window close: Update epoch state (window_status="closed", phase="level1_finalization")

### Dequeuer
**Writes**:
- `{protocol}:{market}:metrics:submissions:timeline` - Submission receipt events
- `{protocol}:{market}:metrics:submissions:metadata:{entityId}` - Detailed submission metadata
- `{protocol}:{market}:epoch:{epochId}:processed` - Processed submission IDs SET

**When**:
- On submission receipt: Write to timeline with enhanced entity ID
- On submission validation: Write metadata with slot_id, peer_id, project_id, cid

### Finalizer
**Writes**:
- `{protocol}:{market}:epoch:{epochId}:state` - Updates `level1_status="in_progress"`

**When**:
- On finalization start: Update epoch state

### Aggregator
**Writes**:
- `{protocol}:{market}:epoch:{epochId}:state` - Updates:
  - `level1_status="completed"` (on Level 1 completion)
  - `level2_status="collecting"` (on Level 2 window start)
  - `level2_status="aggregating"` (on Level 2 window expire)
  - `level2_status="completed"` (on Level 2 completion)
  - `onchain_status="queued"` (on relayer submission)
  - `priority` (validator priority)
- `{protocol}:{market}:metrics:batches:timeline` - Batch creation events

**When**:
- On Level 1 completion: Update epoch state
- On Level 2 window start: Update epoch state
- On Level 2 completion: Update epoch state
- On relayer submission: Update epoch state with priority

### Relayer-Py
**Writes**:
- `{protocol}:{market}:epoch:{epochId}:state` - Updates:
  - `onchain_status="submitted"` (on transaction submission)
  - `onchain_tx_hash` (transaction hash)
  - `onchain_submitted_at` (timestamp)
  - `onchain_status="confirmed"` (on receipt confirmation)
  - `onchain_block_number` (block number from receipt)
  - `onchain_status="failed"` (on transaction failure)

**When**:
- On transaction submission: Write submitted status
- On receipt confirmation: Write confirmed status
- On transaction failure: Write failed status

**Key Point**: Relayer-py writes directly to Redis using the same key format as DSV nodes, providing seamless integration without log parsing.

### State-Tracker
**Writes**:
- `{protocol}:{market}:dashboard:summary` - Pre-aggregated metrics (60s TTL)
- `{protocol}:{market}:stats:current` - Current stats hash (60s TTL)
- `{protocol}:{market}:metrics:participation` - Participation metrics (60s TTL)
- `{protocol}:{market}:metrics:current_epoch` - Current epoch status (30s TTL)
- `{protocol}:{market}:epoch:{epochId}:state` - Updates `submissions_count` field
- `{protocol}:{market}:epochs:gaps` - Gap detection ZSET (24h TTL)

**Reads**:
- All timeline keys for counting
- Epoch state hashes for gap detection
- Active epochs SET (for deterministic aggregation)

**When**:
- Every 30 seconds: Aggregate current metrics
- Every 5 minutes: Aggregate hourly stats
- Every hour: Aggregate daily stats and prune timelines

### Monitor-API
**Reads** (Read-only):
- `{protocol}:{market}:dashboard:summary` - Dashboard metrics
- `{protocol}:{market}:epoch:{epochId}:state` - Epoch state
- `{protocol}:{market}:metrics:epochs:timeline` - Epoch timeline
- `{protocol}:{market}:metrics:submissions:timeline` - Submissions timeline
- `{protocol}:{market}:metrics:submissions:metadata:{entityId}` - Submission metadata
- `{protocol}:{market}:metrics:batches:timeline` - Batches timeline
- `{protocol}:{market}:metrics:batch:local:{epochId}` - Level 1 batch metadata
- `{protocol}:{market}:metrics:batch:aggregated:{epochId}` - Level 2 batch metadata
- `{protocol}:{market}:epochs:gaps` - Gap detection

**Rules**:
- **NO SCAN operations** - Only single key reads
- **NO writes** - Read-only component
- **Query parameters**: All endpoints support `protocol` and `market` for multi-market environments

## Data Flow Examples

### Example 1: Epoch Lifecycle Tracking

```
1. Event Monitor detects EpochReleased event
   → Writes: epoch:{epochId}:state (window_status="open", phase="submission")
   → Writes: metrics:epochs:timeline ("open:{epochId}")

2. Dequeuer processes submissions
   → Writes: metrics:submissions:timeline (entity IDs)
   → Writes: metrics:submissions:metadata:{entityId} (detailed metadata)

3. Event Monitor detects window close
   → Updates: epoch:{epochId}:state (window_status="closed", phase="level1_finalization")
   → Writes: metrics:epochs:timeline ("close:{epochId}")

4. Finalizer starts finalization
   → Updates: epoch:{epochId}:state (level1_status="in_progress")

5. Aggregator completes Level 1
   → Updates: epoch:{epochId}:state (level1_status="completed", phase="level2_aggregation")
   → Writes: metrics:batches:timeline ("local:{epochId}")

6. Aggregator completes Level 2
   → Updates: epoch:{epochId}:state (level2_status="completed", phase="onchain_submission")
   → Writes: metrics:batches:timeline ("aggregated:{epochId}")

7. Aggregator submits to relayer-py
   → Updates: epoch:{epochId}:state (onchain_status="queued", priority=1)

8. Relayer-py submits transaction
   → Updates: epoch:{epochId}:state (onchain_status="submitted", onchain_tx_hash="0x...")

9. Relayer-py confirms receipt
   → Updates: epoch:{epochId}:state (onchain_status="confirmed", onchain_block_number=12345)
```

### Example 2: Querying Epoch Submissions

```
User: GET /api/v1/epochs/23875280/submissions

Monitor-API:
1. Query: ZRANGEBYSCORE metrics:submissions:timeline {24h_ago} +inf
2. Filter: Parse entity IDs, match epoch ID "23875280"
3. For each match:
   - GET metrics:submissions:metadata:{entityId}
   - Parse JSON: {slot_id, peer_id, project_id, cid, ...}
   - Fallback: Parse entity ID format if metadata missing
4. Return: Array of SubmissionInfo sorted by timestamp
```

### Example 3: Active Epochs Query

```
User: GET /api/v1/epochs/active

Monitor-API:
1. Query: ZREVRANGE metrics:epochs:timeline 0 99
2. Extract epoch IDs from timeline entries
3. For each epoch ID:
   - HGETALL epoch:{epochId}:state
   - Check: window_status, level1_status, level2_status
   - Include if: window="open" OR level1="in_progress" OR level2="collecting"/"aggregating"
4. Return: Filtered epochs sorted by epoch ID descending
```

## Key Design Decisions

### Why Epoch State Hash?
- **Single source of truth**: All epoch state in one place
- **Epoch-centered queries**: Easy to query complete epoch workflow
- **Relayer-py integration**: Same key format enables seamless transaction tracking

### Why Timeline Keys?
- **Efficient queries**: Sorted sets enable time-range queries
- **No TTL needed**: Pruned daily by State-Tracker
- **Multiple views**: Same data supports different query patterns

### Why Submission Metadata Keys?
- **Detailed information**: Store slot_id, peer_id, project_id, cid
- **Epoch queries**: Enable "which snapshotter sent what" queries
- **TTL**: 24 hours (sufficient for debugging)

### Why ActiveEpochs Queries Timeline Directly?
- **Reliability**: Avoids stale `epochs:active` SET
- **Accuracy**: Always reflects current state from authoritative source
- **Consistency**: Same data source as `/epochs/{epochId}/status`

## Troubleshooting Guide

### Issue: Epoch appears in `/epochs/active` but shows as completed in `/epochs/{id}/status`
**Cause**: Stale `epochs:active` SET  
**Solution**: Fixed - ActiveEpochs now queries timeline directly

### Issue: Missing submissions in `/epochs/{id}/submissions`
**Possible Causes**:
1. Submissions older than 24 hours (timeline query window)
2. Metadata expired (24h TTL)
3. Entity ID format mismatch

**Solution**: Check timeline directly, verify metadata TTL

### Issue: `onchain_status` stuck at "queued"
**Possible Causes**:
1. Relayer-py not writing to Redis
2. Transaction submission failed
3. Redis connection issue in relayer-py

**Solution**: Check relayer-py logs, verify Redis connection, check transaction status

## Reference Documents

- **REDIS_KEYS.md**: Complete Redis key reference
- **MONITORING_GUIDE.md**: API usage guide
- **MONITORING_IMPLEMENTATION_STATUS.md**: Implementation status
- **MONITORING_IMPLEMENTATION_CHECKLIST.md**: Implementation checklist

