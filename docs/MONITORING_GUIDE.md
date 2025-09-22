# Batch Processing Pipeline Monitoring Guide

## Overview

The enhanced monitoring system provides comprehensive visibility into the parallel batch processing pipeline, tracking data flow from submission collection through final aggregation.

The system now supports both P2PSnapshotSubmission batch format (multiple submissions per message) and single SnapshotSubmission format, with updated Redis key patterns for better organization.

## Quick Start

### Basic Monitoring
```bash
# Check service status
./dsv.sh status

# Monitor pipeline
./dsv.sh monitor

# View all logs
./dsv.sh logs
```

## Pipeline Stages

The monitoring system tracks 6 distinct stages:

### Stage 1: Submission Collection
- **Active Windows**: Open submission windows accepting data (format: `epoch:market:epochID:window`)
- **Queue Depth**: Pending submissions in processing queue
- **Vote Distribution**: Multiple CIDs per project with vote counts
- **Batch vs Single Submissions**: Now handles P2PSnapshotSubmission batch format

### Stage 2: Batch Splitting
- **Split Metadata**: How epochs are divided into parallel batches
- **Finalization Queue**: Batches waiting for processing
- **Batch Size**: Configurable via `FINALIZATION_BATCH_SIZE` (default: 20)

### Stage 3: Parallel Finalization
- **Worker Status**: Individual finalizer worker health
- **Batch Parts**: Processing status of each batch part
- **Worker Assignment**: Which worker handles which batch

### Stage 4: Aggregation
- **Ready Epochs**: Epochs with all parts completed
- **Aggregation Queue**: Epochs awaiting final assembly
- **Aggregator Status**: Single worker combining batch parts

### Stage 5: Final Output
- **Finalized Batches**: Completed batches with IPFS CIDs
- **Merkle Roots**: Cryptographic proofs of batch contents
- **Validator Broadcasts**: Vote dissemination status

### Stage 6: Consensus/Batch Aggregation (NEW)
- **Validator Batch Exchange**: Real-time tracking of batch broadcasts between validators
- **Local Aggregation**: Each validator aggregates all received batches locally
- **Vote Counting**: Per-project CID vote tallies from all validators
- **Consensus Determination**: Local consensus based on majority vote per project
- **No Second Round**: Each validator maintains their own aggregated view - no global consensus round

## Monitoring Commands

### dsv.sh Commands

| Command | Description |
|---------|-------------|
| `monitor` | Basic pipeline status |
| `status` | Service status overview |
| `logs [N]` | All service logs |
| **Component Logs** | |
| `p2p-logs [N]` | P2P Gateway activity |
| `aggregator-logs [N]` | Consensus/aggregation logs |
| `finalizer-logs [N]` | Batch creation logs |
| `dequeuer-logs [N]` | Submission processing logs |
| `event-logs [N]` | Epoch release events |
| `redis-logs [N]` | Redis operation logs |

All log commands support optional line count (default: 100):
```bash
./dsv.sh p2p-logs 200       # Last 200 lines
./dsv.sh aggregator-logs    # Default 100 lines
```

### Direct Script Usage

```bash
# Run comprehensive pipeline monitor
./scripts/monitor_pipeline.sh

# Specify custom container
./scripts/monitor_pipeline.sh container-name
```

## Consensus Monitoring Deep Dive

The new consensus monitoring system provides complete visibility into how validators aggregate batches and determine local consensus. This is critical for understanding the decentralized batch aggregation process.

### Understanding Local Aggregation

**Key Concept**: Each validator maintains their own local aggregation of all received validator batches. There is NO second round of consensus - each validator's local view is their final consensus.

```
Validator A receives batches from: [Val1, Val2, Val3, Val4]
Validator B receives batches from: [Val1, Val2, Val3, Val5]
Validator C receives batches from: [Val1, Val2, Val4, Val5]

Each validator aggregates their received batches independently.
Results may differ slightly based on network conditions.
```

### Consensus Monitoring Commands Explained

#### 1. `./dsv.sh consensus` - Overview Status
Shows the current state of consensus across recent epochs:

**Sample Output**:
```
🤝 Consensus/Aggregation Status
================================
📊 Batch Aggregation Status
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🔄 Recent Aggregation Status:
  📦 Epoch 172883: 4 validators, 25 projects aggregated
  📦 Epoch 172884: 3 validators, 18 projects aggregated

🎯 Consensus Results:
  ✅ Epoch 172883: CID=QmABC123, Merkle=a1b2c3d4..., Projects=25
  ✅ Epoch 172884: CID=QmDEF456, Merkle=e5f6g7h8..., Projects=18

📨 Validator Batches Received:
  📊 4 validators across 2 epochs
```

#### 2. `./dsv.sh aggregated-batch [epoch]` - Complete Aggregation View
Shows the complete local aggregation for a specific epoch:

**Sample Output**:
```
📦 Epoch 172883 - Complete Aggregation View
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

🔍 INDIVIDUAL VALIDATOR BATCHES:

📨 Validator: validator_001
   IPFS CID: QmValidator001BatchCID...
   Merkle: a1b2c3d4e5f6...
   Projects: 25
   PeerID: 12D3KooW...
   Timestamp: 1695123456
   Project Proposals:
     - uniswap_v3: QmUniswapCID...
     - aave_v2: QmAaveCID...
     - compound_v2: QmCompoundCID...

📨 Validator: validator_002
   [Similar structure]

📊 LOCAL AGGREGATION RESULTS:
  Total Validators Participated: 4
  Projects with Consensus: 25
  Last Updated: 2023-09-19T10:30:45Z

🗳️ DETAILED VOTE DISTRIBUTION:
  Project: uniswap_v3
    CID QmUniswapCID...: 4 validator(s)

  Project: aave_v2
    CID QmAaveCID123...: 3 validator(s)
    CID QmAaveCID456...: 1 validator(s)

✅ CONSENSUS WINNERS:
  uniswap_v3: QmUniswapCID...
  aave_v2: QmAaveCID123...
  compound_v2: QmCompoundCID...

👥 VALIDATOR CONTRIBUTIONS:
  validator_001: 25 projects contributed
  validator_002: 23 projects contributed
  validator_003: 20 projects contributed
```

#### 3. `./dsv.sh validator-details <id> [epoch]` - Individual Validator Analysis
Shows what a specific validator proposed and how it compared to consensus:

**Sample Output**:
```
👤 Validator Details: validator_001
====================================
📦 All Batches from Validator validator_001:

🔹 Epoch 172883:
  IPFS CID: QmValidator001Epoch172883...
  Merkle Root: a1b2c3d4e5f6g7h8i9j0...
  Projects: 25
  Timestamp: 1695123456
  Project Proposals:
    - uniswap_v3: QmUniswapCID...
    - aave_v2: QmAaveCID123...
    - compound_v2: QmCompoundCID...

✅ CONSENSUS COMPARISON:
  Projects that reached consensus:
    ✅ uniswap_v3 - ACCEPTED
    ✅ aave_v2 - ACCEPTED
    ❌ compound_v2 - Different CID won
```

#### 4. `./dsv.sh consensus-logs [N]` - Activity Logs
Filters system logs to show only consensus-related activity:

**Sample Output**:
```
INFO[2023-09-19T10:30:15Z] 📡 Broadcasted finalized batch for epoch 172883
INFO[2023-09-19T10:30:16Z] 📨 Received batch from validator validator_002 for epoch 172883
INFO[2023-09-19T10:30:17Z] 📊 AGGREGATION for epoch 172883: 4 validators contributed, 25 projects aggregated
```

## Redis Keys Structure

### Consensus/Aggregation Tracking (NEW)
```
# Validator batch storage
validator:{validatorID}:batch:{epochID}              # Individual validator's batch for epoch
validator:{validatorID}:epoch:{epochID}:batch        # Alternative key pattern

# Aggregation status
consensus:epoch:{epochID}:status                     # Local aggregation status for epoch
consensus:epoch:{epochID}:result                     # Final local consensus determination

# Epoch validator tracking
epoch:{epochID}:validators                           # Set of validators who submitted batches
```

### Submission Tracking (Updated Format)
```
submissionQueue                                     # Raw P2P submissions
{protocol}:{market}:processed:{sequencerID}:{id}    # Individual processed submissions (10min TTL)
{protocol}:{market}:epoch:{epochId}:processed       # Set of submission IDs per epoch (1hr TTL)
{protocol}:{market}:epoch:{epochId}:project:{pid}:votes # Vote counts per CID

# New consensus tracking keys
consensus:{epochId}:total_validators                # Total validators for epoch
consensus:{epochId}:voting_progress                 # Overall consensus voting status
consensus:{epochId}:project:{pid}:votes             # Per-project validator votes
consensus:{epochId}:project:{pid}:cid               # Winning CID for project
consensus:{epochId}:status                          # Epoch consensus status (pending/complete/failed)
```

### Window Management (Updated Format)
```
epoch:{market}:{epochId}:window                     # Active submission windows
{protocol}:{market}:batch:ready:{epochId}          # Ready batches for finalization
batch:finalized:{epochId}                          # Finalized batches with metadata
```

### Batch Processing
```
{protocol}:{market}:epoch:{epochId}:batch:meta      # Split batch metadata
{protocol}:{market}:finalizationQueue               # Queue of batch parts
batch:{epochId}:part:{partId}:status                # Part processing status
epoch:{epochId}:parts:completed                     # Completed parts count
epoch:{epochId}:parts:total                         # Total parts count
```

### Worker Monitoring
```
worker:finalizer:{id}:status              # Worker current status
worker:finalizer:{id}:heartbeat           # Last heartbeat timestamp
worker:finalizer:{id}:current_batch       # Batch being processed
worker:finalizer:{id}:batches_processed   # Total processed count
worker:aggregator:status                  # Aggregator status
worker:aggregator:current_epoch           # Epoch being aggregated
```

### Performance Metrics
```
metrics:total_processed                   # Total submissions processed
metrics:processing_rate                   # Submissions per minute
metrics:avg_latency                       # Average processing time
```

## Worker Status Values

| Status | Description |
|--------|-------------|
| `idle` | Worker available for tasks |
| `processing` | Actively processing batch |
| `failed` | Error state requiring attention |

## Consensus Status Values (NEW)

| Status | Description |
|--------|-------------|
| `initializing` | Consensus process starting |
| `voting` | Collecting validator votes |
| `threshold_reached` | Consensus vote threshold achieved |
| `timeout` | Consensus voting window expired |
| `finalized` | Batch approved and ready |
| `rejected` | Batch did not meet consensus requirements

## Health Indicators

### Healthy Pipeline
- ✅ Queue depth < 100
- ✅ Finalization queue < 10 batches
- ✅ Worker heartbeats < 60s old
- ✅ Aggregation queue < 5 epochs
- ✅ **NEW**: Regular validator batch exchange (>= 2 validators per epoch)
- ✅ **NEW**: Consensus determination completing (projects have majority votes)
- ✅ **NEW**: IPFS batch retrieval succeeding

### Warning Signs
- ⚠️ Queue depth > 100 (backlog forming)
- ⚠️ Worker heartbeat > 60s (stale worker)
- ⚠️ Finalization queue > 10 (workers overwhelmed)
- ⚠️ **NEW**: Only 1 validator participating (no aggregation possible)
- ⚠️ **NEW**: High vote divergence (no clear majority for projects)
- ⚠️ **NEW**: IPFS retrieval failures for validator batches
- ⚠️ **NEW**: Stale validator batches (no recent broadcasts)

### Critical Issues
- 🔴 Queue depth > 1000 (severe backlog)
- 🔴 No worker heartbeats (workers down)
- 🔴 Aggregation blocked (parts incomplete)
- 🔴 **NEW**: No validator batch exchange (network partition)
- 🔴 **NEW**: Validator batch signature validation failures
- 🔴 **NEW**: Consistent IPFS failures (storage layer down)

## Integration with Workers

When implementing parallel workers, use the provided monitoring utilities:

```go
import "github.com/powerloom/snapshot-sequencer-validator/pkgs/workers"

// Initialize worker monitor
monitor := workers.NewWorkerMonitor(
    redisClient,
    "worker-1",
    workers.WorkerTypeFinalizer,
    "powerloom-localnet",
)

// Start worker with monitoring
monitor.StartWorker(ctx)

// Track processing
monitor.ProcessingStarted("epoch:123:batch:5")
// ... do work ...
monitor.ProcessingCompleted()

// Handle failures
if err != nil {
    monitor.ProcessingFailed(err)
}
```

## Performance Optimization

### Batch Size Tuning
Adjust `FINALIZATION_BATCH_SIZE` based on:
- Number of projects per epoch
- Available worker capacity
- Network latency requirements

```bash
# For high-volume epochs (500+ projects)
FINALIZATION_BATCH_SIZE=50

# For low-latency requirements
FINALIZATION_BATCH_SIZE=10
```

### Worker Scaling
Monitor queue depths to determine scaling needs:
- Queue > 100: Add more dequeuer workers
- Finalization queue > 10: Add finalizer workers
- Aggregation queue > 5: Optimize aggregator

## Troubleshooting

### No Active Windows
```bash
# Check event monitor is running
./dsv.sh event-monitor-logs

# Verify EVENT_START_BLOCK setting
grep EVENT_START_BLOCK .env
```

### Workers Not Visible
Workers only appear when implemented. Current status:
- ✅ Dequeuer workers (5 instances)
- ⏳ Finalizer workers (TODO)
- ⏳ Aggregation worker (TODO)

### Queue Backlog
```bash
# Check dequeuer performance
./dsv.sh dqr-logs | grep "Processing submission"

# Monitor queue depth trends
watch -n 5 "./dsv.sh monitor | grep 'Pending'"
```

### Data Format Issues
The system now handles both batch and single submission formats with advanced conversion strategies:
```bash
# Check for batch format submissions
./dsv.sh dqr-logs | grep "P2PSnapshotSubmission"

# Check field name changes (snake_case → camelCase)
./dsv.sh dqr-logs | grep "epochId\|projectId"

# Verify conversion strategy
docker exec powerloom-sequencer-validator-dequeuer-1 \
  printenv SUBMISSION_FORMAT_STRATEGY

# Manual format override
# Possible values: 'auto', 'single', 'batch'
SUBMISSION_FORMAT_STRATEGY=single ./dsv.sh distributed
```

#### Conversion Troubleshooting
- **Auto Strategy**: Automatically detects and converts submission formats
- **Single Strategy**: Forces single submission parsing
- **Batch Strategy**: Forces batch submission parsing

##### Common Conversion Mappings
| Old (snake_case) | New (camelCase) |
|-----------------|----------------|
| `epoch_id`     | `epochId`       |
| `project_id`   | `projectId`     |
| `market_id`    | `marketId`      |
| `submitter`    | `submitterAddress` |

**Warning Signs**:
- Logs showing parsing errors
- Submissions not being processed
- Inconsistent queue depths
- Metrics not being collected

##### Debugging Conversion
1. Check logs for detailed parsing information
2. Verify container environment variables
3. Test with explicit conversion strategy
4. Monitor metrics and queue processing

**Tip**: Always use the most recent version of launch scripts and monitor containers for the latest conversion utilities.

### Stale Heartbeats
Indicates worker process issues:
1. Check container health
2. Review worker logs
3. Restart affected workers

## Advanced Monitoring

### Custom Metrics Collection
```bash
# Export metrics to file
docker exec sequencer-container redis-cli --csv HGETALL metrics:* > metrics.csv

# Real-time metric streaming
watch -n 1 'docker exec sequencer-container redis-cli GET metrics:processing_rate'
```

### Batch Tracking Query
```bash
# Find all batches for specific epoch
docker exec sequencer-container redis-cli KEYS "batch:123:*"

# Check specific batch part status
docker exec sequencer-container redis-cli GET "batch:123:part:5:status"
```

## Debugging Tips

### Using Combined Logs

The combined log commands are particularly useful for debugging cross-component issues:

1. **Collection Issues** (`collection-logs`):
   - Track submissions from P2P receipt through window collection
   - Verify dequeuer is storing submissions with correct keys
   - Confirm event monitor is finding stored submissions

2. **Finalization Issues** (`finalization-logs`):
   - Verify batches are pushed to finalization queue
   - Check if finalizers are picking up batches
   - Track consensus selection and batch creation

3. **Full Pipeline Issues** (`pipeline-logs`):
   - End-to-end submission tracking
   - Identify where submissions get stuck
   - Correlate timing between components

### Common Issues and Solutions

| Issue | Check With | Solution |
|-------|------------|----------|
| Submissions not collected | `collection-logs` | Verify protocolState matches between components |
| Finalizers idle | `finalization-logs` | Check finalization queue has batches |
| Missing projects | `pipeline-logs` | Ensure JSON parsing matches data structure |
| Queue buildup | `monitor` | Increase worker count or check for errors |

## Future Enhancements

Planned monitoring improvements:
- HTTP metrics endpoint (`/metrics`)
- Prometheus integration
- Grafana dashboards
- WebSocket real-time updates
- Historical trend analysis

### Consensus Troubleshooting

#### No Validator Batches Received
```bash
# Check if validator is broadcasting
./dsv.sh consensus-logs | grep "Broadcasted finalized batch"

# Check P2P connectivity
./dsv.sh listener-logs | grep -E "Connected|peer"

# Verify gossipsub topic subscription
./dsv.sh listener-logs | grep "/powerloom/finalized-batches"
```

#### High Vote Divergence
```bash
# Check project-level vote distribution
./dsv.sh aggregated-batch 12345

# Look for inconsistent project CIDs across validators
# This may indicate:
# - Different submission sets received
# - Network timing issues
# - IPFS content differences
```

#### Validator Batches Not Aggregating
```bash
# Check minimum validator threshold
./dsv.sh consensus | grep "validators contributed"

# If < MinValidatorsForConsensus (usually 2), aggregation won't trigger
# Check Redis for validator presence
docker exec <container> redis-cli SMEMBERS "epoch:12345:validators"
```

#### IPFS Batch Retrieval Failures
```bash
# Check IPFS connectivity
./dsv.sh listener-logs | grep -E "IPFS|fetch.*batch"

# Look for IPFS errors in consensus logs
./dsv.sh consensus-logs | grep -E "Failed to fetch|IPFS"

# Verify IPFS client configuration
docker exec <container> printenv | grep IPFS
```

#### Consensus Status Not Updating
```bash
# Check Redis consensus keys exist
docker exec <container> redis-cli KEYS "consensus:epoch:*"

# Look for aggregation processing logs
./dsv.sh consensus-logs | grep "AGGREGATION"

# Verify periodic consensus checker is running
./dsv.sh consensus-logs | grep "BATCH AGGREGATION STATUS"
```

### Consensus Monitoring Roadmap
- Detailed validator performance tracking
- Stake-weighted voting visualization
- Cross-epoch validator reputation system
- Advanced consensus game theory modeling
- Automated validator onboarding/offboarding tools

## Related Documentation

- [VPS_DEPLOYMENT.md](../VPS_DEPLOYMENT.md) - Deployment and operations guide
- [PHASE_2_CURRENT_WORK.md](../../PHASE_2_CURRENT_WORK.md) - Architecture overview
- [PROJECT_STATUS.md](../../PROJECT_STATUS.md) - Implementation status