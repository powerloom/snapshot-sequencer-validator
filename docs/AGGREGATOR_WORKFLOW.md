# Aggregator Component Workflow

**Document Status**: Production Implementation (October 2025)  
**Component**: `cmd/aggregator/main.go`  
**Related**: Two-level aggregation system for DSV consensus

## Overview

The aggregator component orchestrates two-level batch aggregation:
- **Level 1**: Internal aggregation of worker parts within a single DSV node
- **Level 2**: Network-wide aggregation of validator batches across all DSV nodes

## Architecture

### Component Responsibilities

**Aggregator (Singleton):**
- Monitors `aggregationQueue` for Level 1 (worker parts)
- Monitors `aggregation:queue` for Level 2 (validator batches)
- Combines batches and creates consensus views
- Broadcasts local finalized batches to validator network

**Finalizer Workers (Scalable):**
- Process batches in parallel
- Create batch parts: `{protocol}:{market}:batch:part:{epoch}:{partID}`
- Signal completion via `aggregationQueue`

**P2P Gateway (Singleton):**
- Receives validator batches from network
- Stores as `incoming:batch:{epochId}:{validatorId}`
- Triggers Level 2 aggregation via `aggregation:queue`

## Level 1: Internal Aggregation

### Purpose

Combine parallel worker parts into a single finalized batch representing the local validator's view. Level 1 aggregation begins **after** the submission window closes, which collects snapshot CIDs from snapshotter nodes.

### Submission Window (Snapshot CID Collection)

**Purpose**: Collect all snapshot CIDs from snapshotter nodes before Level 1 finalization begins

**Historical Context & Repurposing**:
- The legacy contract's `snapshotSubmissionWindow(dataMarket)` function was originally designed for **centralized sequencer architecture**, where a single sequencer collected snapshots from snapshotter nodes.
- In the new **decentralized validator architecture**, this same window duration is **repurposed** as the snapshot CID collection period that triggers Level 1 finalization.
- **Usage Condition**: This repurposing occurs specifically when commit/reveal windows are **NOT enabled** (absence of commit/reveal windows). When commit/reveal is enabled, Level 1 finalization timing is tied to the snapshot reveal window closure instead.

**Window Duration**:
- **Legacy Contracts**: Queries `snapshotSubmissionWindow(dataMarket)` function on ProtocolState contract → **30 seconds** (repurposed from centralized sequencer design)
- **New Contracts**: Uses dynamic window configuration from `getDataMarketSubmissionWindowConfig(dataMarket)`
  - P1 submission window: **45 seconds** (Priority 1 validators commit on-chain)
  - PN submission window: **45 seconds** (Priority N validators commit on-chain)
- **Fallback**: `LEVEL1_FINALIZATION_DELAY_SECONDS` environment variable if contract query fails

**Timing**:
- Window opens when `EpochReleased` event is detected
- During window: Snapshotters submit snapshot CIDs via P2P network (`/powerloom/snapshot-submissions/all`)
- Window closes: Level 1 finalization begins, aggregating collected snapshot CIDs into finalized batch
- **Visual Reference**: See the [Submission Timeline diagram](./imgs/sequencing-timeline.png) for a visual representation of the submission windows and phases.

**Submission Collection (Deterministic)**:
- Submissions stored deterministically in epoch-keyed structures:
  - ZSET: `{protocol}:{market}:epoch:{epochId}:submissions:ids` (ordered by timestamp)
  - HASH: `{protocol}:{market}:epoch:{epochId}:submissions:data` (direct lookup)
- Event Monitor collects submissions using `ZRANGE` + `HGETALL` (no SCAN operations)
- All submissions for epoch available deterministically when window closes

### Workflow

```
1. Event Monitor detects EpochReleased event
   ↓
2. Event Monitor queries contract for submission window duration:
   - Legacy: snapshotSubmissionWindow(dataMarket) → 30 seconds
   - New: getDataMarketSubmissionWindowConfig(dataMarket) → dynamic config (P1=45s, PN=45s)
   ↓
3. Event Monitor starts submission window timer
   ↓
4. During window: Snapshotters submit snapshot CIDs via P2P
   ↓
5. Window closes → Event Monitor collects submissions deterministically:
   - ZRANGE {protocol}:{market}:epoch:{epochId}:submissions:ids
   - HGETALL {protocol}:{market}:epoch:{epochId}:submissions:data
   ↓
6. Multiple Finalizer Workers process batches in parallel (from collected snapshot CIDs)
   ↓
7. Each worker creates batch part:
   Redis: {protocol}:{market}:batch:part:{epoch}:{partID}
   ↓
8. Workers track completion:
   Redis: {protocol}:{market}:epoch:{epochId}:parts:completed
   Redis: {protocol}:{market}:epoch:{epochId}:parts:total
   ↓
9. When all parts complete:
   Redis: LPUSH {protocol}:{market}:aggregationQueue
   Payload: {epoch_id, parts_completed, ready_at}
   ↓
10. Aggregator pops from aggregationQueue
   ↓
11. Aggregator collects ALL batch parts:
   for i in 0..totalParts:
       partKey = batch:part:{epoch}:{i}
       partData = redis.GET(partKey)
       aggregatedResults.merge(partData)
   ↓
12. Aggregator creates finalized batch:
   - Combines all project submissions
   - Calculates merkle root
   - Generates BLS signature
   - Stores in IPFS → Gets CID
   ↓
13. Store finalized batch:
   Redis: {protocol}:{market}:finalized:{epochID}
   ↓
14. Broadcast to validator network:
   Redis: LPUSH outgoing:broadcast:batch
   ↓
15. P2P Gateway transmits on /powerloom/finalized-batches/all
```

### Key Redis Keys

**Submission Storage (Deterministic)**:
- `{protocol}:{market}:epoch:{epochId}:submissions:ids` - ZSET: Submission IDs ordered by timestamp
  - Score: Unix timestamp
  - Member: Submission ID
  - TTL: 2 hours (refreshed on each write)
- `{protocol}:{market}:epoch:{epochId}:submissions:data` - HASH: Submission data
  - Field: Submission ID
  - Value: JSON-encoded ProcessedSubmission
  - TTL: 2 hours (refreshed on each write)
- Purpose: Deterministic collection using `ZRANGE` + `HGETALL`
**Worker Parts:**
- `{protocol}:{market}:batch:part:{epoch}:{partID}` - STRING: Batch part data
- `{protocol}:{market}:epoch:{epochId}:parts:completed` - STRING: Count of completed parts
- `{protocol}:{market}:epoch:{epochId}:parts:total` - STRING: Total parts expected
- `{protocol}:{market}:epoch:{epochId}:parts:ready` - STRING: "true" when all parts complete

**Aggregation Queue:**
- `{protocol}:{market}:aggregationQueue` - LIST: Worker parts ready for Level 1
  - Format: `{"epoch_id": "123", "parts_completed": 3, "ready_at": 1234567890}`

**Finalized Batch:**
- `{protocol}:{market}:finalized:{epochID}` - STRING: Complete local finalized batch
  - Format: JSON `FinalizedBatch` with IPFS CID

### Implementation Details

**Batch Part Collection:**
```go
func (a *Aggregator) aggregateWorkerParts(epochID uint64, totalParts int) {
    aggregatedResults := make(map[string]interface{})
    
    // Collect all parts
    for i := 0; i < totalParts; i++ {
        partKey := a.keyBuilder.BatchPart(strconv.FormatUint(epochID, 10), i)
        partData := a.redisClient.Get(ctx, partKey).Result()
        
        var partResults map[string]interface{}
        json.Unmarshal([]byte(partData), &partResults)
        
        // Merge results
        for projectID, data := range partResults {
            aggregatedResults[projectID] = data
        }
        
        // Clean up part
        a.redisClient.Del(ctx, partKey)
    }
    
    // Create finalized batch
    finalizedBatch := a.createFinalizedBatchFromParts(epochID, aggregatedResults)
    
    // Store in IPFS
    cid := a.ipfsClient.StoreFinalizedBatch(ctx, finalizedBatch)
    finalizedBatch.BatchIPFSCID = cid
    
    // Store finalized batch
    finalizedKey := a.keyBuilder.FinalizedBatch(strconv.FormatUint(epochID, 10))
    a.redisClient.Set(ctx, finalizedKey, finalizedBatch, 24*time.Hour)
    
    // Broadcast to network
    a.broadcastLocalBatch(epochID, finalizedBatch)
}
```

**Broadcast Mechanism:**
```go
func (a *Aggregator) broadcastLocalBatch(epochID uint64, batch *FinalizedBatch) {
    broadcastMsg := map[string]interface{}{
        "type":    "finalized_batch",
        "epochId": epochID,
        "data":    batch,
    }
    
    msgData, _ := json.Marshal(broadcastMsg)
    broadcastQueue := a.keyBuilder.OutgoingBroadcastBatch()
    a.redisClient.LPush(ctx, broadcastQueue, msgData)
}
```

## Level 2: Network Aggregation

### Purpose

Combine validator batches from across the network to create a consensus view of finalized snapshots.

### Workflow

```
1. P2P Gateway receives batch from validator network:
   Topic: /powerloom/finalized-batches/all
   ↓
2. P2P Gateway stores batch:
   Redis: SET {protocol}:{market}:incoming:batch:{epochId}:{validatorId}
   ↓
3. P2P Gateway writes to aggregation stream:
   Redis Stream: XADD {protocol}:{market}:stream:aggregation:notifications
   ↓
OR: Level 1 finalization completes:
   Aggregator writes local batch to stream
   ↓
4. Stream consumer (Aggregator) receives message
   ↓
5. Aggregator starts aggregation window (30s default, configurable):
   - First batch arrival (local or remote) starts timer
   - Additional batches collected during window
   - Window expiration triggers final aggregation
   ↓
6. Aggregator collects batches:
   - Local batch: {protocol}:{market}:finalized:{epochId}
   - Remote batches: {protocol}:{market}:incoming:batch:{epochId}:*
   ↓
7. Aggregator combines validator views:
   - For each project, count votes per CID
   - Select winning CID (majority vote)
   - Merge submission metadata
   ↓
8. Create aggregated consensus batch:
   Redis: {protocol}:{market}:batch:aggregated:{epochId}
   ↓
9. Store metrics and monitoring data
```

**Unified Stream Path:**
- Both local batches (after Level 1) and remote batches (from P2P Gateway) write to the same stream
- Stream consumer processes all messages uniformly
- Eliminates dual-path complexity (queue vs stream)
- Ensures consistent aggregation behavior

### Aggregation Window Logic

**Purpose**: Provide a "sink" for collecting validator batches before Level 2 consensus computation

**Current Behavior (Commit/Reveal Disabled):**
- Fixed duration: `AGGREGATION_WINDOW_SECONDS` (default 30s, configurable)
- First batch arrival (local or remote) starts timer
- Additional batches received during window are collected
- Window expiration triggers final Level 2 aggregation
- Late validators can still contribute if within window
- Acts as collection period before consensus computation

**Future Behavior (Commit/Reveal Enabled):**
- Level 2 aggregation timing tied to Validator Voting Window closure
- Validator Vote Commit Window: Validators commit encrypted votes
- Validator Vote Reveal Window: Validators reveal votes
- Level 2 aggregation triggers after reveal window closes
- Window duration dynamically calculated from contract configuration
- Ensures validators have revealed votes before consensus computation

**Implementation:**
```go
func (a *Aggregator) aggregateEpoch(epochID uint64) {
    // Start aggregation window
    windowStart := time.Now()
    windowDuration := time.Duration(a.config.AggregationWindowSeconds) * time.Second
    
    // Collect batches during window
    batches := make(map[string]*ValidatorBatch)
    
    // Get local batch
    localBatch := a.getLocalBatch(epochID)
    if localBatch != nil {
        batches[a.config.SequencerID] = localBatch
    }
    
    // Collect remote batches during window
    for time.Since(windowStart) < windowDuration {
        remoteBatches := a.getRemoteBatches(epochID)
        for validatorID, batch := range remoteBatches {
            batches[validatorID] = batch
        }
        
        // Check if we have enough validators or window expired
        if len(batches) >= a.config.MinValidators || 
           time.Since(windowStart) >= windowDuration {
            break
        }
        
        time.Sleep(1 * time.Second) // Poll interval
    }
    
    // Combine validator views
    consensusBatch := a.combineValidatorViews(epochID, batches)
    
    // Store aggregated batch
    a.storeAggregatedBatch(epochID, consensusBatch)
}
```

### Consensus Algorithm

**Vote Counting:**
- For each project, count votes per CID across all validators
- Select CID with majority votes (≥51% for 3 validators, ≥2/3 for 10+)
- Merge submission metadata from all validators

**Implementation:**
```go
func (a *Aggregator) combineValidatorViews(epochID uint64, batches map[string]*ValidatorBatch) *AggregatedBatch {
    // Count votes per project per CID
    projectVotes := make(map[string]map[string]int) // projectID -> CID -> vote count
    
    for validatorID, batch := range batches {
        for i, projectID := range batch.ProjectIds {
            cid := batch.SnapshotCids[i]
            
            if projectVotes[projectID] == nil {
                projectVotes[projectID] = make(map[string]int)
            }
            projectVotes[projectID][cid]++
        }
    }
    
    // Select winning CID for each project
    aggregatedProjects := make(map[string]string) // projectID -> winning CID
    totalValidators := len(batches)
    threshold := float64(totalValidators) * 0.51 // 51% for 3, 66% for 10+
    
    for projectID, cidVotes := range projectVotes {
        winningCID := ""
        maxVotes := 0
        
        for cid, votes := range cidVotes {
            if votes > maxVotes {
                maxVotes = votes
                winningCID = cid
            }
        }
        
        // Check if meets threshold
        if float64(maxVotes) >= threshold {
            aggregatedProjects[projectID] = winningCID
        }
    }
    
    return &AggregatedBatch{
        EpochID:           epochID,
        AggregatedProjects: aggregatedProjects,
        ValidatorBatches:   batches,
        TotalValidators:    totalValidators,
    }
}
```

### Key Redis Keys

**Incoming Batches:**
- `incoming:batch:{epochId}:{validatorId}` - STRING: Validator batch data
  - Format: JSON `ValidatorBatch` with IPFS CID

**Aggregation Queue:**
- `aggregation:queue` - LIST: Epochs ready for Level 2 aggregation
  - Format: `{"epoch_id": 123}`

**Aggregated Batch:**
- `batch:aggregated:{epochId}` - STRING: Network-wide consensus batch
  - Format: JSON `AggregatedBatch` with combined validator views

## Monitoring & Metrics

### Level 1 Metrics

**Timeline:**
- `{protocol}:{market}:metrics:batches:timeline` - ZSET: Batch creation timeline
  - Score: Timestamp
  - Member: `local:{epochID}`

**Batch Metrics:**
- `{protocol}:{market}:metrics:batch:local:{epochID}` - STRING: Local batch metrics
  - Fields: `epoch_id`, `type`, `validator_id`, `ipfs_cid`, `merkle_root`, `project_count`, `parts_merged`, `timestamp`

**Validator Batches:**
- `{protocol}:{market}:metrics:validator:batches:{validatorID}` - ZSET: Validator batch timeline

### Level 2 Metrics

**Aggregation Status:**
- `{protocol}:{market}:metrics:aggregation:{epochID}` - STRING: Aggregation status
  - Fields: `epoch_id`, `total_validators`, `received_batches`, `aggregated_projects`, `consensus_achieved`

**Timeline:**
- `{protocol}:{market}:metrics:batches:timeline` - ZSET: Aggregated batch timeline
  - Score: Timestamp
  - Member: `aggregated:{epochID}`

## Error Handling

### Level 1 Errors

**Missing Batch Parts:**
- Log warning, skip missing part
- Continue aggregation with available parts
- Monitor for consistent part loss

**IPFS Upload Failures:**
- Log error, continue without CID
- Batch still stored in Redis
- Monitoring alerts on IPFS failures

### Level 2 Errors

**Missing Local Batch:**
- Can aggregate with remote batches only
- Log warning if local batch expected
- Continue aggregation

**Insufficient Validators:**
- Wait for aggregation window
- Log warning if below threshold
- May not achieve consensus

**Network Partition:**
- Aggregation window allows late arrivals
- Fallback to local batch if no remote batches
- Monitoring detects partition scenarios

## Configuration

### Environment Variables

**AGGREGATION_WINDOW_SECONDS** (Level 2):
- Default: `30`
- Purpose: Time window for collecting validator batches
- Tuning: Increase for high-latency networks

**MIN_VALIDATORS** (Level 2):
- Default: `2`
- Purpose: Minimum validators required for consensus
- Tuning: Adjust based on network size

**FINALIZATION_BATCH_SIZE** (Level 1):
- Default: `100`
- Purpose: Projects per batch part
- Tuning: Adjust based on project count

## Performance Characteristics

### Level 1 Aggregation

**Latency:**
- Part collection: O(n) where n = number of parts
- IPFS upload: Network-dependent (local: <100ms, external: 500ms-2s)
- Total: ~1-3 seconds for typical batches

**Throughput:**
- Handles multiple epochs concurrently
- Limited by IPFS upload bandwidth
- Scales with number of aggregator instances (singleton recommended)

### Level 2 Aggregation

**Latency:**
- Aggregation window: 30s (configurable)
- Batch collection: O(m) where m = number of validators
- Consensus calculation: O(p) where p = number of projects
- Total: ~30-35 seconds (window + processing)

**Throughput:**
- One epoch per aggregation window
- Limited by network propagation
- Scales with validator count

## Integration Points

### Finalizer Workers

**Signal Completion:**
- Update part completion counters
- Push to `aggregationQueue` when all parts ready

### P2P Gateway

**Receive Batches:**
- Store incoming batches
- Trigger Level 2 aggregation

**Broadcast Batches:**
- Read from `outgoing:broadcast:batch`
- Transmit on `/powerloom/finalized-batches/all`

### Monitor API

**Query Metrics:**
- Level 1 batch metrics
- Level 2 aggregation status
- Validator participation

## References

- **Implementation**: `cmd/aggregator/main.go`
- **Redis Keys**: [`REDIS_KEYS.md`](./REDIS_KEYS.md)
- **State Transitions**: [`PROTOCOL_STATE_TRANSITIONS.md`](./PROTOCOL_STATE_TRANSITIONS.md)
- **Visual Timeline**: [`imgs/sequencing-timeline.png`](./imgs/sequencing-timeline.png)

