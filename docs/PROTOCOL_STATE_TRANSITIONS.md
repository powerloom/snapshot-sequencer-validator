# Protocol State Transitions

**Document Status**: Production Implementation (October 2025)  
**Purpose**: Document the state machine and workflow transitions in the DSV protocol

## Overview

The DSV protocol operates through a series of state transitions from snapshot submission to on-chain finalization. Understanding these transitions is critical for debugging, monitoring, and ensuring correct protocol behavior.

## State Machine Overview

```
SUBMISSION → COLLECTION → FINALIZATION → AGGREGATION → CONSENSUS → ON-CHAIN
```

## Detailed State Transitions

### Phase 1: Submission State

**State**: `SUBMITTED`  
**Actor**: Snapshotter nodes (via Local Collector)  
**Trigger**: Snapshot computation completes

**State Data:**
- Snapshot CID
- Project ID
- Epoch ID
- Submitter ID
- EIP-712 Signature
- Timestamp

**Transition:**
```
Snapshotter computes snapshot
  ↓
Local Collector signs with EIP-712
  ↓
Publishes to /powerloom/snapshot-submissions/all
  ↓
State: SUBMITTED
```

**Redis Storage:**
- `submissionQueue` - LIST: Queued submissions for processing

### Phase 2: Collection State

**State**: `COLLECTED`  
**Actor**: P2P Gateway → Dequeuer  
**Trigger**: P2P Gateway receives submission

**State Data:**
- All submission fields
- Validation status
- Deduplication result
- Collection timestamp

**Transition:**
```
P2P Gateway receives submission
  ↓
Validates EIP-712 signature
  ↓
Checks for duplicates
  ↓
Pushes to submissionQueue
  ↓
Dequeuer processes
  ↓
Stores deterministically in epoch-keyed structures:
  - ZSET: {protocol}:{market}:epoch:{epochId}:submissions:ids
  - HASH: {protocol}:{market}:epoch:{epochId}:submissions:data
  ↓
State: COLLECTED
```

**Redis Storage (Deterministic)**:
- `{protocol}:{market}:epoch:{epochId}:submissions:ids` - ZSET: Submission IDs ordered by timestamp
- `{protocol}:{market}:epoch:{epochId}:submissions:data` - HASH: Submission data (field=submissionId, value=JSON)
- `{protocol}:{market}:processed:{sequencerId}:{submissionId}` - STRING: Individual submission (legacy, kept for compatibility)
- `{protocol}:{market}:epoch:{epochId}:processed` - SET: Submission IDs (legacy, kept for compatibility)

### Phase 3: Finalization State

**State**: `FINALIZED` (Local)  
**Actor**: Event Monitor → Finalizer Workers → Aggregator  
**Trigger**: Submission window closes (after collecting snapshot CIDs)

**State Data:**
- Batch parts (from workers)
- Finalized batch (from aggregator)
- Merkle root
- BLS signature
- IPFS CID
- Project votes

**Submission Window (Snapshot CID Collection):**
- **Purpose**: Collect all snapshot CIDs from snapshotter nodes before Level 1 finalization begins
- **Historical Context**: The legacy contract's `snapshotSubmissionWindow(dataMarket)` function was originally designed for centralized sequencer architecture, where a single sequencer collected snapshots. In the new decentralized validator architecture, this same window duration is repurposed as the snapshot CID collection period that triggers Level 1 finalization.
- **Usage Condition**: This repurposing occurs specifically when commit/reveal windows are **NOT enabled** (absence of commit/reveal windows). When commit/reveal is enabled, Level 1 finalization timing is tied to the snapshot reveal window closure instead.
- **Visual Reference**: See the [Submission Timeline diagram](./imgs/sequencing-timeline.png) for a visual representation of the submission windows and phases.
- **Duration**: Fetched from legacy ProtocolState contract's `snapshotSubmissionWindow(dataMarket)` function
  - Legacy contracts: Queries `snapshotSubmissionWindow()` directly → **30 seconds** (repurposed from centralized sequencer design)
  - New contracts: Uses dynamic window configuration from `getDataMarketSubmissionWindowConfig()`
    - P1 submission window: **45 seconds** (Priority 1 validators commit on-chain)
    - PN submission window: **45 seconds** (Priority N validators commit on-chain)
  - Fallback: `LEVEL1_FINALIZATION_DELAY_SECONDS` env var if contract query fails
- **Timing**: Window opens when `EpochReleased` event is detected, closes after duration expires
- **Collection**: During this window, snapshotters submit snapshot CIDs via P2P network
- **After Window Closes**: Level 1 finalization begins, aggregating collected snapshot CIDs into finalized batch

**Transition:**
```
Event Monitor detects EpochReleased event
  ↓
Event Monitor queries contract for submission window duration:
  - Legacy: snapshotSubmissionWindow(dataMarket) → 30 seconds
  - New: getDataMarketSubmissionWindowConfig(dataMarket) → dynamic config (P1=45s, PN=45s)
  ↓
Event Monitor starts submission window timer
  ↓
During window: Snapshotters submit snapshot CIDs via P2P
  ↓
Window closes → Event Monitor triggers finalization
  ↓
Event Monitor collects submissions deterministically:
  - ZRANGE {protocol}:{market}:epoch:{epochId}:submissions:ids (no SCAN)
  - HGETALL {protocol}:{market}:epoch:{epochId}:submissions:data (no SCAN)
  ↓
Creates finalization tasks for collected snapshot CIDs
  ↓
Finalizer Workers process batches in parallel
  ↓
Each worker creates batch part from collected snapshot CIDs
  ↓
When all parts complete → aggregationQueue
  ↓
Aggregator combines parts (Level 1)
  ↓
Creates finalized batch with merkle root
  ↓
Stores in IPFS
  ↓
State: FINALIZED (Local)
```

**Redis Storage:**
- `{protocol}:{market}:epoch:{epochId}:submissions:ids` - ZSET: Submission IDs (deterministic collection)
- `{protocol}:{market}:epoch:{epochId}:submissions:data` - HASH: Submission data (deterministic collection)
- `{protocol}:{market}:batch:part:{epoch}:{partID}` - STRING: Batch part
- `{protocol}:{market}:finalized:{epochID}` - STRING: Finalized batch
- `{protocol}:{market}:aggregationQueue` - LIST: Ready for Level 1

### Phase 4: Broadcast State

**State**: `BROADCAST`  
**Actor**: Aggregator → P2P Gateway  
**Trigger**: Level 1 aggregation completes

**State Data:**
- Finalized batch with IPFS CID
- Validator ID
- Broadcast timestamp

**Transition:**
```
Aggregator completes Level 1 aggregation
  ↓
Stores finalized batch in Redis
  ↓
Pushes to outgoing:broadcast:batch
  ↓
P2P Gateway reads queue
  ↓
Broadcasts on /powerloom/finalized-batches/all
  ↓
State: BROADCAST
```

**Redis Storage:**
- `outgoing:broadcast:batch` - LIST: Batches to broadcast
- `{protocol}:{market}:finalized:{epochID}` - STRING: Finalized batch (already stored)

### Phase 5: Network Aggregation State

**State**: `AGGREGATING`  
**Actor**: Aggregator  
**Trigger**: Stream message received (from local batch or P2P Gateway)

**State Data:**
- Local finalized batch
- Remote validator batches
- Aggregation window status
- Vote counts per project

**Transition:**
```
Level 1 finalization completes (local batch)
  ↓
Aggregator writes to aggregation stream: {protocol}:{market}:stream:aggregation:notifications
  ↓
OR: P2P Gateway receives batch from validator
  ↓
P2P Gateway stores as incoming:batch:{epochId}:{validatorId}
  ↓
P2P Gateway writes to aggregation stream
  ↓
Stream consumer (Aggregator) receives message
  ↓
Aggregator starts aggregation window (30s default, configurable)
  ↓
Collects batches during window (local + remote)
  ↓
State: AGGREGATING
```

**Redis Storage:**
- `{protocol}:{market}:incoming:batch:{epochId}:{validatorId}` - STRING: Validator batch
- `{protocol}:{market}:stream:aggregation:notifications` - STREAM: Aggregation triggers (unified path)
- `{protocol}:{market}:finalized:{epochId}` - STRING: Local finalized batch

**Aggregation Window Timing:**
- **Current (Commit/Reveal Disabled)**: Fixed 30-second window (`AGGREGATION_WINDOW_SECONDS`) after first batch arrival
  - Provides sink for collecting validator batches before Level 2 aggregation
  - Window starts when first validator batch (local or remote) arrives
  - Additional batches collected during window
  - Window expiration triggers final Level 2 aggregation
- **Future (Commit/Reveal Enabled)**: Level 2 aggregation will trigger after Validator Voting Window closes
  - Validator Vote Commit Window: Validators commit encrypted votes
  - Validator Vote Reveal Window: Validators reveal votes
  - Level 2 aggregation computes consensus after reveal window closes
  - Window duration will be dynamically calculated from contract configuration

### Phase 6: Consensus State

**State**: `CONSENSUS`  
**Actor**: Aggregator  
**Trigger**: Aggregation window expires

**State Data:**
- Aggregated projects (projectID → winning CID)
- Validator contributions
- Consensus status (achieved/not achieved)
- Total validators participated

**Transition:**
```
Aggregation window expires
  ↓
Aggregator combines validator views
  ↓
Counts votes per project per CID
  ↓
Selects winning CID (majority vote)
  ↓
Creates aggregated consensus batch
  ↓
Stores as batch:aggregated:{epochId}
  ↓
State: CONSENSUS
```

**Redis Storage:**
- `batch:aggregated:{epochId}` - STRING: Aggregated consensus batch
- `{protocol}:{market}:metrics:aggregation:{epochID}` - STRING: Aggregation metrics

### Phase 7: On-Chain State

**State**: `ON_CHAIN`  
**Actor**: Aggregator → VPA Client → relayer-py  
**Trigger**: Level 2 aggregation completes + VPA submission window opens

**State Data:**
- On-chain transaction hash
- Block number
- Gas used
- Confirmation status
- Priority assignment

**Transition:**
```
Level 2 aggregation completes
  ↓
Aggregator checks VPA priority for epoch
  ↓
Waits for submission window to open (if not already open)
  ↓
Checks if higher priority validators already submitted
  ↓
Submits via relayer-py service (if priority allows)
  ↓
relayer-py queues transaction
  ↓
Transaction confirmed on-chain
  ↓
State: ON_CHAIN
```

**VPA Submission Window Timing:**
- Priority-based windows: Each validator has assigned priority (1, 2, 3...)
- Priority 1 validators: Submit immediately after pre-submission window
- Priority N validators: Submit after (N-1) * pNSubmissionWindow delay
- Lower priority validators can submit if higher priority ones miss their window
- Submission windows are contract-defined and dynamically fetched

**Status**: ✅ Implemented (Phase 3.3+)

## State Tracking

### Redis Key Patterns

**Submission Tracking:**
- `{protocol}:{market}:submission:{epochId}:{projectId}:{submitterId}` - Submission state
- `{protocol}:{market}:epoch:{epochId}:submissions` - All submissions for epoch

**Finalization Tracking:**
- `{protocol}:{market}:finalized:{epochID}` - Finalized batch state
- `{protocol}:{market}:epoch:{epochId}:parts:completed` - Worker progress

**Aggregation Tracking:**
- `incoming:batch:{epochId}:{validatorId}` - Validator batch state
- `batch:aggregated:{epochId}` - Consensus state

**Timeline Tracking:**
- `{protocol}:{market}:metrics:batches:timeline` - ZSET: State transition timeline
  - Members: `submitted:{epoch}`, `finalized:{epoch}`, `aggregated:{epoch}`

### Monitoring Queries

**Check Submission State:**
```bash
redis-cli KEYS "{protocol}:{market}:submission:*"
```

**Check Finalization State:**
```bash
redis-cli GET "{protocol}:{market}:finalized:{epochId}"
```

**Check Aggregation State:**
```bash
redis-cli KEYS "incoming:batch:{epochId}:*"
redis-cli GET "batch:aggregated:{epochId}"
```

**Check Timeline:**
```bash
redis-cli ZRANGE "{protocol}:{market}:metrics:batches:timeline" -100 -1
```

## Error States & Recovery

### Stuck in SUBMITTED State

**Symptoms:**
- Submissions in `submissionQueue` but not processed
- No `COLLECTED` state entries

**Causes:**
- Dequeuer not running
- Redis connectivity issues
- Queue processing errors

**Recovery:**
- Check dequeuer status
- Verify Redis connectivity
- Review dequeuer logs for errors
- Manually trigger processing if needed

### Stuck in COLLECTED State

**Symptoms:**
- Submissions collected but no finalization
- No batch parts created

**Causes:**
- Event Monitor not detecting epoch release
- Finalizer workers not running
- Finalization queue not processing

**Recovery:**
- Check Event Monitor status
- Verify epoch release events
- Check finalizer worker status
- Manually trigger finalization if needed

### Stuck in FINALIZED State

**Symptoms:**
- Local batch finalized but not broadcast
- No `BROADCAST` state

**Causes:**
- Aggregator not broadcasting
- P2P Gateway not processing broadcast queue
- Network connectivity issues

**Recovery:**
- Check aggregator logs for broadcast errors
- Verify P2P Gateway status
- Check `outgoing:broadcast:batch` queue
- Manually trigger broadcast if needed

### Stuck in AGGREGATING State

**Symptoms:**
- Aggregation window expired but no consensus
- Missing validator batches

**Causes:**
- Network partition
- Validators not broadcasting
- Aggregation queue not processing

**Recovery:**
- Check network connectivity
- Verify validator broadcasts
- Check aggregation queue processing
- Extend aggregation window if needed

### Consensus Not Achieved

**Symptoms:**
- Aggregation completes but no consensus
- Insufficient validator votes

**Causes:**
- Not enough validators participated
- Network partition
- Validator failures

**Recovery:**
- Wait for more validators
- Check validator participation
- Review network health
- May require manual intervention

## State Transition Timing

### Typical Timings

**Submission → Collection:**
- P2P propagation: <500ms
- Queue processing: <100ms
- Total: ~500ms-1s

**Collection → Finalization:**
- Submission window duration: 30s (legacy contract `snapshotSubmissionWindow()`) or dynamic (new contract: P1=45s, PN=45s)
  - During window: Snapshot CIDs collected from snapshotter nodes via P2P network
  - Window closes: Level 1 finalization begins
- Event detection: <1s
- Worker processing: 5-30s (depends on batch size)
- Level 1 aggregation: 1-3s
- Total: ~36-64s (includes 30s submission window for snapshot CID collection)

**Finalization → Broadcast:**
- IPFS upload: 100ms-2s (local/external)
- Queue processing: <100ms
- P2P broadcast: <500ms
- Total: ~1-3s

**Finalization → Aggregation Trigger:**
- Local batch writes to stream: <100ms
- Stream consumer processes: <100ms
- Total: ~200ms

**Aggregation Window (Batch Collection):**
- Window duration: 30s (default, configurable via `AGGREGATION_WINDOW_SECONDS`)
- First batch arrival starts timer
- Additional batches collected during window
- Network propagation for remote batches: <500ms
- Total: ~30s (aggregation window)

**Note**: When commit/reveal is enabled, Level 2 aggregation timing will be tied to Validator Voting Window closure rather than a fixed duration.

**Aggregation → Consensus:**
- Vote counting: <1s
- Consensus calculation: <1s
- Total: ~1-2s

**Total End-to-End:**
- Submission → Consensus: ~45-75s
- Submission → On-Chain: ~60-90s (when implemented)

## State Validation

### Validation Rules

**SUBMITTED:**
- Must have valid EIP-712 signature
- Must be within submission window
- Must have valid project ID

**COLLECTED:**
- Must pass signature verification
- Must pass deduplication check
- Must be associated with valid epoch

**FINALIZED:**
- Must have valid merkle root
- Must have IPFS CID
- Must have BLS signature (when implemented)

**BROADCAST:**
- Must have IPFS CID
- Must be transmitted on correct topic
- Must include validator ID

**AGGREGATING:**
- Must have local batch (or remote batch from stream)
- Must collect remote batches during aggregation window
- Must respect aggregation window timing
- Stream-based unified path: Both local and remote batches trigger via aggregation stream

**CONSENSUS:**
- Must have ≥51% agreement (3 validators) or ≥2/3 (10+ validators)
- Must have valid aggregated projects
- Must track validator contributions

## References

- **Implementation**: `cmd/aggregator/main.go`
- **Redis Keys**: [`REDIS_KEYS.md`](./REDIS_KEYS.md)
- **Aggregator Workflow**: [`AGGREGATOR_WORKFLOW.md`](./AGGREGATOR_WORKFLOW.md)
- **Visual Timeline**: [`imgs/sequencing-timeline.png`](./imgs/sequencing-timeline.png)

